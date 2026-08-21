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

func TestFundingLedgerIntegration(t *testing.T) {
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

	_, _, pricingVersionID := fundingFixture(t, ctx, s)
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, domain.LegacyOrganizationID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE wallets SET billing_mode='PREPAID',available_balance=0,reserved_balance=0,
		credit_limit=0,risk_limit=0.01,risk_exposure=0,status='ACTIVE',credit_enforced=true WHERE id=$1`, walletID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateWalletTransaction(ctx, domain.WalletTransaction{WalletID: walletID, TransactionType: "TOPUP", Amount: domain.Decimal("10"), IdempotencyKey: "funding-fixture-topup", Reference: "synthetic integration funding"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreatePromotionCredit(ctx, domain.PromotionCredit{OrganizationID: domain.LegacyOrganizationID,
		Currency: "USD", AmountGranted: "0.050000000000", IdempotencyKey: "funding-fixture-bonus", Source: "integration-test"}, nil); err != nil {
		t.Fatal(err)
	}
	promotionOperation, _, err := s.ReserveFunding(ctx, FundingReservationRequest{OrganizationID: domain.LegacyOrganizationID,
		ProjectID: domain.LegacyProjectID, RequestID: "promotion-reservation", IdempotencyKey: "promotion-reservation",
		RequestFingerprint: "promotion-reservation", PricingVersionID: pricingVersionID, Currency: "USD",
		MaximumAmount: "0.150000000000", PromotionAmount: "0.050000000000", EstimatedInput: 100_000, MaxOutput: 25_000})
	if err != nil {
		t.Fatal(err)
	}
	if promotionOperation.MaximumAmount.String() != "0.150000000000" || promotionOperation.PromotionAmount.String() != "0.050000000000" {
		t.Fatalf("promotion reservation=%+v", promotionOperation)
	}
	promotionOperation, err = s.SettleFunding(ctx, FundingSettlementRequest{OperationID: promotionOperation.ID,
		InputTokens: 100_000, OutputTokens: 25_000, UsageSource: "PROVIDER_REPORTED"})
	if err != nil || promotionOperation.SettledAmount.String() != "0.100000000000" || promotionOperation.ConsumedPromotionAmount.String() != "0.050000000000" {
		t.Fatalf("promotion settlement=%+v err=%v", promotionOperation, err)
	}
	if _, err = s.ReverseFunding(ctx, promotionOperation.ID, "promotion-reversal", "synthetic integration reversal", nil); err != nil {
		t.Fatal(err)
	}
	var restoredBonus string
	if err = s.pool.QueryRow(ctx, `SELECT amount_remaining::text FROM promotion_credit WHERE organization_id=$1 AND idempotency_key='funding-fixture-bonus'`, domain.LegacyOrganizationID).Scan(&restoredBonus); err != nil {
		t.Fatal(err)
	}
	if restoredBonus != "0.050000000000" {
		t.Fatalf("restored promotion=%s", restoredBonus)
	}

	const workers = 100
	var wg sync.WaitGroup
	results := make(chan domain.FundingOperation, workers)
	errs := make(chan error, workers)
	for index := range workers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			operation, _, reserveErr := s.ReserveFunding(ctx, FundingReservationRequest{OrganizationID: domain.LegacyOrganizationID,
				ProjectID: domain.LegacyProjectID, RequestID: fmt.Sprintf("concurrent-request-%03d", index),
				IdempotencyKey: fmt.Sprintf("concurrent-key-%03d", index), RequestFingerprint: fmt.Sprintf("fingerprint-%03d", index),
				PricingVersionID: pricingVersionID, Currency: "USD", MaximumAmount: "0.2", EstimatedInput: 10, MaxOutput: 10})
			if reserveErr != nil {
				errs <- reserveErr
				return
			}
			results <- operation
		}(index)
	}
	wg.Wait()
	close(results)
	close(errs)
	successes, rejected := 0, 0
	operations := make([]domain.FundingOperation, 0, 50)
	for operation := range results {
		successes++
		operations = append(operations, operation)
	}
	for reserveErr := range errs {
		if !errors.Is(reserveErr, ErrWalletUnavailable) {
			t.Fatal(reserveErr)
		}
		rejected++
	}
	if successes != 50 || rejected != 50 {
		t.Fatalf("concurrent reservations successes=%d rejected=%d", successes, rejected)
	}
	assertWalletAmounts(t, ctx, s, walletID, "0.000000000000", "10.000000000000")

	successful := operations[0]
	replay, replayed, err := s.ReserveFunding(ctx, FundingReservationRequest{OrganizationID: domain.LegacyOrganizationID,
		ProjectID: domain.LegacyProjectID, RequestID: "ignored-replay-request", IdempotencyKey: successful.IdempotencyKey,
		RequestFingerprint: successful.RequestFingerprint, PricingVersionID: pricingVersionID, Currency: "USD", MaximumAmount: "0.2", EstimatedInput: 10, MaxOutput: 10})
	if err != nil || !replayed || replay.ID != successful.ID {
		t.Fatalf("idempotent replay=%+v replayed=%v err=%v", replay, replayed, err)
	}
	if _, _, err = s.ReserveFunding(ctx, FundingReservationRequest{OrganizationID: domain.LegacyOrganizationID, ProjectID: domain.LegacyProjectID,
		RequestID: "conflict", IdempotencyKey: successful.IdempotencyKey, RequestFingerprint: "different", PricingVersionID: pricingVersionID,
		Currency: "USD", MaximumAmount: "0.2", EstimatedInput: 10, MaxOutput: 10}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict=%v", err)
	}

	partial := successful
	partial, err = s.SettleFunding(ctx, FundingSettlementRequest{OperationID: partial.ID, InputTokens: 100_000, OutputTokens: 25_000,
		ObservedBytes: 100_000, UsageSource: "ESTIMATED_PARTIAL_STREAM", FailureCode: "client_disconnected", PartialFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if partial.Status != "PARTIALLY_SETTLED" || partial.SettledAmount.String() != "0.150000000000" || partial.ReleasedAmount.String() != "0.050000000000" {
		t.Fatalf("partial settlement=%+v", partial)
	}

	var crash domain.FundingOperation
	for _, candidate := range operations {
		if candidate.ID != partial.ID {
			crash = candidate
			break
		}
	}
	if crash.ID == "" {
		t.Fatal("no reserved operation available for crash recovery")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE funding_operation SET heartbeat_at=now()-interval '1 hour',observed_output_bytes=4000 WHERE id=$1`, crash.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverStaleFunding(ctx, time.Now().UTC().Add(-10*time.Minute), 100)
	if err != nil || recovered < 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	crash, err = s.FundingOperationByID(ctx, crash.ID)
	if err != nil || crash.Status != "PARTIALLY_SETTLED" || crash.UsageSource != "ESTIMATED_CRASH_RECOVERY" {
		t.Fatalf("crash recovery=%+v err=%v", crash, err)
	}

	late, err := s.AdjustLateFundingUsage(ctx, LateUsageRequest{OperationID: crash.ID, IdempotencyKey: "late-usage-1",
		InputTokens: 100_000, OutputTokens: 50_000, UsageSource: "PROVIDER_LATE"})
	if err != nil {
		t.Fatal(err)
	}
	if late.SettledAmount.String() != "0.200000000000" || late.UsageSource != "PROVIDER_LATE" {
		t.Fatalf("late usage=%+v", late)
	}
	lateReplay, err := s.AdjustLateFundingUsage(ctx, LateUsageRequest{OperationID: crash.ID, IdempotencyKey: "late-usage-1",
		InputTokens: 100_000, OutputTokens: 50_000, UsageSource: "PROVIDER_LATE"})
	if err != nil || lateReplay.SettledAmount.String() != late.SettledAmount.String() {
		t.Fatalf("late replay=%+v err=%v", lateReplay, err)
	}

	if _, err = s.pool.Exec(ctx, `UPDATE ledger_journal SET reference='tamper' WHERE id=(SELECT id FROM ledger_journal WHERE status='POSTED' LIMIT 1)`); err == nil {
		t.Fatal("posted journal accepted update")
	}
	if _, err = s.pool.Exec(ctx, `DELETE FROM ledger_journal_entry WHERE id=(SELECT id FROM ledger_journal_entry LIMIT 1)`); err == nil {
		t.Fatal("journal entry accepted delete")
	}
	if _, err = s.pool.Exec(ctx, `UPDATE ledger_account SET name='tamper' WHERE id=(SELECT id FROM ledger_account LIMIT 1)`); err == nil {
		t.Fatal("ledger account accepted update")
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO ledger_journal(id,journal_type,external_key,currency,status,posted_at)
		VALUES(gen_random_uuid(),'ADJUSTMENT',$1,'USD','POSTED',now())`, "unbalanced-direct-"+id.UUID()); err == nil {
		t.Fatal("direct unbalanced posted journal accepted")
	}
	var unbalanced int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT journal_id,sum(amount) FILTER(WHERE entry_side='DEBIT') debit,
		sum(amount) FILTER(WHERE entry_side='CREDIT') credit FROM ledger_journal_entry GROUP BY journal_id HAVING
		sum(amount) FILTER(WHERE entry_side='DEBIT')<>sum(amount) FILTER(WHERE entry_side='CREDIT')) v`).Scan(&unbalanced); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 {
		t.Fatalf("unbalanced journals=%d", unbalanced)
	}
	var replayAvailable, replayReserved string
	err = s.pool.QueryRow(ctx, `SELECT
		COALESCE(sum(CASE WHEN a.account_key='wallet:'||$1||':available' THEN CASE e.entry_side WHEN 'CREDIT' THEN e.amount ELSE -e.amount END ELSE 0 END),0)::text,
		COALESCE(sum(CASE WHEN a.account_key='wallet:'||$1||':reserved' THEN CASE e.entry_side WHEN 'CREDIT' THEN e.amount ELSE -e.amount END ELSE 0 END),0)::text
		FROM ledger_journal_entry e JOIN ledger_journal j ON j.id=e.journal_id AND j.status='POSTED' JOIN ledger_account a ON a.id=e.account_id`, walletID).Scan(&replayAvailable, &replayReserved)
	if err != nil {
		t.Fatal(err)
	}
	var walletAvailable, walletReserved string
	if err = s.pool.QueryRow(ctx, `SELECT available_balance::text,reserved_balance::text FROM wallets WHERE id=$1`, walletID).Scan(&walletAvailable, &walletReserved); err != nil {
		t.Fatal(err)
	}
	if replayAvailable != walletAvailable || replayReserved != walletReserved {
		t.Fatalf("replay available=%s/%s reserved=%s/%s", replayAvailable, walletAvailable, replayReserved, walletReserved)
	}
}

