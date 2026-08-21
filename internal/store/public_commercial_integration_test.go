package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/migrations"
)

func TestPublicCommercialUpgradeFrom0018Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_UPGRADE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_UPGRADE_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.pool.Exec(ctx, `CREATE TABLE schema_migrations (
		version bigint PRIMARY KEY,name text NOT NULL,checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations.All {
		if migration.Version > 18 {
			break
		}
		tx, beginErr := s.pool.Begin(ctx)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		sum := sha256.Sum256([]byte(migration.SQL))
		if _, err = tx.Exec(ctx, migration.SQL); err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version,name,checksum) VALUES($1,$2,$3)`,
				migration.Version, migration.Name, hex.EncodeToString(sum[:]))
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatalf("commit migration %d: %v", migration.Version, err)
		}
	}
	acquiredUserID, administratorID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'upgrade-hash','Acquired User','USER','ACTIVE',now()),
		($3,$4,'upgrade-hash','Administrator','ADMIN','ACTIVE',now())`, acquiredUserID,
		"upgrade-acquired-"+acquiredUserID+"@example.invalid", administratorID,
		"upgrade-admin-"+administratorID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,action,resource_type,resource_id,after_state)
		VALUES($1,$2,'security.registration_created','user',$2::uuid::text,
		'{"mode":"PUBLIC","status":"ACTIVE"}'::jsonb)`, id.UUID(), acquiredUserID); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 2; index++ {
		if _, err = s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,user_id,organization_id,project_id,
			requested_model,resolved_model,endpoint,status_code,created_at)
			VALUES($1,$2,$3,$4,$5,'upgrade-model','upgrade-model','responses',200,
				now()+make_interval(secs => $6::double precision/1000.0))`,
			id.UUID(), fmt.Sprintf("upgrade-request-%d-%s", index, id.UUID()), acquiredUserID,
			domain.LegacyOrganizationID, domain.LegacyProjectID, index); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var latestVersion int
	if err = s.pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latestVersion); err != nil {
		t.Fatal(err)
	}
	if latestVersion != 21 {
		t.Fatalf("latest migration=%d", latestVersion)
	}
	for _, eventType := range []string{FunnelRegistered, FunnelEmailVerified, FunnelFirstAPICall, FunnelSecondAPICall} {
		var count int
		if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events
			WHERE user_id=$1 AND event_type=$2`, acquiredUserID, eventType).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("backfilled %s count=%d", eventType, count)
		}
	}
	var excludedAdmin, automaticSubscription int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events WHERE user_id=$1`, administratorID).
		Scan(&excludedAdmin); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events
		WHERE organization_id=$1 AND event_type='FIRST_SUBSCRIPTION'`, domain.LegacyOrganizationID).
		Scan(&automaticSubscription); err != nil {
		t.Fatal(err)
	}
	if excludedAdmin != 0 || automaticSubscription != 0 {
		t.Fatalf("upgrade misclassified admin=%d automatic_subscription=%d", excludedAdmin, automaticSubscription)
	}
}

func TestCommercialFunnelConcurrencyIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// A direct administrator-created account is not a public acquisition.
	adminID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'integration-hash','Integration Admin','ADMIN','ACTIVE',now())`,
		adminID, "admin-"+adminID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	var adminRegistrations int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events
		WHERE user_id=$1 AND event_type='REGISTERED'`, adminID).Scan(&adminRegistrations); err != nil {
		t.Fatal(err)
	}
	if adminRegistrations != 0 {
		t.Fatalf("administrator bootstrap/create was counted as public registration: %d", adminRegistrations)
	}

	verificationDigest := sha256.Sum256([]byte("verification:" + id.UUID()))
	userEmail := "funnel-" + id.UUID() + "@example.invalid"
	outboxID := id.UUID()
	registered, err := s.RegisterUser(ctx, userEmail, "integration-password-hash", "Funnel User", "PUBLIC",
		nil, nil, verificationDigest[:], time.Now().UTC().Add(time.Hour), domain.EmailOutbox{
			ID: outboxID, Recipient: userEmail, Template: "VERIFY_EMAIL", EncryptedMessage: []byte("encrypted-test-envelope"),
			DedupeKey: "funnel-verification:" + outboxID, MaxAttempts: 2, AvailableAt: time.Now().UTC(),
		}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !registered.Created || registered.Active {
		t.Fatalf("unexpected registration result=%+v", registered)
	}
	user, err := s.VerifyEmail(ctx, verificationDigest[:], "")
	if err != nil {
		t.Fatal(err)
	}
	var organizationID, projectID, defaultSubscriptionID string
	if err = s.pool.QueryRow(ctx, `SELECT o.id FROM organizations o JOIN organization_memberships m
		ON m.organization_id=o.id WHERE m.user_id=$1 AND m.role='OWNER' ORDER BY o.created_at DESC LIMIT 1`, user.ID).
		Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT p.id FROM projects p JOIN project_memberships m ON m.project_id=p.id
		WHERE p.organization_id=$1 AND m.user_id=$2 ORDER BY p.created_at,p.id LIMIT 1`, organizationID, user.ID).
		Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT id FROM organization_subscription WHERE organization_id=$1
		ORDER BY created_at,id LIMIT 1`, organizationID).Scan(&defaultSubscriptionID); err != nil {
		t.Fatal(err)
	}
	var automaticSubscriptions int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events
		WHERE organization_id=$1 AND event_type='FIRST_SUBSCRIPTION'`, organizationID).Scan(&automaticSubscriptions); err != nil {
		t.Fatal(err)
	}
	if automaticSubscriptions != 0 {
		t.Fatalf("automatic free subscription completed the acquisition funnel: %d", automaticSubscriptions)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO subscription_event(id,organization_id,organization_subscription_id,
		event_type,from_status,to_status,actor_id,idempotency_key,payload)
		VALUES($1,$2,$3,'PLAN_CHANGED_IMMEDIATELY','ACTIVE','ACTIVE',$4,$5,'{}'::jsonb)`,
		id.UUID(), organizationID, defaultSubscriptionID, user.ID, "explicit-plan-selection:"+id.UUID()); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events
		WHERE organization_id=$1 AND event_type='FIRST_SUBSCRIPTION'`, organizationID).Scan(&automaticSubscriptions); err != nil {
		t.Fatal(err)
	}
	if automaticSubscriptions != 1 {
		t.Fatalf("explicit plan selection milestone count=%d", automaticSubscriptions)
	}
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	disabledModelID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,enabled)
		VALUES($1,$2,$3,'Internal Disabled Model',false)`, disabledModelID, providerID, "internal-disabled-"+disabledModelID); err != nil {
		t.Fatal(err)
	}
	publicModels, err := s.ListPublicModels(ctx, "CN", "USD")
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range publicModels {
		if model.ID == disabledModelID {
			t.Fatal("disabled internal model appeared in the public catalog")
		}
	}

	// Project-scoped onboarding is derived from authoritative key/request/usage rows.
	secondProjectID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO projects(id,organization_id,name,slug,status)
		VALUES($1::uuid,$2::uuid,'Second','second-'||substr($1::uuid::text,1,8),'ACTIVE')`, secondProjectID, organizationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO project_memberships(organization_id,project_id,user_id,role,status)
		VALUES($1,$2,$3,'ADMIN','ACTIVE')`, organizationID, secondProjectID, user.ID); err != nil {
		t.Fatal(err)
	}
	keyID := id.UUID()
	keyDigest := sha256.Sum256([]byte("project-key:" + keyID))
	if _, err = s.pool.Exec(ctx, `INSERT INTO api_keys(id,user_id,organization_id,project_id,name,environment,
		key_prefix,key_hash,status) VALUES($1,$2,$3,$4,'Integration','test','rdk_test_integration',$5,'ACTIVE')`,
		keyID, user.ID, organizationID, secondProjectID, keyDigest[:]); err != nil {
		t.Fatal(err)
	}
	status, err := s.UserOnboardingStatus(ctx, user.ID, organizationID, secondProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if status.ProjectID != secondProjectID || !onboardingCompleted(status, "CREATE_API_KEY") || onboardingCompleted(status, "FIRST_API_CALL") {
		t.Fatalf("project-scoped pre-call onboarding=%+v", status)
	}
	requestID := "project-two-request-" + id.UUID()
	if err = insertIntegrationFundedRequest(ctx, s, user.ID, organizationID, secondProjectID, requestID); err != nil {
		t.Fatal(err)
	}
	status, err = s.UserOnboardingStatus(ctx, user.ID, organizationID, secondProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if !onboardingCompleted(status, "FIRST_API_CALL") || onboardingCompleted(status, "VIEW_USAGE_AND_CHARGE") {
		t.Fatalf("request without billing evidence completed the wrong steps=%+v", status.Steps)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,
		model,amount,currency,status) VALUES($1,$2,$3,$4,'integration-model',0.01,'USD','CHARGED')`,
		id.UUID(), requestID, organizationID, secondProjectID); err != nil {
		t.Fatal(err)
	}
	status, err = s.UserOnboardingStatus(ctx, user.ID, organizationID, secondProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if !onboardingCompleted(status, "VIEW_USAGE_AND_CHARGE") {
		t.Fatalf("billing evidence did not complete usage/charge step=%+v", status.Steps)
	}

	// Concurrent successful, terminally-settled requests produce exactly one
	// first-call and one second-call milestone for the acquired user.
	concurrentUserID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'integration-hash','Concurrent Funnel User','USER','ACTIVE',now())`,
		concurrentUserID, "concurrent-"+concurrentUserID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `SELECT record_commercial_funnel_event(
		'REGISTERED',$1::uuid,NULL,NULL,'integration_user',$1::uuid::text,'funnel:registered:'||$1::uuid::text,now(),
		'{"acquisition_source":"SELF_REGISTRATION"}'::jsonb)`, concurrentUserID); err != nil {
		t.Fatal(err)
	}
	const concurrentCalls = 20
	requestIDs := make([]string, concurrentCalls)
	for index := range requestIDs {
		requestIDs[index] = fmt.Sprintf("concurrent-funnel-%s-%02d", id.UUID(), index)
		if err = insertIntegrationFundingOperation(ctx, s, organizationID, projectID, requestIDs[index]); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, concurrentCalls)
	for _, concurrentRequestID := range requestIDs {
		wait.Add(1)
		go func(value string) {
			defer wait.Done()
			_, insertErr := s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,user_id,organization_id,project_id,
				requested_model,resolved_model,endpoint,status_code,funding_operation_id)
				SELECT $1,$2,$3,$4,$5,'integration-model','integration-model','responses',200,id
				FROM funding_operation WHERE request_id=$2`, id.UUID(), value, concurrentUserID, organizationID, projectID)
			if insertErr != nil {
				errorsFound <- insertErr
			}
		}(concurrentRequestID)
	}
	wait.Wait()
	close(errorsFound)
	for insertErr := range errorsFound {
		t.Error(insertErr)
	}
	if t.Failed() {
		t.FailNow()
	}
	redirectRequestID := "redirect-not-success-" + id.UUID()
	if err = insertIntegrationFundingOperation(ctx, s, organizationID, projectID, redirectRequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,user_id,organization_id,project_id,
		requested_model,resolved_model,endpoint,status_code,funding_operation_id)
		SELECT $1,$2,$3,$4,$5,'integration-model','integration-model','responses',302,id
		FROM funding_operation WHERE request_id=$2`, id.UUID(), redirectRequestID, concurrentUserID,
		organizationID, projectID); err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{FunnelFirstAPICall, FunnelSecondAPICall} {
		var count int
		if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM commercial_funnel_events
			WHERE user_id=$1 AND event_type=$2`, concurrentUserID, eventType).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s milestone count=%d", eventType, count)
		}
	}
	var successfulCalls int64
	if err = s.pool.QueryRow(ctx, `SELECT successful_call_count FROM commercial_funnel_api_call_counter
		WHERE user_id=$1`, concurrentUserID).Scan(&successfulCalls); err != nil {
		t.Fatal(err)
	}
	if successfulCalls != concurrentCalls {
		t.Fatalf("successful call counter=%d want=%d", successfulCalls, concurrentCalls)
	}

	// Anonymous visits are HMAC-only, replay safe, payload-bound, and do not
	// serialize the privileged tamper-evident audit chain.
	var auditBefore, auditAfter int64
	if err = s.pool.QueryRow(ctx, `SELECT sealed_entries FROM audit_log_chain_state WHERE singleton=true`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	homeKey := "homepage:" + id.UUID()
	homeHash := sha256.Sum256([]byte("anonymous-browser-session:" + id.UUID()))
	firstHome, replayed, err := s.RecordAnonymousHomepageVisit(ctx, homeHash[:], homeKey)
	if err != nil || replayed {
		t.Fatalf("first homepage event=%+v replayed=%t err=%v", firstHome, replayed, err)
	}
	secondHome, replayed, err := s.RecordAnonymousHomepageVisit(ctx, homeHash[:], homeKey)
	if err != nil || !replayed || secondHome.ID != firstHome.ID {
		t.Fatalf("homepage replay=%+v replayed=%t err=%v", secondHome, replayed, err)
	}
	differentHash := sha256.Sum256([]byte("different-anonymous-browser"))
	if _, _, err = s.RecordAnonymousHomepageVisit(ctx, differentHash[:], homeKey); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different anonymous payload replay error=%v", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT sealed_entries FROM audit_log_chain_state WHERE singleton=true`).Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if auditAfter != auditBefore {
		t.Fatalf("homepage analytics polluted serialized audit chain: before=%d after=%d", auditBefore, auditAfter)
	}

	// Administrator publication is append-only, transactionally audited, and
	// safe when replicas submit the same idempotent command concurrently.
	termsRequest := PublishCommercialTermsRequest{
		Region: "CN", Currency: "CNY", TaxDisclosure: "Pending qualified legal review.",
		RefundSummary: "Pending qualified legal review.", RefundPolicyURL: "/legal/refunds",
		BonusCreditAmount: "0", BonusNonRefundable: true, EffectiveAt: time.Now().UTC(),
		LegalReviewStatus: "PENDING", IdempotencyKey: "public-terms:" + id.UUID(), CreatedBy: adminID,
	}
	const concurrentPublishes = 8
	wait = sync.WaitGroup{}
	publishErrors := make(chan error, concurrentPublishes)
	for range concurrentPublishes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, publishErr := s.PublishCommercialTerms(ctx, termsRequest)
			if publishErr != nil {
				publishErrors <- publishErr
			}
		}()
	}
	wait.Wait()
	close(publishErrors)
	for publishErr := range publishErrors {
		t.Error(publishErr)
	}
	var publishedRows, publicationAudits int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM public_commercial_terms WHERE idempotency_key=$1`,
		termsRequest.IdempotencyKey).Scan(&publishedRows); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs audit
		WHERE audit.action='commercial.public_terms.publish' AND audit.resource_id=(
			SELECT terms.id::text FROM public_commercial_terms terms WHERE terms.idempotency_key=$1
		)`, termsRequest.IdempotencyKey).Scan(&publicationAudits); err != nil {
		t.Fatal(err)
	}
	if publishedRows != 1 || publicationAudits != 1 {
		t.Fatalf("published terms rows=%d audit_rows=%d", publishedRows, publicationAudits)
	}
	changedTerms := termsRequest
	changedTerms.BonusCreditAmount = "1"
	if _, _, err = s.PublishCommercialTerms(ctx, changedTerms); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed terms replay error=%v", err)
	}
	feeRequest := PublishPaymentFeeRequest{
		FeeCategory: "PAYMENT_CHANNEL", PaymentProvider: "manual_transfer", Region: "CN", Currency: "CNY",
		FeeKind: "NONE", FixedAmount: "0", RateBPS: 0, ChargedToCustomer: false,
		Description: "No separately configured customer payment-channel fee; pending legal review.",
		EffectiveAt: time.Now().UTC(), LegalReviewStatus: "PENDING",
		IdempotencyKey: "public-fee:" + id.UUID(), CreatedBy: adminID,
	}
	if _, replay, publishErr := s.PublishPaymentFee(ctx, feeRequest); publishErr != nil || replay {
		t.Fatalf("publish payment fee replay=%t err=%v", replay, publishErr)
	}
	if _, replay, publishErr := s.PublishPaymentFee(ctx, feeRequest); publishErr != nil || !replay {
		t.Fatalf("replay payment fee replay=%t err=%v", replay, publishErr)
	}
	catalog, err := s.PublicPricing(ctx, "CN", "CNY")
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.TermsConfigured || catalog.CommercialTerms == nil || catalog.CommercialTerms.ID == "" ||
		!catalog.PaymentFeesConfigured || len(catalog.PaymentFees) == 0 {
		t.Fatalf("published commercial pricing was not visible: %+v", catalog)
	}
	dimensions, err := s.PublicCatalogDimensions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !integrationContains(dimensions.Regions, "CN") || !integrationContains(dimensions.Currencies, "CNY") {
		t.Fatalf("public catalog dimensions=%+v", dimensions)
	}
}

