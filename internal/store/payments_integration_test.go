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
	"github.com/relayedock/relayedock/internal/payment"
)

func TestPaymentOrdersIntegration(t *testing.T) {
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
	organizationID, walletID := paymentFixture(t, ctx, s)
	const idempotencyWorkers = 20
	createdIDs := make(chan string, idempotencyWorkers)
	createErrors := make(chan error, idempotencyWorkers)
	var createWG sync.WaitGroup
	for index := range idempotencyWorkers {
		createWG.Add(1)
		go func(index int) {
			defer createWG.Done()
			created, _, createErr := s.CreateRechargeOrder(ctx, CreateRechargeOrderRequest{PlatformOrderNo: fmt.Sprintf("RO_CONCURRENT_%02d", index),
				OrganizationID: organizationID, PaymentProvider: "sandbox", Amount: domain.Decimal("1.000000000000"), Currency: "USD",
				Region: "CN", IdempotencyKey: "concurrent-create", ExpiresAt: time.Now().UTC().Add(time.Hour)})
			if createErr != nil {
				createErrors <- createErr
				return
			}
			createdIDs <- created.ID
		}(index)
	}
	createWG.Wait()
	close(createdIDs)
	close(createErrors)
	for createErr := range createErrors {
		t.Fatal(createErr)
	}
	uniqueCreated := map[string]struct{}{}
	for createdID := range createdIDs {
		uniqueCreated[createdID] = struct{}{}
	}
	if len(uniqueCreated) != 1 {
		t.Fatalf("concurrent idempotent create returned %d orders", len(uniqueCreated))
	}

	order := createPendingPaymentFixture(t, ctx, s, organizationID, "replay", "10.250000000000")
	verified := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: order.ProviderOrderNo,
		PlatformOrderNo: order.PlatformOrderNo, EventType: "payment.paid", Status: "PAID", Amount: order.Amount,
		Currency: order.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID(),
		NormalizedPayload: map[string]any{"status": "PAID"}}
	const replays = 100
	var wg sync.WaitGroup
	errs := make(chan error, replays)
	for range replays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			current, _, recordErr := s.RecordVerifiedPaymentWebhook(ctx, "sandbox", verified, fmt.Sprintf("%064x", 1))
			if recordErr != nil {
				errs <- recordErr
				return
			}
			if current.Status == "PAID" {
				_, _, recordErr = s.CreditPaidRecharge(ctx, current.ID)
			}
			if recordErr != nil {
				errs <- recordErr
			}
		}()
	}
	wg.Wait()
	close(errs)
	for replayErr := range errs {
		t.Fatal(replayErr)
	}
	assertPaymentTrace(t, ctx, s, order.ID, walletID, "10.250000000000", 1)

	failed := createPendingPaymentFixture(t, ctx, s, organizationID, "failed", "3.000000000000")
	failedEvent := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: failed.ProviderOrderNo,
		PlatformOrderNo: failed.PlatformOrderNo, EventType: "payment.failed", Status: "FAILED", Amount: failed.Amount,
		Currency: failed.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	failed, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", failedEvent, fmt.Sprintf("%064x", 2))
	if err != nil || failed.Status != "FAILED" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	if _, _, err = s.CreditPaidRecharge(ctx, failed.ID); err != ErrPaymentState {
		t.Fatalf("failed payment credit err=%v", err)
	}

	crash := createPendingPaymentFixture(t, ctx, s, organizationID, "crash", "4.750000000000")
	crashEvent := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: crash.ProviderOrderNo,
		PlatformOrderNo: crash.PlatformOrderNo, EventType: "payment.paid", Status: "PAID", Amount: crash.Amount,
		Currency: crash.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	crash, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", crashEvent, fmt.Sprintf("%064x", 3))
	if err != nil || crash.Status != "PAID" {
		t.Fatalf("durable paid=%+v err=%v", crash, err)
	}
	recoverable, err := s.ListRecoverableRechargeOrders(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range recoverable {
		if candidate.ID == crash.ID {
			found = true
			_, replayed, creditErr := s.CreditPaidRecharge(ctx, candidate.ID)
			if creditErr != nil || replayed {
				t.Fatalf("recovery replayed=%v err=%v", replayed, creditErr)
			}
		}
	}
	if !found {
		t.Fatal("paid order was not recoverable")
	}
	assertPaymentTrace(t, ctx, s, crash.ID, walletID, "15.000000000000", 1)
	var refundableCash string
	if err = s.pool.QueryRow(ctx, `SELECT remaining_amount::text FROM wallet_cash_lot WHERE recharge_order_id=$1`, crash.ID).Scan(&refundableCash); err != nil || refundableCash != "4.750000000000" {
		t.Fatalf("refundable cash=%s err=%v", refundableCash, err)
	}

	refund, _, err := s.CreateRefundOrder(ctx, CreateRefundOrderRequest{PlatformRefundNo: "RF_" + stringsNoDash(id.UUID()),
		RechargeOrderID: crash.ID, Amount: crash.Amount, Reason: "integration refund", IdempotencyKey: "refund-once"})
	if err != nil {
		t.Fatal(err)
	}
	refund, replayed, err := s.CompleteRefund(ctx, refund.ID, "sbxr_"+refund.PlatformRefundNo)
	if err != nil || replayed || refund.Status != "SUCCEEDED" || refund.WalletTransactionID == nil || refund.LedgerJournalID == nil {
		t.Fatalf("refund=%+v replayed=%v err=%v", refund, replayed, err)
	}
	assertWalletAvailable(t, ctx, s, walletID, "10.250000000000")
	if err = s.pool.QueryRow(ctx, `SELECT remaining_amount::text FROM wallet_cash_lot WHERE recharge_order_id=$1`, crash.ID).Scan(&refundableCash); err != nil || refundableCash != "0.000000000000" {
		t.Fatalf("refunded cash lot=%s err=%v", refundableCash, err)
	}

	partialOrder := createPendingPaymentFixture(t, ctx, s, organizationID, "partial-refund", "5.000000000000")
	partialEvent := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: partialOrder.ProviderOrderNo,
		PlatformOrderNo: partialOrder.PlatformOrderNo, EventType: "payment.paid", Status: "PAID", Amount: partialOrder.Amount,
		Currency: partialOrder.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	partialOrder, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", partialEvent, fmt.Sprintf("%064x", 6))
	if err != nil {
		t.Fatal(err)
	}
	partialOrder, _, err = s.CreditPaidRecharge(ctx, partialOrder.ID)
	if err != nil {
		t.Fatal(err)
	}
	var requestedBy string
	requestedBy = id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Finance Integration','ADMIN','ACTIVE',now())`, requestedBy, "finance-"+requestedBy+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	application, _, err := s.CreateRefundApplication(ctx, CreateRefundApplicationRequest{OrganizationID: organizationID,
		SourceType: "RECHARGE", RechargeOrderID: partialOrder.ID, Amount: "2.000000000000", Reason: "synthetic partial refund",
		IdempotencyKey: "partial-refund-application", RequestedBy: requestedBy})
	if err != nil {
		t.Fatal(err)
	}
	application, _, err = s.DecideRefundApplication(ctx, application.ID, "APPROVE", "verified unused cash", "partial-refund-approval", requestedBy)
	if err != nil || application.Status != "APPROVED" {
		t.Fatalf("refund application=%+v err=%v", application, err)
	}
	if _, _, err = s.DecideRefundApplication(ctx, application.ID, "REJECT", "changed decision must conflict", "partial-refund-approval", requestedBy); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("refund decision replay accepted changed payload: %v", err)
	}
	secondApplication, _, err := s.CreateRefundApplication(ctx, CreateRefundApplicationRequest{OrganizationID: organizationID,
		SourceType: "RECHARGE", RechargeOrderID: partialOrder.ID, Amount: "2.000000000000", Reason: application.Reason,
		IdempotencyKey: "second-partial-refund-application", RequestedBy: requestedBy})
	if err != nil {
		t.Fatal(err)
	}
	secondApplication, _, err = s.DecideRefundApplication(ctx, secondApplication.ID, "APPROVE", "verified second unused cash",
		"second-partial-refund-approval", requestedBy)
	if err != nil || secondApplication.Status != "APPROVED" {
		t.Fatalf("second refund application=%+v err=%v", secondApplication, err)
	}
	partialRefund, _, err := s.CreateRefundOrder(ctx, CreateRefundOrderRequest{PlatformRefundNo: "RF_" + stringsNoDash(id.UUID()),
		RechargeOrderID: partialOrder.ID, RefundApplicationID: application.ID, Amount: domain.Decimal("2.000000000000"),
		Reason: application.Reason, IdempotencyKey: "partial-refund-order", CreatedBy: &requestedBy})
	if err != nil {
		t.Fatal(err)
	}
	if partialRefund.RefundApplicationID == nil || *partialRefund.RefundApplicationID != application.ID {
		t.Fatalf("refund order does not trace back to its application: %+v", partialRefund)
	}
	if _, _, err = s.CreateRefundOrder(ctx, CreateRefundOrderRequest{PlatformRefundNo: "RF_" + stringsNoDash(id.UUID()),
		RechargeOrderID: partialOrder.ID, RefundApplicationID: secondApplication.ID, Amount: domain.Decimal("2.000000000000"),
		Reason: application.Reason, IdempotencyKey: "partial-refund-order", CreatedBy: &requestedBy}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("refund replay accepted a different application: %v", err)
	}
	if _, _, err = s.CreateRefundOrder(ctx, CreateRefundOrderRequest{PlatformRefundNo: "RF_" + stringsNoDash(id.UUID()),
		RechargeOrderID: partialOrder.ID, Amount: domain.Decimal("2.000000000000"), Reason: application.Reason,
		IdempotencyKey: "partial-refund-order", CreatedBy: &requestedBy}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("refund replay accepted application removal: %v", err)
	}
	partialRefund, _, err = s.CompleteRefund(ctx, partialRefund.ID, "manual:synthetic-evidence")
	if err != nil || partialRefund.Status != "SUCCEEDED" {
		t.Fatalf("partial refund=%+v err=%v", partialRefund, err)
	}
	application, err = s.RefundApplicationByID(ctx, application.ID)
	if err != nil || application.Status != "COMPLETED" {
		t.Fatalf("completed refund application=%+v err=%v", application, err)
	}
	partialOrder, err = s.RechargeOrderByID(ctx, partialOrder.ID)
	if err != nil || partialOrder.Status != "CREDITED" {
		t.Fatalf("partially refunded recharge=%+v err=%v", partialOrder, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT remaining_amount::text FROM wallet_cash_lot WHERE recharge_order_id=$1`, partialOrder.ID).Scan(&refundableCash); err != nil || refundableCash != "3.000000000000" {
		t.Fatalf("partial refundable lot=%s err=%v", refundableCash, err)
	}

	failedRefund, _, err := s.CreateRefundOrder(ctx, CreateRefundOrderRequest{PlatformRefundNo: "RF_" + stringsNoDash(id.UUID()),
		RechargeOrderID: partialOrder.ID, RefundApplicationID: secondApplication.ID, Amount: domain.Decimal("2.000000000000"),
		Reason: secondApplication.Reason, IdempotencyKey: "failed-partial-refund-order", CreatedBy: &requestedBy})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FailRefund(ctx, failedRefund.ID, "provider_declined"); err != nil {
		t.Fatal(err)
	}
	if err = s.FailRefund(ctx, failedRefund.ID, "provider_declined"); err != nil {
		t.Fatalf("failed refund exact replay=%v", err)
	}
	if err = s.FailRefund(ctx, failedRefund.ID, "changed_failure"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("failed refund changed replay=%v", err)
	}
	var released, failedRefundAudits string
	if err = s.pool.QueryRow(ctx, `SELECT allocation.released_amount::text,
		(SELECT count(*)::text FROM audit_logs WHERE action='payment.refund_failed' AND resource_id=$1::text)
		FROM refund_cash_allocation allocation WHERE allocation.refund_order_id=$1::uuid`, failedRefund.ID).
		Scan(&released, &failedRefundAudits); err != nil || released != "2.000000000000" || failedRefundAudits != "1" {
		t.Fatalf("failed refund release=%s audits=%s err=%v", released, failedRefundAudits, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT remaining_amount::text FROM wallet_cash_lot WHERE recharge_order_id=$1`, partialOrder.ID).Scan(&refundableCash); err != nil || refundableCash != "3.000000000000" {
		t.Fatalf("failed refund did not restore source lot=%s err=%v", refundableCash, err)
	}

	chargeback := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: order.ProviderOrderNo,
		PlatformOrderNo: order.PlatformOrderNo, EventType: "payment.chargeback", Status: "CHARGEBACK", Amount: order.Amount,
		Currency: order.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	order, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", chargeback, fmt.Sprintf("%064x", 4))
	if err != nil || order.Status != "CHARGEBACK" {
		t.Fatalf("chargeback=%+v err=%v", order, err)
	}
	assertWalletAvailable(t, ctx, s, walletID, "3.000000000000")
	var allocatedTotal, cashAllocated, creditAllocated string
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(allocation.amount),0)::text,
		COALESCE(sum(allocation.amount) FILTER (WHERE allocation.bucket='CASH'),0)::text,
		COALESCE(sum(allocation.amount) FILTER (WHERE allocation.bucket='CREDIT'),0)::text
		FROM wallet_cash_allocation allocation
		JOIN wallet_transactions transaction ON transaction.id=allocation.wallet_transaction_id
		WHERE transaction.refund_order_id=(SELECT id FROM refund_order WHERE recharge_order_id=$1 AND provider_refund_no LIKE 'chargeback:%')`, order.ID).
		Scan(&allocatedTotal, &cashAllocated, &creditAllocated); err != nil || !decimalEqual(allocatedTotal, order.Amount.String()) ||
		!decimalEqual(cashAllocated, "10.250000000000") || !decimalEqual(creditAllocated, "0") {
		t.Fatalf("chargeback allocation total=%s cash=%s credit=%s err=%v", allocatedTotal, cashAllocated, creditAllocated, err)
	}

	consumedChargeback := createPendingPaymentFixture(t, ctx, s, organizationID, "consumed-chargeback", "3.000000000000")
	consumedEvent := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: consumedChargeback.ProviderOrderNo,
		PlatformOrderNo: consumedChargeback.PlatformOrderNo, EventType: "payment.paid", Status: "PAID", Amount: consumedChargeback.Amount,
		Currency: consumedChargeback.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	consumedChargeback, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", consumedEvent, fmt.Sprintf("%064x", 7))
	if err == nil {
		consumedChargeback, _, err = s.CreditPaidRecharge(ctx, consumedChargeback.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	var consumedLotID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallet_cash_lot WHERE recharge_order_id=$1`, consumedChargeback.ID).Scan(&consumedLotID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=1 WHERE id=$1`, consumedLotID); err != nil {
		t.Fatal(err)
	}
	consumedEvent = payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: consumedChargeback.ProviderOrderNo,
		PlatformOrderNo: consumedChargeback.PlatformOrderNo, EventType: "payment.chargeback", Status: "CHARGEBACK", Amount: consumedChargeback.Amount,
		Currency: consumedChargeback.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	consumedChargeback, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", consumedEvent, fmt.Sprintf("%064x", 8))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(sum(allocation.amount),0)::text,
		COALESCE(sum(allocation.amount) FILTER (WHERE allocation.bucket='CASH'),0)::text,
		COALESCE(sum(allocation.amount) FILTER (WHERE allocation.bucket='CREDIT'),0)::text
		FROM wallet_cash_allocation allocation
		JOIN wallet_transactions transaction ON transaction.id=allocation.wallet_transaction_id
		WHERE transaction.refund_order_id=(SELECT id FROM refund_order WHERE recharge_order_id=$1 AND provider_refund_no LIKE 'chargeback:%')`, consumedChargeback.ID).
		Scan(&allocatedTotal, &cashAllocated, &creditAllocated); err != nil || !decimalEqual(allocatedTotal, "3") ||
		!decimalEqual(cashAllocated, "1") || !decimalEqual(creditAllocated, "2") {
		t.Fatalf("consumed chargeback total=%s cash=%s credit=%s err=%v", allocatedTotal, cashAllocated, creditAllocated, err)
	}

	late := createPendingPaymentFixture(t, ctx, s, organizationID, "late-paid", "2.000000000000")
	if _, err = s.pool.Exec(ctx, `UPDATE recharge_order SET expires_at=now()-interval '1 second' WHERE id=$1`, late.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpireRechargeOrders(ctx, time.Now().UTC(), 100); err != nil {
		t.Fatal(err)
	}
	lateEvent := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: late.ProviderOrderNo,
		PlatformOrderNo: late.PlatformOrderNo, EventType: "payment.paid", Status: "PAID", Amount: late.Amount,
		Currency: late.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	late, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", lateEvent, fmt.Sprintf("%064x", 5))
	if err != nil || late.Status != "PAID" {
		t.Fatalf("late paid=%+v err=%v", late, err)
	}
	late, _, err = s.CreditPaidRecharge(ctx, late.ID)
	if err != nil || late.Status != "CREDITED" {
		t.Fatalf("late credit=%+v err=%v", late, err)
	}
	assertWalletAvailable(t, ctx, s, walletID, "5.000000000000")
}

