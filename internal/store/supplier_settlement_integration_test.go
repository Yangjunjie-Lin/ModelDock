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

func TestSupplierSettlementPlatformUsageConcurrencyAndPayoutIntegration(t *testing.T) {
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
	adminOne, adminTwo := id.UUID(), id.UUID()
	for index, admin := range []string{adminOne, adminTwo} {
		if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at) VALUES($1,$2,'synthetic-hash',$3,'ADMIN','ACTIVE',now())`, admin, "settlement-admin-"+admin+"@example.invalid", "Settlement Admin "+string(rune('A'+index))); err != nil {
			t.Fatal(err)
		}
	}
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,contract_status='ACTIVE',allowed_regions='["US"]',pricing_disabled=false,emergency_kill_switch=false WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE provider_quality_policies SET enabled=true WHERE provider_id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	modelID, priceVersionID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,enabled,allowed_regions) VALUES($1,$2,$3,$3,true,'["*"]')`, modelID, providerID, "settlement-model-"+modelID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_price_version(id,provider_id,model_id,organization_id,version,provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,provider_request_fixed_cost,retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,retail_request_fixed_price,provider_currency,retail_currency,provider_unit,retail_unit,effective_at,source,approval_status) VALUES($1,$2,$3,$4,nextval('model_price_version_sequence'),0,0,0,10,0,0,0,12,'USD','USD',1,1,now()-interval '1 day','settlement-integration','APPROVED')`, priceVersionID, providerID, modelID, domain.LegacyOrganizationID); err != nil {
		t.Fatal(err)
	}

	supplierOrganizationID, supplierID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,status,billing_region,metadata) VALUES($1,'Settlement Supplier',$2,'ACTIVE','US','{}')`, supplierOrganizationID, "settlement-supplier-"+supplierID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('relaydock.supplier_admin_action','true',true)`); err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO supplier_organizations(id,organization_id,owner_user_id,legal_name,display_name,registration_number,incorporation_country,kyb_status,contract_status,status,payout_account_encrypted,payout_account_last4,payout_currency) VALUES($1,$2,$3,'Synthetic Settlement Supplier','Synthetic Settlement Supplier',$4,'US','VERIFIED','ACTIVE','APPROVED',$5,'0000','USD')`, supplierID, supplierOrganizationID, adminOne, "SYNTHETIC-"+supplierID, []byte("synthetic-encrypted-envelope"))
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkSupplierProvider(ctx, providerID, supplierID, "synthetic settlement link", adminOne); err != nil {
		t.Fatal(err)
	}
	next := time.Now().UTC().Add(time.Hour)
	policy := domain.SupplierSettlementPolicy{SupplierID: supplierID, Enabled: true, SettlementCycle: "DAILY", MinimumPayout: domain.Decimal("1"), CommissionBPS: 1000, RiskReserveBPS: 1000, ReserveHoldDays: 30, PayoutAdapter: "sandbox", PayoutRegion: "US", TaxVerificationRequired: true, InvoiceRequired: true, NextSettlementAt: &next}
	if _, err = s.UpdateSupplierSettlementPolicy(ctx, policy, adminOne); err != nil {
		t.Fatal(err)
	}

	// Docker Desktop's VM clock can be slightly ahead of the Windows host.
	// Keep the synthetic settlement evidence deterministically after linkage.
	usageTime := time.Now().UTC().Add(time.Minute)
	operationID, requestID, snapshotID, usageID := id.UUID(), "supplier-settlement-"+id.UUID(), id.UUID(), id.UUID()
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, domain.LegacyOrganizationID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,request_fingerprint,pricing_version_id,status,currency,maximum_amount,settled_amount,estimated_input_tokens,max_output_tokens,actual_input_tokens,actual_cached_input_tokens,actual_output_tokens,usage_source,settled_at) VALUES($1,$2,$3,$4,$5,$5,$5,$6,'SETTLED','USD',12,12,0,0,10,0,10,'SYNTHETIC',$7)`, operationID, walletID, domain.LegacyOrganizationID, domain.LegacyProjectID, requestID, priceVersionID, usageTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,organization_id,project_id,provider_id,requested_model,resolved_model,endpoint,status_code,pricing_version_id,funding_operation_id,usage_source,created_at) VALUES($1,$2,$3,$4,$5,$6,$6,'/v1/responses',200,$7,$8,'PROVIDER_REPORTED',$9)`, id.UUID(), requestID, domain.LegacyOrganizationID, domain.LegacyProjectID, providerID, "settlement-model-"+modelID, priceVersionID, operationID, usageTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO usage_price_snapshot(id,request_id,pricing_version_id,provider_id,model_id,input_tokens,cached_input_tokens,output_tokens,provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,provider_request_fixed_cost,retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,retail_request_fixed_price,provider_unit,retail_unit,provider_cost_amount,provider_currency,customer_sale_amount,customer_currency,exchange_rate,platform_gross_margin,promotion_amount,pre_tax_amount,tax_rate,tax_amount,final_user_amount,pricing_rule_version,credential_owner,settled_at) VALUES($1,$2,$3,$4,$5,10,0,10,0,0,0,10,0,0,0,12,1,1,10,'USD',12,'USD',1,2,0,12,0,0,12,'settlement:1','PLATFORM',$6)`, snapshotID, requestID, priceVersionID, providerID, modelID, usageTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,provider_id,model,input_tokens,cached_input_tokens,output_tokens,amount,currency,status,usage_price_snapshot_id,provider_cost_amount,customer_sale_amount,final_user_amount,pricing_rule_version,funding_operation_id,created_at) VALUES($1,$2,$3,$4,$5,$6,10,0,10,12,'USD','CHARGED',$7,10,12,12,'settlement:1',$8,$9)`, usageID, requestID, domain.LegacyOrganizationID, domain.LegacyProjectID, providerID, "settlement-model-"+modelID, snapshotID, operationID, usageTime); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errCh := make(chan error, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() { defer wait.Done(); _, accrueErr := s.AccrueEligibleSupplierUsage(ctx, 10); errCh <- accrueErr }()
	}
	wait.Wait()
	close(errCh)
	for accrueErr := range errCh {
		if accrueErr != nil {
			t.Fatal(accrueErr)
		}
	}
	var eligible bool
	if err = s.pool.QueryRow(ctx, `SELECT policy.enabled AND usage.status='CHARGED' AND operation.status IN ('SETTLED','PARTIALLY_SETTLED')
		AND snapshot.credential_owner='PLATFORM' AND snapshot.settled_at>=link.linked_at AND supplier.status='APPROVED'
		AND supplier.kyb_status='VERIFIED' AND supplier.contract_status='ACTIVE' AND provider.enabled
		AND provider.contract_status='ACTIVE' AND NOT provider.pricing_disabled AND NOT provider.emergency_kill_switch
		FROM billing_usage_records usage JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
		JOIN funding_operation operation ON operation.id=usage.funding_operation_id JOIN supplier_provider_links link ON link.provider_id=usage.provider_id
		JOIN supplier_organizations supplier ON supplier.id=link.supplier_id JOIN supplier_settlement_policy policy ON policy.supplier_id=supplier.id
		JOIN providers provider ON provider.id=usage.provider_id WHERE usage.id=$1`, usageID).Scan(&eligible); err != nil {
		t.Fatal(err)
	}
	if !eligible {
		var values []string
		rows, debugErr := s.pool.Query(ctx, `SELECT policy.enabled::text,usage.status,operation.status,snapshot.credential_owner,
			(snapshot.settled_at>=link.linked_at)::text,supplier.status,supplier.kyb_status,supplier.contract_status,
			provider.enabled::text,provider.contract_status,provider.pricing_disabled::text,provider.emergency_kill_switch::text
			FROM billing_usage_records usage JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
			JOIN funding_operation operation ON operation.id=usage.funding_operation_id JOIN supplier_provider_links link ON link.provider_id=usage.provider_id
			JOIN supplier_organizations supplier ON supplier.id=link.supplier_id JOIN supplier_settlement_policy policy ON policy.supplier_id=supplier.id
			JOIN providers provider ON provider.id=usage.provider_id WHERE usage.id=$1`, usageID)
		if debugErr == nil && rows.Next() {
			var fields [12]string
			_ = rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3], &fields[4], &fields[5], &fields[6], &fields[7], &fields[8], &fields[9], &fields[10], &fields[11])
			values = fields[:]
		}
		if rows != nil {
			rows.Close()
		}
		t.Fatalf("synthetic usage did not satisfy supplier accrual eligibility: %v", values)
	}
	var accrualID, accrualCount, payable, commission, reserve string
	if err = s.pool.QueryRow(ctx, `SELECT id::text,(SELECT count(*)::text FROM supplier_payable_accrual WHERE billing_usage_record_id=$1),initial_payable_amount::text,commission_amount::text,reserve_amount::text FROM supplier_payable_accrual WHERE billing_usage_record_id=$1`, usageID).Scan(&accrualID, &accrualCount, &payable, &commission, &reserve); err != nil {
		t.Fatal(err)
	}
	if accrualCount != "1" || payable != "8.100000000000" || commission != "1.000000000000" || reserve != "0.900000000000" {
		t.Fatalf("accrual count=%s payable=%s commission=%s reserve=%s", accrualCount, payable, commission, reserve)
	}
	type refundResult struct {
		entry    domain.SupplierPayableEntry
		replayed bool
		err      error
	}
	refundResults := make(chan refundResult, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			entry, replayed, refundErr := s.CreateSupplierRefundShare(ctx, accrualID, "1.000000000000", "synthetic customer refund allocation", "refund-share-"+requestID, adminTwo)
			refundResults <- refundResult{entry: entry, replayed: replayed, err: refundErr}
		}()
	}
	wait.Wait()
	close(refundResults)
	newRefunds := 0
	for result := range refundResults {
		if result.err != nil || result.entry.EntryType != "REFUND_SHARE" {
			t.Fatalf("refund result=%+v", result)
		}
		if !result.replayed {
			newRefunds++
		}
	}
	if newRefunds != 1 {
		t.Fatalf("new concurrent refund shares=%d", newRefunds)
	}

	supplierBill, replayed, err := s.ImportSupplierBill(ctx, SupplierBillInput{SupplierID: supplierID, ProviderID: providerID,
		BillReference: "supplier-declaration-" + requestID, PeriodStart: usageTime.AddDate(0, 0, -1), PeriodEnd: usageTime.AddDate(0, 0, 1),
		Currency: "USD", TotalAmount: "10", SourceSHA256: strings.Repeat("b", 64), DeclaredBy: adminOne,
		Lines: []SupplierBillLineInput{{ExternalLineID: "supplier-declared-line", RequestID: requestID, UsageDate: usageTime,
			Amount: "10.000000000000", Currency: "USD", Metadata: map[string]any{"synthetic": true}}}})
	if err != nil || replayed {
		t.Fatalf("supplier bill replayed=%v err=%v", replayed, err)
	}
	batch, replayed, err := s.CreateSupplierSettlementBatch(ctx, supplierID, providerID, usageTime.AddDate(0, 0, -1), usageTime.AddDate(0, 0, 1), "settlement-batch-"+requestID, &adminOne)
	if err != nil || replayed || batch.PayoutAmount.String() != "7.100000000000" {
		t.Fatalf("batch=%+v replayed=%v err=%v", batch, replayed, err)
	}
	if _, err = s.UpdateSupplierSettlementCompliance(ctx, batch.ID, "VERIFIED", "APPROVED", "synthetic verified evidence", adminTwo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveSupplierSettlement(ctx, batch.ID, supplierBill.ID, "supplier declaration must not authorize payout", adminTwo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("supplier declaration unexpectedly authorized payout: %v", err)
	}
	line := ProviderStatementLineInput{ExternalLineID: "settlement-line", RequestID: requestID, UsageDate: usageTime, Amount: "10.000000000000", Currency: "USD", Metadata: map[string]any{"synthetic": true}}
	statementID, replayed, err := s.ImportProviderStatement(ctx, ProviderStatementInput{ProviderID: providerID, StatementReference: "settlement-statement-" + requestID, PeriodStart: usageTime.AddDate(0, 0, -1), PeriodEnd: usageTime.AddDate(0, 0, 1), Region: "US", Currency: "USD", TotalAmount: "10", SourceSHA256: strings.Repeat("a", 64), ImportedBy: adminOne, Lines: []ProviderStatementLineInput{line}})
	if err != nil || replayed {
		t.Fatalf("statement replayed=%v err=%v", replayed, err)
	}
	appeal, replayed, err := s.CreateSupplierAppeal(ctx, domain.SupplierAppeal{ID: "usage-appeal-" + requestID,
		AppealNumber: "pending", SupplierID: supplierID, AppealType: "USAGE", AccrualID: &accrualID,
		Reason: "synthetic usage dispute", Evidence: map[string]any{"synthetic": true}}, adminOne)
	if err != nil || replayed {
		t.Fatalf("appeal=%+v replayed=%v err=%v", appeal, replayed, err)
	}
	if _, err = s.ApproveSupplierSettlement(ctx, batch.ID, statementID, "blocked while disputed", adminTwo); !errors.Is(err, ErrSupplierPayoutBlocked) {
		t.Fatalf("open usage dispute did not block payout approval: %v", err)
	}
	if _, err = s.ResolveSupplierAppeal(ctx, appeal.ID, "REJECTED", "platform evidence verified", adminTwo); err != nil {
		t.Fatal(err)
	}
	approved, err := s.ApproveSupplierSettlement(ctx, batch.ID, statementID, "synthetic four-eyes approval", adminTwo)
	if err != nil || approved.Status != "APPROVED" {
		t.Fatalf("approved=%+v err=%v", approved, err)
	}
	job, err := s.ClaimSupplierPayout(ctx, time.Now().UTC())
	if err != nil || job.Batch.ID != batch.ID {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if err = s.FailSupplierPayout(ctx, batch.ID, job.AttemptID, "synthetic_processor_timeout", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = s.FailSupplierPayout(ctx, batch.ID, job.AttemptID, "synthetic_processor_timeout", time.Now().UTC()); err != nil {
		t.Fatalf("payout failure replay was not idempotent: %v", err)
	}
	if _, err = s.RetrySupplierSettlement(ctx, batch.ID, "synthetic retry approval", adminTwo); err != nil {
		t.Fatal(err)
	}
	job, err = s.ClaimSupplierPayout(ctx, time.Now().UTC())
	if err != nil || job.AttemptNo != 2 {
		t.Fatalf("retry job=%+v err=%v", job, err)
	}
	type payoutResult struct {
		batch    domain.SupplierSettlementBatch
		replayed bool
		err      error
	}
	payoutResults := make(chan payoutResult, 8)
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			paid, wasReplay, payoutErr := s.CompleteSupplierPayout(ctx, batch.ID, job.AttemptID, "synthetic-payout-reference", map[string]any{"synthetic": true}, time.Now().UTC())
			payoutResults <- payoutResult{batch: paid, replayed: wasReplay, err: payoutErr}
		}()
	}
	wait.Wait()
	close(payoutResults)
	newPayouts := 0
	for result := range payoutResults {
		if result.err != nil || result.batch.Status != "PAID" {
			t.Fatalf("payout result=%+v", result)
		}
		if !result.replayed {
			newPayouts++
		}
	}
	if newPayouts != 1 {
		t.Fatalf("new concurrent payouts=%d", newPayouts)
	}
	if _, _, err = s.CompleteSupplierPayout(ctx, batch.ID, job.AttemptID, "different-reference", nil, time.Now().UTC()); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed payout replay was not rejected: %v", err)
	}
	var journals, payoutEntries, openAppeals int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ledger_journal WHERE supplier_settlement_batch_id=$1 AND status='POSTED'),(SELECT count(*) FROM supplier_payable_entry WHERE settlement_batch_id=$1 AND entry_type='PAYOUT'),(SELECT count(*) FROM supplier_appeal WHERE accrual_id=$2 AND status IN ('OPEN','UNDER_REVIEW'))`, batch.ID, accrualID).Scan(&journals, &payoutEntries, &openAppeals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 || payoutEntries != 1 || openAppeals != 0 {
		t.Fatalf("journals=%d payout_entries=%d appeals=%d", journals, payoutEntries, openAppeals)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE supplier_payable_entry SET amount=amount+1 WHERE accrual_id=$1 AND entry_type='USAGE_ACCRUAL'`, accrualID); err == nil {
		t.Fatal("append-only supplier payable entry accepted mutation")
	}
	reconciliation, _, err := s.RunFinancialReconciliation(ctx, usageTime, "TEST", &adminTwo)
	if err != nil {
		t.Fatal(err)
	}
	var supplierMatches int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM financial_reconciliation_observation WHERE run_id=$1
		AND check_type IN ('SUPPLIER_PAYABLE_TO_USAGE','SUPPLIER_BILL_TO_PAYABLE','SUPPLIER_PAYOUT_TO_LEDGER') AND result='MATCHED'`, reconciliation.ID).Scan(&supplierMatches); err != nil {
		t.Fatal(err)
	}
	if supplierMatches != 3 {
		t.Fatalf("supplier reconciliation matches=%d", supplierMatches)
	}
}