func fundingFixture(t *testing.T, ctx context.Context, s *Store) (string, string, string) {
	t.Helper()
	var providerID string
	if err := s.pool.QueryRow(ctx, `SELECT id FROM providers WHERE slug='openai'`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE providers SET enabled=true,pricing_disabled=false,commercial_status='COMMERCIAL_APPROVED',
		commercial_resale_status='APPROVED',allowed_customer_regions='["*"]',data_processing_regions='["*"]',emergency_kill_switch=false WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	modelID := id.UUID()
	if _, err := s.pool.Exec(ctx, `INSERT INTO models(id,provider_id,provider_model_id,display_name,model_type,enabled)
		VALUES($1,$2,$3,'Funding Integration','text',true)`, modelID, providerID, "funding-"+modelID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	if _, err := s.CreateProviderCostPriceBook(ctx, domain.ProviderCostPriceBook{ProviderID: providerID, ModelID: modelID,
		InputTokenCost: "0.5", CachedInputTokenCost: "0.1", OutputTokenCost: "1", RequestFixedCost: "0", Currency: "USD",
		Unit: 1_000_000, EffectiveAt: now, Source: "funding-integration", ApprovalStatus: "APPROVED"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCustomerRetailPriceBook(ctx, domain.CustomerRetailPriceBook{ProviderID: providerID, ModelID: modelID,
		InputTokenPrice: "1", CachedInputTokenPrice: "0.2", OutputTokenPrice: "2", RequestFixedPrice: "0", Currency: "USD",
		Unit: 1_000_000, EffectiveAt: now, Source: "funding-integration"}, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	quote, err := s.QuotePricing(ctx, PriceQuoteRequest{OrganizationID: domain.LegacyOrganizationID, ProviderID: providerID,
		Model: modelID, InputTokens: 100_000, OutputTokens: 50_000, PromotionAmount: "0", ExchangeRate: "1"})
	if err != nil {
		t.Fatal(err)
	}
	return providerID, modelID, quote.PricingVersionID
}

func assertWalletAmounts(t *testing.T, ctx context.Context, s *Store, walletID, available, reserved string) {
	t.Helper()
	var actualAvailable, actualReserved string
	if err := s.pool.QueryRow(ctx, `SELECT available_balance::text,reserved_balance::text FROM wallets WHERE id=$1`, walletID).Scan(&actualAvailable, &actualReserved); err != nil {
		t.Fatal(err)
	}
	if actualAvailable != available || actualReserved != reserved {
		t.Fatalf("wallet available=%s reserved=%s", actualAvailable, actualReserved)
	}
}