func assertWalletAvailable(t *testing.T, ctx context.Context, s *Store, walletID, expected string) {
	t.Helper()
	var actual string
	if err := s.pool.QueryRow(ctx, `SELECT available_balance::text FROM wallets WHERE id=$1`, walletID).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("wallet available=%s want=%s", actual, expected)
	}
}

func paymentFixture(t *testing.T, ctx context.Context, s *Store) (string, string) {
	t.Helper()
	organizationID := id.UUID()
	slug := "payment-" + stringsNoDash(organizationID)
	if _, err := s.pool.Exec(ctx, `INSERT INTO organizations(id,name,slug,status) VALUES($1,'Payment Integration',$2,'ACTIVE')`, organizationID, slug); err != nil {
		t.Fatal(err)
	}
	var walletID string
	if err := s.pool.QueryRow(ctx, `SELECT id FROM wallets WHERE organization_id=$1`, organizationID).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	return organizationID, walletID
}

func createPendingPaymentFixture(t *testing.T, ctx context.Context, s *Store, organizationID, suffix, amount string) domain.RechargeOrder {
	t.Helper()
	order, _, err := s.CreateRechargeOrder(ctx, CreateRechargeOrderRequest{PlatformOrderNo: "RO_" + stringsNoDash(id.UUID()),
		OrganizationID: organizationID, PaymentProvider: "sandbox", Amount: domain.Decimal(amount), Currency: "USD",
		Region: "CN", IdempotencyKey: "payment-" + suffix, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartPaymentAttempt(ctx, order.ID, "CREATE", "fixture", map[string]any{"source": "integration"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishPaymentAttempt(ctx, attempt.ID, "SUCCEEDED", "sbx_"+order.PlatformOrderNo, "PENDING", "", nil); err != nil {
		t.Fatal(err)
	}
	order, err = s.MarkRechargePending(ctx, order.ID, "sbx_"+order.PlatformOrderNo, map[string]any{"mode": "test"})
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func assertPaymentTrace(t *testing.T, ctx context.Context, s *Store, orderID, walletID, expectedBalance string, expectedRows int) {
	t.Helper()
	order, err := s.RechargeOrderByID(ctx, orderID)
	if err != nil || order.Status != "CREDITED" || order.WalletTransactionID == nil || order.LedgerJournalID == nil {
		t.Fatalf("credited order=%+v err=%v", order, err)
	}
	var balance string
	if err = s.pool.QueryRow(ctx, `SELECT available_balance::text FROM wallets WHERE id=$1`, walletID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != expectedBalance {
		t.Fatalf("balance=%s want=%s", balance, expectedBalance)
	}
	var transactions, journals, webhooks, linkedEntries int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM wallet_transactions WHERE recharge_order_id=$1`, orderID).Scan(&transactions); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_journal WHERE recharge_order_id=$1`, orderID).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM payment_webhook_event WHERE recharge_order_id=$1`, orderID).Scan(&webhooks); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_journal_entry WHERE journal_id=$1`, *order.LedgerJournalID).Scan(&linkedEntries); err != nil {
		t.Fatal(err)
	}
	if transactions != expectedRows || journals != expectedRows || webhooks != expectedRows || linkedEntries != 2 {
		t.Fatalf("trace transactions=%d journals=%d webhooks=%d entries=%d", transactions, journals, webhooks, linkedEntries)
	}
}

func stringsNoDash(value string) string {
	result := ""
	for _, character := range value {
		if character != '-' {
			result += string(character)
		}
	}
	return result
}
