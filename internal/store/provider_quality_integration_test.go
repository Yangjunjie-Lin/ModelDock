package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestProviderQualityEvidenceConcurrencyAndCircuitIntegration(t *testing.T) {
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
	adminID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Quality Admin','ADMIN','ACTIVE',now())`, adminID, "quality-admin-"+adminID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,commercial_status='COMMERCIAL_APPROVED',
		commercial_resale_status='APPROVED',allowed_customer_regions='["*"]',pricing_disabled=false,
		emergency_kill_switch=false,contract_start_at=now()-interval '1 day',contract_end_at=now()+interval '1 day'
		WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	modelID := id.UUID()
	modelName := "quality-integration-" + modelID
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,model_type,enabled,allowed_regions)
		VALUES($1,$2,$3,$3,'text',true,'["*"]')`, modelID, providerID, modelName); err != nil {
		t.Fatal(err)
	}
	policy := domain.ProviderQualityPolicy{ProviderID: providerID, Enabled: true, ProbeModelID: &modelID,
		ProbeIntervalSeconds: 300, ProbeTimeoutMS: 30000, EvaluationWindowMinutes: 30, MinimumSamples: 2,
		AvailabilityTargetPct: domain.Decimal("99"), MaximumErrorRatePct: domain.Decimal("5"),
		Maximum429RatePct: domain.Decimal("10"), MaximumTTFTMS: 1000, MaximumFullLatencyMS: 5000,
		MinimumThroughputTPS: domain.Decimal("1"), MinimumOutputQualityScore: domain.Decimal("90"),
		RequiredTestRegions: []string{}, AutoDownweightEnabled: true, AutoCircuitBreakerEnabled: true,
		CircuitFailureThreshold: 2, CircuitRecoveryThreshold: 2, CircuitOpenSeconds: 300,
		RampEnabled: true, RampInitialBPS: 500, RampStepBPS: 500, RampStepIntervalSeconds: 3600}
	if _, err = s.UpsertProviderQualityPolicy(ctx, policy, adminID); err != nil {
		t.Fatal(err)
	}
	supplierID, supplierOrganizationID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,billing_region,metadata)
		VALUES($1,'Quality Supplier Org',$2,'ACTIVE','CN','{}')`, supplierOrganizationID, "quality-supplier-"+supplierID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO supplier_organizations(id,organization_id,owner_user_id,legal_name,display_name,
			registration_number,incorporation_country,kyb_status,contract_status,status)
			VALUES($1,$2,$3,'Quality Supplier','Quality Supplier',$4,'CN','VERIFIED','ACTIVE','APPROVED')`,
			supplierID, supplierOrganizationID, adminID, "QUALITY-"+supplierID)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkSupplierProvider(ctx, providerID, supplierID, "synthetic controlled ramp verification", adminID); err != nil {
		t.Fatal(err)
	}
	allowedRequest, deniedRequest := "", ""
	for index := 0; index < 10000 && (allowedRequest == "" || deniedRequest == ""); index++ {
		candidate := fmt.Sprintf("manual-ramp-%d", index)
		if qualityTrafficAdmitted(candidate, providerID, "manual-route", 500) {
			allowedRequest = candidate
		} else {
			deniedRequest = candidate
		}
	}
	if allowed, gateErr := s.providerQualityTrafficAdmitted(ctx, providerID, "manual-route", allowedRequest); gateErr != nil || !allowed {
		t.Fatalf("manual ramp rejected admitted bucket: allowed=%v err=%v", allowed, gateErr)
	}
	if allowed, gateErr := s.providerQualityTrafficAdmitted(ctx, providerID, "manual-route", deniedRequest); gateErr != nil || allowed {
		t.Fatalf("manual ramp bypassed denied bucket: allowed=%v err=%v", allowed, gateErr)
	}
	if summaries, listErr := s.ListProviderQualitySummaries(ctx); listErr != nil || len(summaries) == 0 {
		t.Fatalf("quality summary projection failed: count=%d err=%v", len(summaries), listErr)
	}

	observation := domain.ProviderQualityObservation{IdempotencyKey: "quality-concurrent-" + id.UUID(), ProviderID: providerID,
		Source: "PLATFORM_TRAFFIC", StatusCode: intPointer(200), Succeeded: true, TTFTMS: int64Pointer(100),
		FullLatencyMS: int64Pointer(500), OutputTokens: int64Pointer(10), ThroughputTPS: decimalTestPointer("25"),
		OutputQualityScore: decimalTestPointer("100"), ObservedAt: time.Now().UTC()}
	var wait sync.WaitGroup
	errCh := make(chan error, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, recordErr := s.RecordProviderQualityObservation(ctx, observation)
			errCh <- recordErr
		}()
	}
	wait.Wait()
	close(errCh)
	for recordErr := range errCh {
		if recordErr != nil {
			t.Fatal(recordErr)
		}
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM provider_quality_observations WHERE idempotency_key=$1`, observation.IdempotencyKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent observation count=%d err=%v", count, err)
	}
	second := observation
	second.ID, second.IdempotencyKey = "", "quality-second-"+id.UUID()
	if _, _, err = s.RecordProviderQualityObservation(ctx, second); err != nil {
		t.Fatal(err)
	}
	evaluationAt := time.Now().UTC().Add(time.Second)
	errCh = make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, evaluationErr := s.EvaluateProviderQuality(ctx, providerID, evaluationAt)
			errCh <- evaluationErr
		}()
	}
	wait.Wait()
	close(errCh)
	for evaluationErr := range errCh {
		if evaluationErr != nil {
			t.Fatal(evaluationErr)
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM provider_quality_rollups WHERE provider_id=$1 AND window_end=$2`, providerID, evaluationAt).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent rollup count=%d err=%v", count, err)
	}

	for index := 0; index < 4; index++ {
		failed := domain.ProviderQualityObservation{IdempotencyKey: fmt.Sprintf("quality-failure-%s-%d", modelID, index),
			ProviderID: providerID, Source: "PLATFORM_TRAFFIC", StatusCode: intPointer(500), Succeeded: false,
			FullLatencyMS: int64Pointer(7000), ErrorClass: "http_5xx", ObservedAt: time.Now().UTC()}
		if _, _, err = s.RecordProviderQualityObservation(ctx, failed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.EvaluateProviderQuality(ctx, providerID, evaluationAt.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	state, err := s.EvaluateProviderQuality(ctx, providerID, evaluationAt.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.CircuitState != "OPEN" || state.RoutingMultiplier.String() != "0.000000" || state.TrafficCapBPS != 0 {
		t.Fatalf("quality circuit did not fail closed: %+v", state)
	}
	publicProviders, err := s.ListPublicProviders(ctx, "CN")
	if err != nil {
		t.Fatal(err)
	}
	var foundPublic bool
	for _, publicProvider := range publicProviders {
		if publicProvider.ID == providerID {
			foundPublic = true
			if publicProvider.QualitySource != "PLATFORM_MEASURED" || publicProvider.QualityGrade == "UNKNOWN" || publicProvider.DeclaredUptime != "" {
				t.Fatalf("public quality/declaration boundary failed: %+v", publicProvider)
			}
		}
	}
	if !foundPublic {
		t.Fatal("quality Provider missing from public projection")
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", providerID, modelName); !errors.Is(err, ErrProviderQualityCircuitOpen) {
		t.Fatalf("dispatch admission ignored quality circuit: %v", err)
	}
	resetState, err := s.ResetProviderQualityCircuit(ctx, providerID, "synthetic recovery check", adminID)
	if err != nil || resetState.CircuitState != "HALF_OPEN" {
		t.Fatalf("audited half-open reset failed: state=%+v err=%v", resetState, err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", providerID, modelName); !errors.Is(err, ErrProviderQualityCircuitOpen) {
		t.Fatalf("half-open state admitted ordinary traffic: %v", err)
	}
	publicModels, err := s.ListPublicModels(ctx, "CN", "USD")
	if err != nil {
		t.Fatal(err)
	}
	for _, publicModel := range publicModels {
		if publicModel.ID == modelID && (publicModel.Availability.Available || publicModel.Availability.ReasonCode != "PROVIDER_QUALITY_UNAVAILABLE") {
			t.Fatalf("public model ignored half-open quality state: %+v", publicModel.Availability)
		}
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM provider_sla_events WHERE provider_id=$1 AND status='OPEN' AND metric='CIRCUIT_BREAKER'`, providerID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("circuit SLA event count=%d err=%v", count, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE provider_quality_observations SET error_class='tampered' WHERE idempotency_key=$1`, observation.IdempotencyKey); err == nil {
		t.Fatal("append-only quality observation accepted an update")
	}
}

func TestProviderPriceVerificationExactIdempotencyIntegration(t *testing.T) {
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
	adminID, providerID, modelID := id.UUID(), id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Price Quality Admin','ADMIN','ACTIVE',now())`, adminID, "quality-price-admin-"+adminID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO providers(id,name,slug,provider_type,base_url,enabled,config)
		VALUES($1,'Quality Price Provider',$2,'openai','https://api.openai.com/v1',false,'{}')`, providerID, "quality-price-"+providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,enabled)
		VALUES($1,$2,$3,$3,true)`, modelID, providerID, "quality-price-model-"+modelID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO provider_cost_price_book(provider_id,model_id,input_token_cost,
		cached_input_token_cost,output_token_cost,request_fixed_cost,currency,unit,effective_at,source,approval_status)
		VALUES($1,$2,1.250000000000,0.500000000000,2.750000000000,0.010000000000,'USD',1000000,now()-interval '1 minute','OFFICIAL_DOCUMENT','APPROVED')`, providerID, modelID); err != nil {
		t.Fatal(err)
	}
	verification := domain.ProviderPriceVerification{IdempotencyKey: "price-quality-" + id.UUID(), ProviderID: providerID,
		ModelID: modelID, SourceType: "OFFICIAL_DOCUMENT", SourceReference: "https://example.invalid/official-pricing",
		EvidenceSHA256:         "0000000000000000000000000000000000000000000000000000000000000000",
		ObservedInputTokenCost: "1.25", ObservedCachedInputTokenCost: "0.5", ObservedOutputTokenCost: "2.75",
		ObservedRequestFixedCost: "0.01", Currency: "USD", Unit: 1000000, ObservedAt: time.Now().UTC()}
	created, inserted, err := s.CreateProviderPriceVerification(ctx, verification, adminID)
	if err != nil || !inserted || created.Result != "MATCH" || created.MaximumDeviationBPS == nil || *created.MaximumDeviationBPS != 0 {
		t.Fatalf("unexpected verification: %+v inserted=%v err=%v", created, inserted, err)
	}
	replayed, inserted, err := s.CreateProviderPriceVerification(ctx, verification, adminID)
	if err != nil || inserted || replayed.ID != created.ID {
		t.Fatalf("idempotent replay failed: %+v inserted=%v err=%v", replayed, inserted, err)
	}
	verification.ObservedOutputTokenCost = "2.76"
	if _, _, err = s.CreateProviderPriceVerification(ctx, verification, adminID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed replay did not conflict: %v", err)
	}
}

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }
func decimalTestPointer(value string) *domain.Decimal {
	decimal := domain.Decimal(value)
	return &decimal
}
