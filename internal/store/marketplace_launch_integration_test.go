package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func TestMarketplaceLaunchAcceptanceAndLifecycleIntegration(t *testing.T) {
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
	now := time.Now().UTC()
	adminOne, adminTwo := id.UUID(), id.UUID()
	for index, adminID := range []string{adminOne, adminTwo} {
		_, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
			VALUES($1,$2,'synthetic-hash',$3,'ADMIN','ACTIVE',now())`, adminID,
			"marketplace-admin-"+adminID+"@example.invalid", "Marketplace Admin "+string(rune('A'+index)))
		if err != nil {
			t.Fatal(err)
		}
	}
	provider, err := s.CreateProvider(ctx, domain.Provider{Name: "Synthetic Marketplace Provider", Slug: "marketplace-" + id.UUID(),
		ProviderType: "openai", BaseURL: "https://provider.example.invalid/v1", Enabled: true, ContractStatus: "ACTIVE",
		CommercialStatus: domain.ProviderStatusCommercialApproved, ContractReviewedAt: &now, ContractStartAt: &now,
		CommercialResaleStatus: "APPROVED", CredentialOwner: domain.CredentialOwnerPlatform,
		AllowedRegions: []string{"*"}, AllowedCustomerRegions: []string{"*"}, DataProcessingRegions: []string{"*"},
		DataRetentionPolicy: "no-training-30-days", TermsVersion: "synthetic-marketplace-v1", SettlementCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	modelID, modelName := id.UUID(), "synthetic-marketplace-model-"+id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,enabled,allowed_regions)
		VALUES($1,$2,$3,$3,true,'["*"]')`, modelID, provider.ID, modelName); err != nil {
		t.Fatal(err)
	}
	priceBookID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO provider_cost_price_book(id,provider_id,model_id,input_token_cost,cached_input_token_cost,
		output_token_cost,request_fixed_cost,currency,unit,effective_at,source,approval_status)
		VALUES($1,$2,$3,1,0,2,0,'USD',1000000,now()-interval '1 day','synthetic-official','APPROVED')`, priceBookID, provider.ID, modelID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO customer_retail_price_book(id,provider_id,model_id,input_token_price,
		cached_input_token_price,output_token_price,request_fixed_price,currency,unit,effective_at,source,approval_status)
		VALUES($1,$2,$3,2,0,4,0,'USD',1000000,now()-interval '1 day','synthetic-platform','APPROVED')`, id.UUID(), provider.ID, modelID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO provider_price_verifications(id,idempotency_key,request_fingerprint,provider_id,model_id,
		price_book_id,source_type,source_reference,evidence_sha256,observed_input_token_cost,observed_cached_input_token_cost,
		observed_output_token_cost,observed_request_fixed_cost,currency,unit,result,observed_at,created_by)
		VALUES($1,$2,$3,$4,$5,$6,'OFFICIAL_DOCUMENT',$7,$8,1,0,2,0,'USD',1000000,'MATCH',now(),$9)`,
		id.UUID(), "marketplace-price-"+provider.ID, strings.Repeat("a", 64), provider.ID, modelID, priceBookID,
		"synthetic-official-price-sheet", strings.Repeat("b", 64), adminOne); err != nil {
		t.Fatal(err)
	}

	supplierOrganizationID, supplierID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,billing_region,metadata)
		VALUES($1,'Synthetic Marketplace Supplier',$2,'ACTIVE','US','{}')`, supplierOrganizationID, "marketplace-supplier-"+supplierID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO supplier_organizations(id,organization_id,owner_user_id,legal_name,display_name,
			registration_number,incorporation_country,kyb_status,contract_status,contract_version,contract_start_at,status,
			payout_account_encrypted,payout_account_last4,payout_currency,tax_id,tax_country,tax_residency,tax_form_type)
			VALUES($1,$2,$3,'Synthetic Marketplace Supplier','Synthetic Marketplace Supplier',$4,'US','VERIFIED','ACTIVE',
			'marketplace-v1',now()-interval '1 day','APPROVED',$5,'0000','USD','SYNTHETIC-TAX','US','US','W8-SYNTHETIC')`,
			supplierID, supplierOrganizationID, adminOne, "SYNTHETIC-"+supplierID, []byte("synthetic-encrypted-payout-envelope"))
	}
	if err == nil {
		err = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	endpointID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_endpoints(id,supplier_id,endpoint_url,challenge_hash,verification_status,
		isolation_status,verified_at) VALUES($1,$2,$3,$4,'VERIFIED','PASSED',now())`, endpointID, supplierID,
		provider.BaseURL, []byte("synthetic-challenge-hash")); err != nil {
		t.Fatal(err)
	}
	modelApplicationID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_model_applications(id,supplier_id,endpoint_id,model_name,status)
		VALUES($1,$2,$3,$4,'APPROVED')`, modelApplicationID, supplierID, endpointID, modelName); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_price_applications(id,supplier_id,model_application_id,input_token_price,
		output_token_price,currency,unit,status) VALUES($1,$2,$3,1,2,'USD',1000000,'APPROVED')`, id.UUID(), supplierID, modelApplicationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_security_questionnaires(id,supplier_id,version,answers,status,submitted_at,
		reviewed_at,reviewed_by) VALUES($1,$2,'synthetic-security-v1','{"encryption":true}','APPROVED',now(),now(),$3)`, id.UUID(), supplierID, adminOne); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE provider_quality_policies SET enabled=true,minimum_samples=1,ramp_enabled=true,
		ramp_initial_bps=500,required_test_regions='["US"]' WHERE provider_id=$1`, provider.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkSupplierProvider(ctx, provider.ID, supplierID, "synthetic Marketplace qualification complete", adminOne); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE provider_quality_states SET grade='B',quality_score=92,routing_multiplier=0.8,
		traffic_cap_bps=500,circuit_state='CLOSED',availability_pct=99.9,error_rate_pct=0.1,rate_limited_pct=0,
		measurement_count=25,last_evaluated_at=now(),last_probe_at=now() WHERE provider_id=$1`, provider.ID); err != nil {
		t.Fatal(err)
	}
	listing, err := s.UpsertMarketplaceListing(ctx, domain.MarketplaceListing{ProviderID: provider.ID, Endpoint: provider.BaseURL,
		SupportedModels: []string{modelName}, Price: map[string]any{"declared": "1.000000000000"}, Status: "DRAFT", Uptime: 100, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	listing.Status = "ACTIVE"
	if _, err = s.UpsertMarketplaceListing(ctx, listing); err == nil {
		t.Fatal("supplier declaration activated a listing without platform acceptance")
	}

	type reviewResult struct {
		review   domain.MarketplaceLaunchReview
		replayed bool
		err      error
	}
	results := make(chan reviewResult, 8)
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			review, replayed, createErr := s.CreateMarketplaceLaunchReview(ctx, listing.ID, "marketplace-launch-idempotent-"+listing.ID,
				marketplacePolicyVersion, adminOne)
			results <- reviewResult{review, replayed, createErr}
		}()
	}
	wait.Wait()
	close(results)
	newReviews := 0
	var review domain.MarketplaceLaunchReview
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		review = result.review
		if !result.replayed {
			newReviews++
		}
	}
	if newReviews != 1 {
		t.Fatalf("concurrent launch review creations=%d", newReviews)
	}

	productionBatchID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_settlement_batch(id,batch_number,idempotency_key,supplier_id,provider_id,
		period_start,period_end,currency,payout_amount,payout_adapter,payout_region,payout_idempotency_key)
		VALUES($1,$2,$3,$4,$5,current_date-20,current_date-15,'USD',1,'production-wire','US',$6)`, productionBatchID,
		"production-block-"+productionBatchID, "production-block-"+productionBatchID, supplierID, provider.ID, "production-payout-"+productionBatchID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE supplier_settlement_batch SET status='APPROVED',approved_by=$2,approved_at=now() WHERE id=$1`, productionBatchID, adminTwo); err == nil {
		t.Fatal("production payout was approved before readiness review")
	}

	readiness := domain.SupplierPayoutReadinessReview{SupplierID: supplierID, ContractStatus: "APPROVED",
		ContractEvidenceReference: "contract-review-synthetic", TaxStatus: "APPROVED", TaxEvidenceReference: "tax-review-synthetic",
		PaymentStatus: "APPROVED", PaymentEvidenceReference: "payment-review-synthetic", SecurityStatus: "APPROVED",
		SecurityEvidenceReference: "security-review-synthetic", ReviewReason: "synthetic four-part independent review"}
	readinessResults := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, updateErr := s.UpdateSupplierPayoutReadiness(ctx, readiness, 0, adminTwo)
			readinessResults <- updateErr
		}()
	}
	wait.Wait()
	close(readinessResults)
	readinessUpdates := 0
	for updateErr := range readinessResults {
		if updateErr == nil {
			readinessUpdates++
		} else if !errors.Is(updateErr, ErrIdempotencyConflict) {
			t.Fatal(updateErr)
		}
	}
	if readinessUpdates != 1 {
		t.Fatalf("concurrent payout readiness updates=%d", readinessUpdates)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE supplier_settlement_batch SET status='APPROVED',approved_by=$2,approved_at=now() WHERE id=$1`, productionBatchID, adminTwo); err != nil {
		t.Fatal(err)
	}

	review, err = s.EvaluateMarketplaceLaunchReview(ctx, review.ID, adminOne)
	if err != nil {
		t.Fatal(err)
	}
	gateStatus := map[string]string{}
	for _, gate := range review.Gates {
		gateStatus[gate.GateCode] = gate.Status
	}
	for _, gateCode := range []string{"SUPPLIER_REGISTRATION", "QUALIFICATION_REVIEW", "ENDPOINT_VERIFICATION", "MODEL_PUBLICATION", "PRICE_APPROVAL", "HEALTH_TEST", "CONTRACT_REVIEW", "TAX_REVIEW", "PAYMENT_REVIEW", "SECURITY_REVIEW"} {
		if gateStatus[gateCode] != "PASSED" {
			t.Fatalf("foundation gate %s=%s", gateCode, gateStatus[gateCode])
		}
	}
	if gateStatus["USER_INVOCATION"] == "PASSED" || gateStatus["SUPPLIER_PAYABLE"] == "PASSED" {
		t.Fatal("supplier listing declarations were used as platform runtime or payable evidence")
	}
	if _, err = s.MarketplaceLifecycleAction(ctx, listing.ID, "CANARY_START", "synthetic controlled 500 bps canary", adminOne); err != nil {
		t.Fatal(err)
	}

	groupID, routeID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO credential_groups(id,provider_id,name) VALUES($1,$2,$3)`, groupID, provider.ID, "synthetic-marketplace-group-"+groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_routes(id,alias,provider_id,upstream_model,credential_group_id,enabled)
		VALUES($1,$2,$3,$4,$5,true)`, routeID, "marketplace-route-"+routeID, provider.ID, modelName, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO project_model_routes(id,project_id,model_route_id,alias,enabled)
		VALUES($1,$2,$3,$4,true)`, id.UUID(), domain.LegacyProjectID, routeID, "marketplace-project-route-"+routeID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", provider.ID, modelName); err != nil {
		t.Fatalf("canary Provider admission failed: %v", err)
	}

	priceVersionID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_price_version(id,provider_id,model_id,organization_id,version,
		provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,provider_request_fixed_cost,
		retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,retail_request_fixed_price,
		provider_currency,retail_currency,provider_unit,retail_unit,effective_at,source,approval_status)
		VALUES($1,$2,$3,$4,nextval('model_price_version_sequence'),0,0,0,10,0,0,0,12,'USD','USD',1,1,
		now()-interval '1 day','marketplace-integration','APPROVED')`, priceVersionID, provider.ID, modelID, domain.LegacyOrganizationID); err != nil {
		t.Fatal(err)
	}
	requestID, operationID, snapshotID, usageID := "marketplace-request-"+id.UUID(), id.UUID(), id.UUID(), id.UUID()
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, domain.LegacyOrganizationID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,
		request_fingerprint,pricing_version_id,status,currency,maximum_amount,settled_amount,estimated_input_tokens,max_output_tokens,
		actual_input_tokens,actual_cached_input_tokens,actual_output_tokens,usage_source,settled_at)
		VALUES($1,$2,$3,$4,$5,$5,$5,$6,'SETTLED','USD',12,12,0,0,10,0,10,'SYNTHETIC',now())`, operationID,
		walletID, domain.LegacyOrganizationID, domain.LegacyProjectID, requestID, priceVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,organization_id,project_id,provider_id,requested_model,
		resolved_model,endpoint,status_code,pricing_version_id,funding_operation_id,usage_source,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$6,'/v1/responses',200,$7,$8,'PROVIDER_REPORTED',now())`, id.UUID(), requestID,
		domain.LegacyOrganizationID, domain.LegacyProjectID, provider.ID, modelName, priceVersionID, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO usage_price_snapshot(id,request_id,pricing_version_id,provider_id,model_id,input_tokens,
		cached_input_tokens,output_tokens,provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,
		provider_request_fixed_cost,retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,
		retail_request_fixed_price,provider_unit,retail_unit,provider_cost_amount,provider_currency,customer_sale_amount,
		customer_currency,exchange_rate,platform_gross_margin,promotion_amount,pre_tax_amount,tax_rate,tax_amount,
		final_user_amount,pricing_rule_version,credential_owner,settled_at)
		VALUES($1,$2,$3,$4,$5,10,0,10,0,0,0,10,0,0,0,12,1,1,10,'USD',12,'USD',1,2,0,12,0,0,12,
		'marketplace:1','PLATFORM',now())`, snapshotID, requestID, priceVersionID, provider.ID, modelID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,provider_id,model,
		input_tokens,output_tokens,amount,currency,status,usage_price_snapshot_id,provider_cost_amount,customer_sale_amount,
		final_user_amount,pricing_rule_version,funding_operation_id,created_at)
		VALUES($1,$2,$3,$4,$5,$6,10,10,12,'USD','CHARGED',$7,10,12,12,'marketplace:1',$8,now())`, usageID,
		requestID, domain.LegacyOrganizationID, domain.LegacyProjectID, provider.ID, modelName, snapshotID, operationID); err != nil {
		t.Fatal(err)
	}
	accrualID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_payable_accrual(id,idempotency_key,supplier_id,provider_id,
		billing_usage_record_id,usage_price_snapshot_id,funding_operation_id,request_id,gross_amount,commission_bps,
		commission_amount,reserve_bps,reserve_amount,initial_payable_amount,currency,usage_settled_at,reserve_releasable_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,10,1000,1,1000,1,8,'USD',now(),now()+interval '30 days')`, accrualID,
		"marketplace-accrual-"+accrualID, supplierID, provider.ID, usageID, snapshotID, operationID, requestID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_payable_entry(id,idempotency_key,supplier_id,provider_id,accrual_id,
		entry_type,entry_side,amount,currency,available_at) VALUES($1,$2,$3,$4,$5,'USAGE_ACCRUAL','CREDIT',8,'USD',now()),
		($6,$7,$3,$4,$5,'REFUND_SHARE','DEBIT',1,'USD',now())`, id.UUID(), "marketplace-payable-"+accrualID,
		supplierID, provider.ID, accrualID, id.UUID(), "marketplace-refund-"+accrualID); err != nil {
		t.Fatal(err)
	}
	paidBatchID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_settlement_batch(id,batch_number,idempotency_key,supplier_id,provider_id,
		period_start,period_end,currency,gross_usage_amount,commission_amount,payout_amount,status,tax_status,invoice_status,
		payout_adapter,payout_region,payout_idempotency_key,approved_by,approved_at,paid_at)
		VALUES($1,$2,$3,$4,$5,current_date-10,current_date-5,'USD',10,1,8,'PAID','VERIFIED','APPROVED','sandbox','US',$6,$7,now(),now())`,
		paidBatchID, "marketplace-paid-"+paidBatchID, "marketplace-paid-"+paidBatchID, supplierID, provider.ID,
		"marketplace-paid-payout-"+paidBatchID, adminTwo); err != nil {
		t.Fatal(err)
	}
	billID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_bill(id,supplier_id,provider_id,bill_reference,period_start,period_end,currency,
		total_amount,source_sha256,import_fingerprint_sha256,status,declared_by)
		VALUES($1,$2,$3,$4,current_date-10,current_date-5,'USD',10,$5,$6,'RECONCILED',$7)`, billID, supplierID,
		provider.ID, "marketplace-bill-"+billID, strings.Repeat("c", 64), strings.Repeat("d", 64), adminOne); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO supplier_appeal(id,appeal_number,idempotency_key,supplier_id,appeal_type,
		supplier_bill_id,status,reason,evidence,resolution_reason,submitted_by,resolved_by,resolved_at)
		VALUES($1,$2,$3,$4,'BILL',$5,'REJECTED','synthetic dispute','{}','platform evidence confirmed',$6,$7,now())`,
		id.UUID(), "MARKETPLACE-APPEAL-"+id.UUID(), "marketplace-appeal-"+billID, supplierID, billID, adminOne, adminTwo); err != nil {
		t.Fatal(err)
	}
	review, err = s.EvaluateMarketplaceLaunchReview(ctx, review.ID, adminOne)
	if err != nil {
		t.Fatal(err)
	}
	for _, gate := range review.Gates {
		if gate.EvidenceSource == "PLATFORM_AUTOMATED" && gate.Status != "PASSED" {
			t.Fatalf("automatic gate %s=%s ref=%s", gate.GateCode, gate.Status, gate.EvidenceReference)
		}
	}
	for _, gateCode := range []string{"SUPPLIER_SUSPENSION_DRILL", "EMERGENCY_CUTOVER_DRILL", "SUPPLIER_EXIT_DRILL"} {
		review, err = s.AttestMarketplaceLaunchGate(ctx, review.ID, gateCode, "PASSED", "synthetic-drill-report-"+strings.ToLower(gateCode),
			"operator-reviewed synthetic lifecycle rehearsal", adminOne)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.ApproveMarketplaceLaunchReview(ctx, review.ID, "self approval must fail", adminOne); !errors.Is(err, ErrMarketplaceLaunchState) {
		t.Fatalf("self approval error=%v", err)
	}
	review, err = s.ApproveMarketplaceLaunchReview(ctx, review.ID, "second administrator verified all acceptance evidence", adminTwo)
	if err != nil || review.Status != "APPROVED" {
		t.Fatalf("approved review=%+v err=%v", review, err)
	}
	listing.Status = "ACTIVE"
	listing.Price = map[string]any{"declared": "2.000000000000"}
	if _, err = s.UpsertMarketplaceListing(ctx, listing); err == nil {
		t.Fatal("material listing change reused an approval bound to older content")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE marketplace_launch_gate_event SET evidence_reference='tampered' WHERE gate_id=(SELECT id FROM marketplace_launch_gate WHERE review_id=$1 LIMIT 1)`, review.ID); err == nil {
		t.Fatal("append-only launch evidence was mutable")
	}
	if _, err = s.MarketplaceLifecycleAction(ctx, listing.ID, "SUSPEND", "synthetic supplier suspension", adminTwo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", provider.ID, modelName); !errors.Is(err, ErrProviderCommercialUnavailable) {
		t.Fatalf("suspended admission error=%v", err)
	}
	if _, err = s.MarketplaceLifecycleAction(ctx, listing.ID, "RESUME", "synthetic reviewed resume", adminTwo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarketplaceLifecycleAction(ctx, listing.ID, "EMERGENCY_CUTOVER", "synthetic emergency cutover", adminTwo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", provider.ID, modelName); !errors.Is(err, ErrProviderCommercialUnavailable) {
		t.Fatalf("cutover admission error=%v", err)
	}
	if _, err = s.MarketplaceLifecycleAction(ctx, listing.ID, "RESUME", "synthetic post-incident resume", adminTwo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MarketplaceLifecycleAction(ctx, listing.ID, "EXIT", "synthetic supplier exit with no processing payout", adminTwo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", provider.ID, modelName); !errors.Is(err, ErrProviderCommercialUnavailable) {
		t.Fatalf("exited admission error=%v", err)
	}
	var finalListing, finalSupplier, finalLink, finalReview string
	if err = s.pool.QueryRow(ctx, `SELECT listing.status,supplier.status,link.status,review.status FROM provider_marketplace_listings listing
		JOIN supplier_provider_links link ON link.provider_id=listing.provider_id JOIN supplier_organizations supplier ON supplier.id=link.supplier_id
		JOIN marketplace_launch_review review ON review.listing_id=listing.id WHERE listing.id=$1`, listing.ID).
		Scan(&finalListing, &finalSupplier, &finalLink, &finalReview); err != nil {
		t.Fatal(err)
	}
	if finalListing != "EXITED" || finalSupplier != "EXITED" || finalLink != "ENDED" || finalReview != "REVOKED" {
		t.Fatalf("exit state=%s/%s/%s/%s", finalListing, finalSupplier, finalLink, finalReview)
	}
}