func insertIntegrationFundedRequest(ctx context.Context, s *Store, userID, organizationID, projectID, requestID string) error {
	if err := insertIntegrationFundingOperation(ctx, s, organizationID, projectID, requestID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,user_id,organization_id,project_id,
		requested_model,resolved_model,endpoint,status_code,funding_operation_id)
		SELECT $1,$2,$3,$4,$5,'integration-model','integration-model','responses',200,id
		FROM funding_operation WHERE request_id=$2`, id.UUID(), requestID, userID, organizationID, projectID)
	return err
}

func insertIntegrationFundingOperation(ctx context.Context, s *Store, organizationID, projectID, requestID string) error {
	var walletID, currency string
	if err := s.pool.QueryRow(ctx, `SELECT id,currency FROM wallets WHERE organization_id=$1`, organizationID).
		Scan(&walletID, &currency); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,
		idempotency_key,request_fingerprint,status,currency,maximum_amount,settled_amount,
		estimated_input_tokens,max_output_tokens,actual_input_tokens,actual_cached_input_tokens,
		actual_output_tokens,usage_source,settled_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,'SETTLED',$8,0.01,0.01,1,1,1,0,1,'PROVIDER_REPORTED',now())`,
		id.UUID(), walletID, organizationID, projectID, requestID, "funding:"+requestID,
		"fingerprint:"+requestID, currency)
	return err
}

func onboardingCompleted(status OnboardingStatus, key string) bool {
	for _, step := range status.Steps {
		if step.Key == key {
			return step.Completed
		}
	}
	return false
}

func integrationContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
