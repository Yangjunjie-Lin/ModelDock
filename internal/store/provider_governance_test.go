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

func TestProviderGovernanceIntegration(t *testing.T) {
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
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	modelID := id.UUID()
	modelName := "governance-" + modelID
	if _, err = s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,enabled,allowed_regions) VALUES($1,$2,$3,$3,true,'["US"]')`, modelID, providerID, modelName); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE organizations SET billing_region='US',required_data_regions='["US"]' WHERE id=$1`, domain.LegacyOrganizationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,pricing_disabled=false,commercial_status='CONTRACT_PENDING',commercial_resale_status='APPROVED',allowed_customer_regions='["US"]',prohibited_regions='[]',data_processing_regions='["US"]',emergency_kill_switch=false WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", providerID, modelName); !errors.Is(err, ErrProviderCommercialUnavailable) {
		t.Fatalf("pending admission error=%v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET commercial_status='COMMERCIAL_APPROVED',contract_start_at=now()-interval '1 hour',contract_end_at=now()+interval '1 day' WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	priceEffectiveAt := time.Now().UTC().Add(-time.Minute)
	if _, err = s.CreateProviderCostPriceBook(ctx, domain.ProviderCostPriceBook{ProviderID: providerID, ModelID: modelID,
		InputTokenCost: "1", CachedInputTokenCost: "0.5", OutputTokenCost: "2", RequestFixedCost: "0", Currency: "USD",
		Unit: 1_000_000, EffectiveAt: priceEffectiveAt, Source: "governance-admission", ApprovalStatus: "APPROVED"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateCustomerRetailPriceBook(ctx, domain.CustomerRetailPriceBook{ProviderID: providerID, ModelID: modelID,
		InputTokenPrice: "2", CachedInputTokenPrice: "1", OutputTokenPrice: "4", RequestFixedPrice: "0", Currency: "USD",
		Unit: 1_000_000, EffectiveAt: priceEffectiveAt, Source: "governance-admission"}, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", providerID, modelName); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProviderKillSwitch(ctx, providerID, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckProviderAdmission(ctx, domain.LegacyOrganizationID, "", providerID, modelName); !errors.Is(err, ErrProviderCommercialUnavailable) {
		t.Fatalf("kill switch admission error=%v", err)
	}
	if _, err = s.SetProviderKillSwitch(ctx, providerID, false, nil); err != nil {
		t.Fatal(err)
	}

	change := domain.ProviderCostChangeRequest{IdempotencyKey: "governance-change-" + modelID, ProviderID: providerID, ModelID: modelID, SourceType: "CSV", SourceReference: "sha256:test-fixture", InputTokenCost: "1", CachedInputTokenCost: "0.5", OutputTokenCost: "2", RequestFixedCost: "0", Currency: "USD", Unit: 1000000, EffectiveAt: time.Now().UTC().Add(time.Hour)}
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, _, createErr := s.CreateProviderCostChange(ctx, change, nil)
			results <- createErr
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result != nil {
			t.Fatal(result)
		}
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM provider_cost_change_requests WHERE idempotency_key=$1`, change.IdempotencyKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("idempotent change count=%d err=%v", count, err)
	}
	requester := id.UUID()
	reviewer := id.UUID()
	for _, actor := range []string{requester, reviewer} {
		if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status) VALUES($1,$2,'integration-hash','Governance Admin','ADMIN','ACTIVE')`, actor, actor+"@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	change.IdempotencyKey += "-review"
	created, _, err := s.CreateProviderCostChange(ctx, change, &requester)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReviewProviderCostChange(ctx, created.ID, "APPROVE", "self review", &requester); !errors.Is(err, ErrPriceChangeSelfReview) {
		t.Fatalf("self review error=%v", err)
	}
	reviewed, err := s.ReviewProviderCostChange(ctx, created.ID, "APPROVE", "contract test", &reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.PublishedPriceBookID == nil {
		t.Fatal("approved change did not publish a price")
	}
	if _, err = s.ReviewProviderCostChange(ctx, created.ID, "APPROVE", "race", nil); !errors.Is(err, ErrPriceChangeState) {
		t.Fatalf("second approval error=%v", err)
	}
}

func TestProviderBudgetMonthUsesCalendarMonthStart(t *testing.T) {
	if got := providerBudgetMonth(time.Date(2026, time.August, 31, 23, 59, 0, 0, time.FixedZone("west", -7*60*60))); got != "2026-09-01" {
		t.Fatalf("budget month=%s", got)
	}
}

func TestProviderBudgetAndKillSwitchConcurrency(t *testing.T) {
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
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,pricing_disabled=false,commercial_status='COMMERCIAL_APPROVED',
		commercial_resale_status='APPROVED',allowed_customer_regions='["*"]',data_processing_regions='["*"]',cost_limit=0.15,
		settlement_currency='USD',emergency_kill_switch=false WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	_, _, pricingVersionID := fundingFixture(t, ctx, s)
	credentialID, groupID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO provider_credentials(id,provider_id,name,encrypted_secret,secret_last4,status,credential_owner)
		VALUES($1,$2,'budget-fixture',decode('00','hex'),'0000','ACTIVE','PLATFORM')`, credentialID, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO credential_groups(id,provider_id,name) VALUES($1,$2,$3)`, groupID, providerID, "budget-"+groupID); err != nil {
		t.Fatal(err)
	}
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, domain.LegacyOrganizationID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	operations := make([]domain.FundingOperation, 2)
	for index := range operations {
		operations[index].ID = id.UUID()
		_, err = s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,
			request_fingerprint,pricing_version_id,status,currency,maximum_amount,estimated_input_tokens,max_output_tokens)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,'RESERVED','USD',1,100000,50000)`, operations[index].ID, walletID,
			domain.LegacyOrganizationID, domain.LegacyProjectID, fmt.Sprintf("budget-request-%d-%s", index, groupID),
			fmt.Sprintf("budget-key-%d-%s", index, groupID), fmt.Sprintf("budget-fingerprint-%d-%s", index, groupID), pricingVersionID)
		if err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, operation := range operations {
		wg.Add(1)
		go func(operationID string) {
			defer wg.Done()
			_, attemptErr := s.BeginFundingAttempt(ctx, operationID, providerID, credentialID, groupID, false)
			results <- attemptErr
		}(operation.ID)
	}
	wg.Wait()
	close(results)
	success, budgetDenied := 0, 0
	for result := range results {
		switch {
		case result == nil:
			success++
		case errors.Is(result, ErrProviderBudgetExceeded):
			budgetDenied++
		default:
			t.Fatal(result)
		}
	}
	if success != 1 || budgetDenied != 1 {
		t.Fatalf("budget concurrency success=%d denied=%d", success, budgetDenied)
	}
	var reserved string
	if err = s.pool.QueryRow(ctx, `SELECT reserved_cost::text FROM provider_usage_budget WHERE provider_id=$1 AND period_month=date_trunc('month',now())::date`, providerID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != "0.100000000000" {
		t.Fatalf("reserved cost=%s", reserved)
	}
	var reservedOperationID string
	if err = s.pool.QueryRow(ctx, `SELECT operation_id FROM provider_budget_reservations WHERE provider_id=$1 AND status='RESERVED'`, providerID).Scan(&reservedOperationID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginFundingAttempt(ctx, reservedOperationID, providerID, credentialID, groupID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT reserved_cost::text FROM provider_usage_budget WHERE provider_id=$1 AND period_month=date_trunc('month',now())::date`, providerID).Scan(&reserved); err != nil || reserved != "0.100000000000" {
		t.Fatalf("idempotent operation reservation=%s err=%v", reserved, err)
	}
	if err = s.SettleProviderBudgetReservation(ctx, reservedOperationID, "0.1"); err != nil {
		t.Fatal(err)
	}
	var providerCost, reservationStatus string
	if err = s.pool.QueryRow(ctx, `SELECT b.reserved_cost::text,b.provider_cost::text,r.status FROM provider_usage_budget b
		JOIN provider_budget_reservations r ON r.provider_id=b.provider_id AND r.period_month=b.period_month
		WHERE r.operation_id=$1`, reservedOperationID).Scan(&reserved, &providerCost, &reservationStatus); err != nil {
		t.Fatal(err)
	}
	if reserved != "0.000000000000" || providerCost != "0.100000000000" || reservationStatus != "SETTLED" {
		t.Fatalf("budget settlement reserved=%s cost=%s status=%s", reserved, providerCost, reservationStatus)
	}
	// A NULL cost_limit is the documented "no configured Provider budget"
	// state. It must not fail request admission by scanning SQL NULL into a Go
	// string, and zero remains the internal exact-decimal sentinel for no cap.
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET cost_limit=NULL WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	unlimitedOperationID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,
		request_fingerprint,pricing_version_id,status,currency,maximum_amount,estimated_input_tokens,max_output_tokens)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'RESERVED','USD',1,100000,50000)`, unlimitedOperationID, walletID,
		domain.LegacyOrganizationID, domain.LegacyProjectID, "unlimited-request-"+groupID,
		"unlimited-key-"+groupID, "unlimited-fingerprint-"+groupID, pricingVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginFundingAttempt(ctx, unlimitedOperationID, providerID, credentialID, groupID, false); err != nil {
		t.Fatalf("NULL provider cost limit rejected a funding attempt: %v", err)
	}
	if _, err = s.SetProviderKillSwitch(ctx, providerID, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginFundingAttempt(ctx, operations[1].ID, providerID, credentialID, groupID, false); !errors.Is(err, ErrProviderCommercialUnavailable) {
		t.Fatalf("post-kill-switch attempt error=%v", err)
	}
}
