package store

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/payment"
)

func TestFinancialCloseIntegration(t *testing.T) {
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
	var walletID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM wallets ORDER BY created_at,id LIMIT 1`).Scan(&walletID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateWalletTransaction(ctx, domain.WalletTransaction{WalletID: walletID, TransactionType: "ADJUSTMENT",
		Amount: domain.Decimal("1"), IdempotencyKey: "financial-close-immutable-fixture", Reference: "synthetic integration evidence"}); err != nil {
		t.Fatal(err)
	}

	// Build a paid subscription fixture in this disposable database so the
	// cumulative and concurrent refund boundary is always exercised.
	var organizationID, requesterID string
	if err = s.pool.QueryRow(ctx, `SELECT organization_id FROM wallets WHERE id=$1`, walletID).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	requesterID = id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,role,status,email_verified_at)
		VALUES($1,$2,'synthetic-hash','Finance Close Integration','ADMIN','ACTIVE',now())`, requesterID, "finance-close-"+requesterID+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id,user_id,role,status) VALUES($1,$2,'ADMIN','ACTIVE')`, organizationID, requesterID); err != nil {
		t.Fatal(err)
	}
	subscription, invoice, err := s.ChangeSubscription(ctx, SubscriptionChangeRequest{OrganizationID: organizationID,
		PlanVersionID: "11000000-0000-4000-8000-000000000002", Mode: "IMMEDIATE", IdempotencyKey: "finclose-subscription"})
	if err != nil || invoice == nil {
		t.Fatalf("subscription=%+v invoice=%+v err=%v", subscription, invoice, err)
	}
	paidInvoice, err := s.PaySubscriptionInvoice(ctx, SubscriptionPaymentRequest{InvoiceID: invoice.ID,
		PaymentProvider: "manual_contract_test", ProviderPaymentReference: "synthetic-finclose-subscription-payment",
		IdempotencyKey: "finclose-subscription-payment"})
	if err != nil || paidInvoice.Status != "PAID" {
		t.Fatalf("paid invoice=%+v err=%v", paidInvoice, err)
	}
	if paidInvoice.PaidAt == nil {
		t.Fatal("paid invoice has no paid_at evidence")
	}
	var subscriptionMatched bool
	if err = s.pool.QueryRow(ctx, `SELECT candidate.matched FROM (`+subscriptionToStateSQL+`) AS candidate(`+reconciliationCandidateColumns+`) WHERE candidate.observation_key=$2`,
		paidInvoice.PaidAt.UTC().Format("2006-01-02"), paidInvoice.ID).Scan(&subscriptionMatched); err != nil || !subscriptionMatched {
		t.Fatalf("paid subscription reconciliation matched=%v err=%v", subscriptionMatched, err)
	}
	// A historical mismatch cannot veto later verified matching evidence; the
	// daily check evaluates whether at least one current source matches.
	rechargeForRecon := createPendingPaymentFixture(t, ctx, s, organizationID, "finclose-reconciliation-payment", "2.000000000000")
	if _, err = s.pool.Exec(ctx, `UPDATE recharge_order SET status='PAID',paid_at=current_date + interval '1 hour',updated_at=now()
		WHERE id=$1`, rechargeForRecon.ID); err != nil {
		t.Fatal(err)
	}
	rechargeForRecon, err = s.RechargeOrderByID(ctx, rechargeForRecon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordReconciliation(ctx, rechargeForRecon, "finclose-mismatch", payment.ReconcileResult{ProviderStatus: "FAILED",
		Amount: domain.Decimal("1.000000000000"), Currency: rechargeForRecon.Currency, EvidenceSource: "PROVIDER_API"}, &requesterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordReconciliation(ctx, rechargeForRecon, "finclose-match", payment.ReconcileResult{ProviderStatus: "PAID",
		Amount: rechargeForRecon.Amount, Currency: rechargeForRecon.Currency, EvidenceSource: "PROVIDER_API"}, &requesterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordReconciliation(ctx, rechargeForRecon, "finclose-match", payment.ReconcileResult{ProviderStatus: "PAID",
		Amount: rechargeForRecon.Amount, Currency: rechargeForRecon.Currency, EvidenceSource: "PROVIDER_API"}, &requesterID); err != nil {
		t.Fatalf("payment reconciliation exact replay failed: %v", err)
	}
	if _, err = s.RecordReconciliation(ctx, rechargeForRecon, "finclose-match", payment.ReconcileResult{ProviderStatus: "PAID",
		Amount: domain.Decimal("1.000000000000"), Currency: rechargeForRecon.Currency, EvidenceSource: "PROVIDER_API"}, &requesterID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("payment reconciliation accepted changed replay payload: %v", err)
	}
	var paymentMatched bool
	if err = s.pool.QueryRow(ctx, `SELECT matched FROM (`+paymentToRechargeSQL+`) candidate WHERE observation_key=$2`,
		time.Now().UTC().Format("2006-01-02"), rechargeForRecon.ID).Scan(&paymentMatched); err != nil || !paymentMatched {
		t.Fatalf("current matching payment evidence was vetoed matched=%v err=%v", paymentMatched, err)
	}
	// The channel settles a payment as PAID; wallet crediting and refund state
	// are local lifecycle transitions and must not turn valid channel evidence
	// into a false mismatch. The daily query also follows the state-change date,
	// rather than permanently pinning an order to its creation date.
	rechargeForRecon, _, err = s.CreditPaidRecharge(ctx, rechargeForRecon.ID)
	if err != nil || rechargeForRecon.Status != "CREDITED" {
		t.Fatalf("credit recharge=%+v err=%v", rechargeForRecon, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT matched FROM (`+paymentToRechargeSQL+`) candidate WHERE observation_key=$2`,
		time.Now().UTC().Format("2006-01-02"), rechargeForRecon.ID).Scan(&paymentMatched); err != nil || !paymentMatched {
		t.Fatalf("credited recharge did not normalize channel PAID evidence matched=%v err=%v", paymentMatched, err)
	}
	if record, err := s.RecordReconciliation(ctx, rechargeForRecon, "finclose-credited-match", payment.ReconcileResult{ProviderStatus: "PAID",
		Amount: rechargeForRecon.Amount, Currency: rechargeForRecon.Currency, EvidenceSource: "PROVIDER_API"}, &requesterID); err != nil || record.Result != "MATCHED" {
		t.Fatalf("credited recharge reconciliation record=%+v err=%v", record, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE recharge_order SET created_at=current_date - interval '2 days',paid_at=current_date - interval '1 day',
		credited_at=current_date,updated_at=current_date WHERE id=$1`, rechargeForRecon.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT matched FROM (`+paymentToRechargeSQL+`) candidate WHERE observation_key=$2`,
		time.Now().UTC().Format("2006-01-02"), rechargeForRecon.ID).Scan(&paymentMatched); err != nil || !paymentMatched {
		t.Fatalf("cross-day recharge state change was omitted matched=%v err=%v", paymentMatched, err)
	}
	// Restore timestamps so this fixture cannot enter the previous-day close
	// exercised below and change that independent idempotency assertion.
	if _, err = s.pool.Exec(ctx, `UPDATE recharge_order SET created_at=current_date,paid_at=current_date,
		credited_at=current_date,updated_at=current_date WHERE id=$1`, rechargeForRecon.ID); err != nil {
		t.Fatal(err)
	}
	accountingRows, err := s.ListAccountingRows(ctx, FinanceFilter{OrganizationID: organizationID,
		From: time.Now().UTC().Add(-time.Hour), To: time.Now().UTC().Add(time.Hour), Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	foundSubscriptionJournal := false
	for _, row := range accountingRows {
		if paidInvoice.LedgerJournalID != nil && row.JournalID == *paidInvoice.LedgerJournalID {
			foundSubscriptionJournal = true
			break
		}
	}
	if !foundSubscriptionJournal {
		t.Fatal("organization accounting export omitted its walletless subscription journal")
	}
	if _, err = s.ListAccountingRows(ctx, FinanceFilter{OrganizationID: organizationID,
		From: time.Now().UTC().Add(-time.Hour), To: time.Now().UTC().Add(time.Hour), Limit: 1, RejectTruncated: true}); !errors.Is(err, ErrFinanceExportLimit) {
		t.Fatalf("bounded accounting export silently truncated: %v", err)
	}

	// Re-running the same business day returns the original durable run and
	// cannot create duplicate observations or automated repair journals.
	date := time.Now().UTC().AddDate(0, 0, -1)
	first, replayed, err := s.RunFinancialReconciliation(ctx, date, "TEST", nil)
	if err != nil || replayed || first.Status != "COMPLETED" {
		t.Fatalf("first reconciliation=%+v replayed=%v err=%v", first, replayed, err)
	}
	second, replayed, err := s.RunFinancialReconciliation(ctx, date, "TEST", nil)
	if err != nil || !replayed || second.ID != first.ID {
		t.Fatalf("replay reconciliation=%+v replayed=%v err=%v", second, replayed, err)
	}
	var runs, duplicateObservations, repairJournals int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM financial_reconciliation_run WHERE business_date=$1::date`, date.Format("2006-01-02")).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT run_id,observation_key,count(*) FROM financial_reconciliation_observation GROUP BY run_id,observation_key HAVING count(*)>1) duplicate`).Scan(&duplicateObservations); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_journal WHERE journal_type='RECONCILIATION_REVERSAL'`).Scan(&repairJournals); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || duplicateObservations != 0 || repairJournals != 0 {
		t.Fatalf("runs=%d duplicate observations=%d unsolicited repairs=%d", runs, duplicateObservations, repairJournals)
	}

	// Posted finance evidence is append-only; corrections require a reversal.
	if _, err = s.pool.Exec(ctx, `UPDATE ledger_journal SET reference='direct-edit' WHERE status='POSTED' AND id=(SELECT id FROM ledger_journal WHERE status='POSTED' LIMIT 1)`); err == nil {
		t.Fatal("posted financial journal accepted a direct edit")
	}

	// A finance operator cannot attach an unrelated posted journal to a case.
	var unrelatedJournalID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM ledger_journal WHERE wallet_id=$1 AND status='POSTED' ORDER BY created_at,id LIMIT 1`, walletID).Scan(&unrelatedJournalID); err != nil {
		t.Fatal(err)
	}
	var mismatchCaseID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM financial_reconciliation_case WHERE organization_id=$1
		AND recharge_order_id IS NOT NULL AND status='OPEN' ORDER BY created_at,id LIMIT 1`, organizationID).Scan(&mismatchCaseID); err == nil {
		if _, _, err = s.ResolveReconciliationCase(ctx, mismatchCaseID, "REVERSE_JOURNAL", unrelatedJournalID,
			"synthetic cross-source reversal must fail", "finclose-unrelated-reversal", requesterID); !errors.Is(err, ErrFinanceState) {
			t.Fatalf("unrelated journal reversal was accepted: %v", err)
		}
	}
	// Create a deterministic recharge-linked case to exercise the guard even
	// when the day's automatic candidates do not include a recharge mismatch.
	syntheticRunID := id.UUID()
	syntheticCaseID := id.UUID()
	guardRecharge := createPendingPaymentFixture(t, ctx, s, organizationID, "finclose-case-bound", "1.000000000000")
	if _, err = s.pool.Exec(ctx, `INSERT INTO financial_reconciliation_run(id,run_key,business_date,status,trigger_source,completed_at)
		VALUES($1,$2,current_date-2,'COMPLETED','TEST',now())`, syntheticRunID, "test:case-bound:"+syntheticRunID); err != nil {
		t.Fatal(err)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO financial_reconciliation_case(id,case_key,check_type,classification,severity,
		organization_id,recharge_order_id,first_seen_run_id,last_seen_run_id)
		VALUES($1,$2,'RECHARGE_TO_WALLET','SYNTHETIC_LINK_GUARD','HIGH',$3,$4,$5,$5)`,
		syntheticCaseID, "test:case-bound:"+syntheticCaseID, organizationID, guardRecharge.ID, syntheticRunID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("synthetic case-bound fixture rows=%d err=%v", tag.RowsAffected(), err)
	}
	if _, _, err = s.ResolveReconciliationCase(ctx, syntheticCaseID, "REVERSE_JOURNAL", "",
		"missing source journal must fail", "finclose-missing-source-reversal", requesterID); !errors.Is(err, ErrFinanceState) {
		t.Fatalf("store accepted reversal without source journal: %v", err)
	}
	if err == nil {
		if _, _, err = s.ResolveReconciliationCase(ctx, syntheticCaseID, "REVERSE_JOURNAL", unrelatedJournalID,
			"synthetic unrelated journal must fail", "finclose-case-bound-reversal", requesterID); !errors.Is(err, ErrFinanceState) {
			t.Fatalf("case-bound journal guard failed: %v", err)
		}
	}
	// A related source journal produces a balanced, linked reversal exactly
	// once and preserves the original posted journal.
	_, _, pricingVersionID := fundingFixture(t, ctx, s)
	var fundingProjectID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM projects WHERE organization_id=$1 ORDER BY created_at,id LIMIT 1`, organizationID).Scan(&fundingProjectID); err != nil {
		t.Fatal(err)
	}
	operation, _, err := s.ReserveFunding(ctx, FundingReservationRequest{OrganizationID: organizationID,
		ProjectID: fundingProjectID, RequestID: "finclose-related-reversal", IdempotencyKey: "finclose-related-reversal",
		RequestFingerprint: "finclose-related-reversal", PricingVersionID: pricingVersionID, Currency: "USD",
		MaximumAmount: "0.100000000000", EstimatedInput: 10, MaxOutput: 10})
	if err != nil {
		t.Fatal(err)
	}
	operation, err = s.SettleFunding(ctx, FundingSettlementRequest{OperationID: operation.ID, InputTokens: 10, OutputTokens: 10, UsageSource: "SYNTHETIC"})
	if err != nil {
		t.Fatal(err)
	}
	var sourceJournalID, sourceTransactionID string
	if err = s.pool.QueryRow(ctx, `SELECT journal.id,transaction.id FROM ledger_journal journal
		JOIN wallet_transactions transaction ON transaction.journal_id=journal.id
		WHERE transaction.funding_operation_id=$1 AND journal.status='POSTED' AND transaction.amount<0
		ORDER BY journal.created_at,journal.id LIMIT 1`, operation.ID).Scan(&sourceJournalID, &sourceTransactionID); err != nil {
		t.Fatal(err)
	}
	var sourceOperationID string
	if err = s.pool.QueryRow(ctx, `SELECT funding_operation_id FROM wallet_transactions WHERE id=$1`, sourceTransactionID).Scan(&sourceOperationID); err != nil {
		t.Fatal(err)
	}
	reversalRunID := id.UUID()
	reversalCaseID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO financial_reconciliation_run(id,run_key,business_date,status,trigger_source,completed_at)
		VALUES($1,$2,current_date-3,'COMPLETED','TEST',now())`, reversalRunID, "test:reversal:"+reversalRunID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO financial_reconciliation_case(id,case_key,check_type,classification,severity,
		organization_id,funding_operation_id,first_seen_run_id,last_seen_run_id)
		VALUES($1,$2,'USAGE_TO_USER_CHARGE','SYNTHETIC_REVERSAL','HIGH',$3,$4,$5,$5)`,
		reversalCaseID, "test:reversal:"+reversalCaseID, organizationID, sourceOperationID, reversalRunID); err != nil {
		t.Fatal(err)
	}
	resolved, replayed, err := s.ResolveReconciliationCase(ctx, reversalCaseID, "REVERSE_JOURNAL", sourceJournalID,
		"synthetic verified reversal", "finclose-related-reversal", requesterID)
	if err != nil || replayed || resolved.Status != "RESOLVED" {
		t.Fatalf("related reversal=%+v replayed=%v err=%v", resolved, replayed, err)
	}
	resolved, replayed, err = s.ResolveReconciliationCase(ctx, reversalCaseID, "REVERSE_JOURNAL", sourceJournalID,
		"synthetic verified reversal", "finclose-related-reversal", requesterID)
	if err != nil || !replayed || resolved.Status != "RESOLVED" {
		t.Fatalf("related reversal replay=%+v replayed=%v err=%v", resolved, replayed, err)
	}
	if _, _, err = s.ResolveReconciliationCase(ctx, reversalCaseID, "REVERSE_JOURNAL", sourceJournalID,
		"changed replay payload must conflict", "finclose-related-reversal", requesterID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reconciliation idempotency accepted changed payload: %v", err)
	}
	var reversals int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM ledger_journal WHERE reconciliation_case_id=$1
		AND reversal_of_journal_id=$2 AND status='POSTED'`, reversalCaseID, sourceJournalID).Scan(&reversals); err != nil || reversals != 1 {
		t.Fatalf("related reversal journals=%d err=%v", reversals, err)
	}
	var usageCaseID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM financial_reconciliation_case WHERE funding_operation_id IS NOT NULL
		AND status='OPEN' ORDER BY created_at,id LIMIT 1`).Scan(&usageCaseID); err == nil {
		if _, _, err = s.ResolveReconciliationCase(ctx, usageCaseID, "REVERSE_JOURNAL", unrelatedJournalID,
			"synthetic cross-operation reversal must fail", "finclose-unrelated-operation-reversal", requesterID); !errors.Is(err, ErrFinanceState) {
			t.Fatalf("unrelated funding journal reversal was accepted: %v", err)
		}
	}

	// Concurrent refund applications against one paid subscription invoice
	// serialize on that invoice. At most one 60% request can be retained.
	invoiceID := paidInvoice.ID
	if invoiceID != "" {
		var total string
		if err = s.pool.QueryRow(ctx, `SELECT total_amount::text FROM subscription_invoice WHERE id=$1`, invoiceID).Scan(&total); err != nil {
			t.Fatal(err)
		}
		requestAmount := formatRat(new(big.Rat).Mul(mustFundingRat(total), big.NewRat(3, 5)))
		const workers = 8
		var wg sync.WaitGroup
		results := make(chan error, workers)
		for index := 0; index < workers; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				_, _, createErr := s.CreateRefundApplication(ctx, CreateRefundApplicationRequest{OrganizationID: organizationID,
					SourceType: "SUBSCRIPTION", SubscriptionInvoiceID: invoiceID, Amount: requestAmount,
					Reason: "synthetic concurrent subscription refund", IdempotencyKey: fmt.Sprintf("finclose-sub-refund-%d", index), RequestedBy: requesterID})
				results <- createErr
			}(index)
		}
		wg.Wait()
		close(results)
		succeeded, rejected := 0, 0
		for createErr := range results {
			if createErr == nil {
				succeeded++
			} else if errors.Is(createErr, ErrRefundNotEligible) {
				rejected++
			} else {
				t.Fatal(createErr)
			}
		}
		if succeeded != 1 || rejected != workers-1 {
			t.Fatalf("concurrent subscription refunds succeeded=%d rejected=%d", succeeded, rejected)
		}
		var retainedRefundKey string
		if err = s.pool.QueryRow(ctx, `SELECT idempotency_key FROM refund_application WHERE subscription_invoice_id=$1`, invoiceID).Scan(&retainedRefundKey); err != nil {
			t.Fatal(err)
		}
		if _, _, err = s.CreateRefundApplication(ctx, CreateRefundApplicationRequest{OrganizationID: organizationID,
			SourceType: "SUBSCRIPTION", SubscriptionInvoiceID: id.UUID(), Amount: requestAmount,
			Reason: "synthetic concurrent subscription refund", IdempotencyKey: retainedRefundKey, RequestedBy: requesterID}); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("refund application idempotency accepted changed source: %v", err)
		}
	}

	// Concurrent recharge applications reserve the same still-refundable cash
	// envelope even before payment-provider processing starts.
	recharge := createPendingPaymentFixture(t, ctx, s, organizationID, "finclose-concurrent-refund", "5.000000000000")
	rechargeEvent := payment.VerifiedWebhook{ProviderEventID: "evt_" + id.UUID(), ProviderOrderNo: recharge.ProviderOrderNo,
		PlatformOrderNo: recharge.PlatformOrderNo, EventType: "payment.paid", Status: "PAID", Amount: recharge.Amount,
		Currency: recharge.Currency, ProviderTimestamp: time.Now().UTC(), ReplayKey: "replay_" + id.UUID()}
	recharge, _, err = s.RecordVerifiedPaymentWebhook(ctx, "sandbox", rechargeEvent, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	recharge, _, err = s.CreditPaidRecharge(ctx, recharge.ID)
	if err != nil {
		t.Fatal(err)
	}
	var refundable string
	if err = s.pool.QueryRow(ctx, `SELECT remaining_amount::text FROM wallet_cash_lot WHERE recharge_order_id=$1`, recharge.ID).Scan(&refundable); err == nil {
		requestAmount := formatRat(new(big.Rat).Mul(mustFundingRat(refundable), big.NewRat(3, 5)))
		const workers = 6
		var wg sync.WaitGroup
		results := make(chan error, workers)
		for index := 0; index < workers; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				_, _, createErr := s.CreateRefundApplication(ctx, CreateRefundApplicationRequest{OrganizationID: organizationID,
					SourceType: "RECHARGE", RechargeOrderID: recharge.ID, Amount: requestAmount,
					Reason: "synthetic concurrent recharge refund", IdempotencyKey: fmt.Sprintf("finclose-recharge-refund-%d", index), RequestedBy: requesterID})
				results <- createErr
			}(index)
		}
		wg.Wait()
		close(results)
		succeeded, rejected := 0, 0
		for createErr := range results {
			if createErr == nil {
				succeeded++
			} else if errors.Is(createErr, ErrRefundNotEligible) {
				rejected++
			} else {
				t.Fatal(createErr)
			}
		}
		if succeeded != 1 || rejected != workers-1 {
			t.Fatalf("concurrent recharge refunds succeeded=%d rejected=%d", succeeded, rejected)
		}
	}

	// Invoice export owns the approved set exactly once and persists the state
	// transition together with its audit evidence.
	invoiceApplication, _, err := s.CreateInvoiceApplication(ctx, CreateInvoiceApplicationRequest{OrganizationID: organizationID,
		InvoiceTitle: "Synthetic Finance Close", Amount: "1.000000000000", Currency: paidInvoice.Currency,
		PeriodStart: paidInvoice.PaidAt.UTC().AddDate(0, 0, -1), PeriodEnd: paidInvoice.PaidAt.UTC().AddDate(0, 0, 1), IdempotencyKey: "finclose-invoice-application", RequestedBy: requesterID})
	if err != nil {
		t.Fatal(err)
	}
	invoiceApplication, _, err = s.DecideInvoiceApplication(ctx, invoiceApplication.ID, "APPROVE", "verified synthetic settled source", "finclose-invoice-approval", requesterID)
	if err != nil || invoiceApplication.Status != "APPROVED" {
		t.Fatalf("approved invoice application=%+v err=%v", invoiceApplication, err)
	}
	if _, _, err = s.DecideInvoiceApplication(ctx, invoiceApplication.ID, "REJECT", "changed decision must conflict", "finclose-invoice-approval", requesterID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("invoice decision replay accepted changed payload: %v", err)
	}
	if _, _, err = s.CreateInvoiceApplication(ctx, CreateInvoiceApplicationRequest{OrganizationID: organizationID,
		InvoiceTitle: "Conflicting Synthetic Finance Close", Amount: "1.000000000000", Currency: paidInvoice.Currency,
		PeriodStart: paidInvoice.PaidAt.UTC().AddDate(0, 0, -1), PeriodEnd: paidInvoice.PaidAt.UTC().AddDate(0, 0, 1),
		IdempotencyKey: "finclose-invoice-application", RequestedBy: requesterID}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("invoice application idempotency accepted changed payload: %v", err)
	}
	if _, _, err = s.CreateRefundApplication(ctx, CreateRefundApplicationRequest{OrganizationID: organizationID,
		SourceType: "SUBSCRIPTION", SubscriptionInvoiceID: paidInvoice.ID, Amount: paidInvoice.TotalAmount.String(),
		Reason: "must not refund invoiced subscription evidence", IdempotencyKey: "finclose-invoiced-source-refund", RequestedBy: requesterID}); !errors.Is(err, ErrRefundNotEligible) {
		t.Fatalf("refund application reused invoice-attributed source: %v", err)
	}
	exportBatch, replayed, err := s.ExportInvoiceApplicationBatch(ctx, FinanceFilter{OrganizationID: organizationID, Limit: 100},
		"finclose-invoice-export", requesterID)
	if err != nil || replayed || exportBatch.RowCount != 1 || len(exportBatch.Artifact) == 0 {
		t.Fatalf("export batch=%+v replayed=%v err=%v", exportBatch, replayed, err)
	}
	exportReplay, replayed, err := s.ExportInvoiceApplicationBatch(ctx, FinanceFilter{OrganizationID: organizationID, Limit: 100},
		"finclose-invoice-export", requesterID)
	if err != nil || !replayed || exportReplay.ID != exportBatch.ID || string(exportReplay.Artifact) != string(exportBatch.Artifact) || exportReplay.ArtifactSHA256 != exportBatch.ArtifactSHA256 {
		t.Fatalf("invoice export byte replay=%+v replayed=%v err=%v", exportReplay, replayed, err)
	}
	if _, _, err = s.ExportInvoiceApplicationBatch(ctx, FinanceFilter{Limit: 100}, "finclose-invoice-export", requesterID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("invoice export accepted changed filter: %v", err)
	}
	loadedInvoiceApplication, err := s.InvoiceApplicationByID(ctx, invoiceApplication.ID)
	if err != nil || loadedInvoiceApplication.Status != "EXPORTED" || loadedInvoiceApplication.ExportedAt == nil ||
		loadedInvoiceApplication.ExportBatchID == nil || *loadedInvoiceApplication.ExportBatchID != exportBatch.ID {
		t.Fatalf("exported invoice application=%+v err=%v", loadedInvoiceApplication, err)
	}
	var exportAudits int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='finance.invoice_applications_exported'
		AND resource_type='invoice_export_batch' AND after_state->>'batch_key'='finclose-invoice-export'`).Scan(&exportAudits); err != nil || exportAudits != 1 {
		t.Fatalf("invoice export audits=%d err=%v", exportAudits, err)
	}

	// Bulk CSV readers must not inherit the 200-row UI pagination ceiling.
	projectID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO projects(id,organization_id,name,slug,status) VALUES($1,$2,'Finance Export','finance-export','ACTIVE')`, projectID, organizationID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 205; index++ {
		if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,model,
			input_tokens,cached_input_tokens,output_tokens,amount,currency,status,customer_sale_amount,final_user_amount,created_at)
			VALUES($1,$2,$3,$4,'synthetic-export',1,0,1,0,'USD','WAIVED',0,0,now())`,
			id.UUID(), fmt.Sprintf("finclose-export-%03d", index), organizationID, projectID); err != nil {
			t.Fatal(err)
		}
	}
	usageRows, err := s.ListFinanceUsageExport(ctx, FinanceFilter{OrganizationID: organizationID,
		From: time.Now().UTC().Add(-time.Hour), To: time.Now().UTC().Add(time.Hour), Limit: 100000})
	if err != nil || len(usageRows) != 205 {
		t.Fatalf("bulk finance usage rows=%d err=%v", len(usageRows), err)
	}
	interactiveRows, err := s.ListFinanceUsage(ctx, FinanceFilter{OrganizationID: organizationID,
		From: time.Now().UTC().Add(-time.Hour), To: time.Now().UTC().Add(time.Hour), Limit: 100000})
	if err != nil || len(interactiveRows) != financeInteractiveMaximumRows {
		t.Fatalf("interactive finance usage rows=%d err=%v", len(interactiveRows), err)
	}

	// Provider statements enforce both the Provider's contractual region and
	// the organization billing region of any linked usage evidence.
	var providerID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM providers ORDER BY created_at,id LIMIT 1`).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,contract_status='ACTIVE',allowed_regions='["US","CN"]'::jsonb
		WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE organizations SET billing_region='US' WHERE id=$1`, organizationID); err != nil {
		t.Fatal(err)
	}
	usageDate := time.Now().UTC()
	statementLine := ProviderStatementLineInput{ExternalLineID: "finclose-region-line", RequestID: "finclose-export-000",
		UsageDate: usageDate, Amount: "1.000000000000", Currency: "USD", Metadata: map[string]any{"synthetic": true}}
	if _, _, err = s.ImportProviderStatement(ctx, ProviderStatementInput{ProviderID: providerID,
		StatementReference: "finclose-region-cn", PeriodStart: usageDate.AddDate(0, 0, -1), PeriodEnd: usageDate.AddDate(0, 0, 1),
		Region: "CN", Currency: "USD", TotalAmount: "1.000000000000", SourceSHA256: strings.Repeat("a", 64),
		ImportedBy: requesterID, Lines: []ProviderStatementLineInput{statementLine}}); !errors.Is(err, ErrProviderNotContracted) {
		t.Fatalf("cross-region Provider statement was accepted: %v", err)
	}
	statementID, replayed, err := s.ImportProviderStatement(ctx, ProviderStatementInput{ProviderID: providerID,
		StatementReference: "finclose-region-us", PeriodStart: usageDate.AddDate(0, 0, -1), PeriodEnd: usageDate.AddDate(0, 0, 1),
		Region: "US", Currency: "USD", TotalAmount: "1.000000000000", SourceSHA256: strings.Repeat("b", 64),
		ImportedBy: requesterID, Lines: []ProviderStatementLineInput{statementLine}})
	if err != nil || replayed {
		t.Fatalf("allowed-region Provider statement was rejected: %v", err)
	}
	replayedID, replayed, err := s.ImportProviderStatement(ctx, ProviderStatementInput{ProviderID: providerID,
		StatementReference: "finclose-region-us", PeriodStart: usageDate.AddDate(0, 0, -1), PeriodEnd: usageDate.AddDate(0, 0, 1),
		Region: "US", Currency: "USD", TotalAmount: "1.000000000000", SourceSHA256: strings.Repeat("b", 64),
		ImportedBy: requesterID, Lines: []ProviderStatementLineInput{statementLine}})
	if err != nil || !replayed || replayedID != statementID {
		t.Fatalf("Provider statement exact replay id=%s replayed=%v err=%v", replayedID, replayed, err)
	}
	changedLine := statementLine
	changedLine.Metadata = map[string]any{"synthetic": true, "changed": true}
	if _, _, err = s.ImportProviderStatement(ctx, ProviderStatementInput{ProviderID: providerID,
		StatementReference: "finclose-region-us", PeriodStart: usageDate.AddDate(0, 0, -1), PeriodEnd: usageDate.AddDate(0, 0, 1),
		Region: "US", Currency: "USD", TotalAmount: "1.000000000000", SourceSHA256: strings.Repeat("b", 64),
		ImportedBy: requesterID, Lines: []ProviderStatementLineInput{changedLine}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("Provider statement accepted changed replay payload: %v", err)
	}
	var statementJournals, statementEntries int
	if err = s.pool.QueryRow(ctx, `SELECT count(*),(SELECT count(*) FROM ledger_journal_entry entry JOIN ledger_journal journal ON journal.id=entry.journal_id WHERE journal.provider_statement_id=$1)
		FROM ledger_journal WHERE provider_statement_id=$1 AND journal_type='PROVIDER_STATEMENT' AND status='POSTED'`, statementID).
		Scan(&statementJournals, &statementEntries); err != nil || statementJournals != 1 || statementEntries != 2 {
		t.Fatalf("Provider statement journals=%d entries=%d err=%v", statementJournals, statementEntries, err)
	}
	globalAccounting, err := s.ListAccountingRows(ctx, FinanceFilter{From: usageDate.Add(-time.Hour), To: usageDate.Add(time.Hour), Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	statementAccountingRows := 0
	for _, row := range globalAccounting {
		if row.JournalType == "PROVIDER_STATEMENT" {
			statementAccountingRows++
		}
	}
	if statementAccountingRows != 2 {
		t.Fatalf("global accounting export Provider statement rows=%d", statementAccountingRows)
	}
	providerReport, err := s.FinanceReport(ctx, "provider_cost", usageDate.AddDate(0, 0, -2), usageDate.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	foundStatementReport := false
	for _, row := range providerReport {
		if row["source"] == "provider_statement" && row["amount"] == "1.000000000000" {
			foundStatementReport = true
			break
		}
	}
	if !foundStatementReport {
		t.Fatalf("Provider cost report omitted imported statement: %+v", providerReport)
	}
	revenueReport, err := s.FinanceReport(ctx, "user_revenue", usageDate.AddDate(0, 0, -2), usageDate.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	foundSubscriptionRevenue := false
	for _, row := range revenueReport {
		if row["provider"] == "Subscription" && row["source"] == "posted_ledger" && row["amount"] == paidInvoice.TotalAmount.String() {
			foundSubscriptionRevenue = true
			break
		}
	}
	if !foundSubscriptionRevenue {
		t.Fatalf("user revenue report omitted posted subscription income: %+v", revenueReport)
	}

	// A cross-currency Provider cost is converted only with the immutable rate
	// captured for the exact request. This fixture has a USD 1 Provider fixed
	// cost, a CNY customer currency, and a frozen USD->CNY rate of 7. It is
	// intentionally customer-waived so the report must expose a cost-only CNY
	// margin row without requiring a wallet in the retail currency.
	var crossProviderID, crossModelID string
	if err = s.pool.QueryRow(ctx, `SELECT provider_id,model_id FROM model_price_version WHERE id=$1`, pricingVersionID).
		Scan(&crossProviderID, &crossModelID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,contract_status='ACTIVE',allowed_regions='["US"]'::jsonb
		WHERE id=$1`, crossProviderID); err != nil {
		t.Fatal(err)
	}
	crossVersionID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO model_price_version(id,provider_id,model_id,organization_id,version,
		provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,provider_request_fixed_cost,
		retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,retail_request_fixed_price,
		provider_currency,retail_currency,provider_unit,retail_unit,effective_at,source,approval_status)
		VALUES($1,$2,$3,$4,nextval('model_price_version_sequence'),0,0,0,1,0,0,0,0,'USD','CNY',1,1,$5,
		'financial-close-cross-currency','APPROVED')`, crossVersionID, crossProviderID, crossModelID, organizationID,
		usageDate.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	crossRequestID := "finclose-cross-currency-" + id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO request_logs(id,request_id,organization_id,project_id,provider_id,
		requested_model,resolved_model,endpoint,status_code,pricing_version_id,usage_source,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$6,'/v1/responses',200,$7,'PROVIDER_REPORTED',$8)`, id.UUID(), crossRequestID,
		organizationID, fundingProjectID, crossProviderID, crossModelID, crossVersionID, usageDate); err != nil {
		t.Fatal(err)
	}
	crossSnapshotID := id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO usage_price_snapshot(id,request_id,pricing_version_id,provider_id,model_id,
		input_tokens,cached_input_tokens,output_tokens,provider_input_token_cost,provider_cached_input_token_cost,
		provider_output_token_cost,provider_request_fixed_cost,retail_input_token_price,retail_cached_input_token_price,
		retail_output_token_price,retail_request_fixed_price,provider_unit,retail_unit,provider_cost_amount,provider_currency,
		customer_sale_amount,customer_currency,exchange_rate,platform_gross_margin,promotion_amount,pre_tax_amount,tax_rate,
		tax_amount,final_user_amount,pricing_rule_version,settled_at)
		VALUES($1,$2,$3,$4,$5,0,0,0,0,0,0,1,0,0,0,0,1,1,1,'USD',0,'CNY',7,-7,0,0,0,0,0,$6,$7)`,
		crossSnapshotID, crossRequestID, crossVersionID, crossProviderID, crossModelID, "cross-currency:1", usageDate); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,provider_id,
		model,input_tokens,cached_input_tokens,output_tokens,amount,currency,status,usage_price_snapshot_id,
		provider_cost_amount,customer_sale_amount,promotion_amount,tax_amount,final_user_amount,pricing_rule_version,created_at)
		VALUES($1,$2,$3,$4,$5,$6,0,0,0,0,'CNY','WAIVED',$7,1,0,0,0,0,$8,$9)`, id.UUID(), crossRequestID,
		organizationID, fundingProjectID, crossProviderID, crossModelID, crossSnapshotID, "cross-currency:1", usageDate); err != nil {
		t.Fatal(err)
	}
	crossLine := ProviderStatementLineInput{ExternalLineID: "finclose-cross-currency-line", RequestID: crossRequestID,
		UsageDate: usageDate, Amount: "1.000000000000", Currency: "USD", Metadata: map[string]any{"synthetic": true}}
	if _, replayed, err = s.ImportProviderStatement(ctx, ProviderStatementInput{ProviderID: crossProviderID,
		StatementReference: "finclose-cross-currency", PeriodStart: usageDate.AddDate(0, 0, -1), PeriodEnd: usageDate.AddDate(0, 0, 1),
		Region: "US", Currency: "USD", TotalAmount: "1.000000000000", SourceSHA256: strings.Repeat("c", 64),
		ImportedBy: requesterID, Lines: []ProviderStatementLineInput{crossLine}}); err != nil || replayed {
		t.Fatalf("cross-currency Provider statement replayed=%v err=%v", replayed, err)
	}
	marginReport, err := s.FinanceReport(ctx, "gross_margin", usageDate.AddDate(0, 0, -2), usageDate.AddDate(0, 0, 2))
	if err != nil {
		t.Fatal(err)
	}
	foundUnallocatedCost := false
	foundConvertedMargin := false
	for _, row := range marginReport {
		if row["row_type"] == "unallocated_cost" && row["currency"] == "USD" &&
			row["unallocated_reason"] == "USAGE_NOT_FOUND" && decimalEqual(fmt.Sprint(row["amount"]), "1") {
			foundUnallocatedCost = true
		}
		if row["row_type"] == "gross_margin" && row["currency"] == "CNY" &&
			row["provider"] != "Subscription" && decimalEqual(fmt.Sprint(row["amount"]), "-7") {
			foundConvertedMargin = true
		}
	}
	if !foundUnallocatedCost || !foundConvertedMargin {
		t.Fatalf("gross margin allocation unallocated=%v converted=%v rows=%+v", foundUnallocatedCost, foundConvertedMargin, marginReport)
	}
}

func TestFinancialReconciliationFailureIsDurable(t *testing.T) {
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

	original := reconciliationChecks
	reconciliationChecks = []struct{ checkType, query string }{{"PAYMENT_TO_RECHARGE", `SELECT missing_column FROM recharge_order WHERE created_at>=($1::date::timestamp AT TIME ZONE 'UTC')`}}
	t.Cleanup(func() { reconciliationChecks = original })
	businessDate := time.Now().UTC().AddDate(0, 0, -8)
	run, replayed, err := s.RunFinancialReconciliation(ctx, businessDate, "TEST", nil)
	if err == nil || replayed || run.ID == "" {
		t.Fatalf("run=%+v replayed=%v err=%v", run, replayed, err)
	}
	var status, errorCode string
	var completedAt *time.Time
	if err = s.pool.QueryRow(ctx, `SELECT status,COALESCE(error_code,''),completed_at FROM financial_reconciliation_run WHERE id=$1`, run.ID).
		Scan(&status, &errorCode, &completedAt); err != nil {
		t.Fatal(err)
	}
	if status != "FAILED" || errorCode != "check_failed:payment_to_recharge" || completedAt == nil {
		t.Fatalf("status=%s error_code=%s completed_at=%v", status, errorCode, completedAt)
	}
	reconciliationChecks = original
	loaded, replayed, err := s.RunFinancialReconciliation(ctx, businessDate, "TEST", nil)
	if err != nil || replayed || loaded.ID != run.ID || loaded.Status != "COMPLETED" {
		t.Fatalf("failed retry=%+v replayed=%v err=%v", loaded, replayed, err)
	}
	var previousAttempts int
	if err = s.pool.QueryRow(ctx, `SELECT jsonb_array_length(COALESCE(summary->'previous_attempts','[]'::jsonb))
		FROM financial_reconciliation_run WHERE id=$1`, run.ID).Scan(&previousAttempts); err != nil || previousAttempts != 1 {
		t.Fatalf("durable failed attempt history=%d err=%v", previousAttempts, err)
	}
}

func TestReconciliationUTCStatementReverseScanAndSourceReversalGuard(t *testing.T) {
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

	var organizationID, walletID, projectID, providerID, requesterID string
	if err = s.pool.QueryRow(ctx, `SELECT organization.id,wallet.id,project.id,provider.id,user_account.id
		FROM organizations organization JOIN wallets wallet ON wallet.organization_id=organization.id
		JOIN projects project ON project.organization_id=organization.id
		CROSS JOIN LATERAL(SELECT id FROM providers ORDER BY created_at,id LIMIT 1) provider
		CROSS JOIN LATERAL(SELECT id FROM users ORDER BY created_at,id LIMIT 1) user_account
		ORDER BY organization.created_at,organization.id LIMIT 1`).
		Scan(&organizationID, &walletID, &projectID, &providerID, &requesterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE providers SET enabled=true,contract_status='ACTIVE',allowed_regions='["US"]'::jsonb WHERE id=$1`, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE organizations SET billing_region='US' WHERE id=$1`, organizationID); err != nil {
		t.Fatal(err)
	}

	utcDate := time.Now().UTC().AddDate(0, 0, -6)
	utcStart := time.Date(utcDate.Year(), utcDate.Month(), utcDate.Day(), 0, 0, 0, 0, time.UTC)
	utcRequestID := "finclose-utc-" + id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,provider_id,model,
		input_tokens,cached_input_tokens,output_tokens,amount,currency,status,provider_cost_amount,customer_sale_amount,final_user_amount,created_at)
		VALUES($1,$2,$3,$4,$5,'recon-utc',1,0,1,1,'USD','CHARGED',1,1,1,$6)`, id.UUID(), utcRequestID,
		organizationID, projectID, providerID, utcStart.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `SET TIME ZONE 'America/Los_Angeles'`); err != nil {
		t.Fatal(err)
	}
	defer s.pool.Exec(ctx, `SET TIME ZONE 'UTC'`) //nolint:errcheck
	var utcObservation string
	if err = s.pool.QueryRow(ctx, `SELECT observation_key FROM (`+usageToUserChargeSQL+`) AS candidate(`+reconciliationCandidateColumns+`)
		WHERE observation_key=$2`,
		utcStart.Format("2006-01-02"), utcRequestID).Scan(&utcObservation); err != nil || utcObservation != utcRequestID {
		t.Fatalf("UTC boundary usage observation=%s err=%v", utcObservation, err)
	}

	lineID := id.UUID()
	statementID := id.UUID()
	orphanRequestID := "provider-orphan-" + id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO provider_statement(id,provider_id,statement_reference,period_start,period_end,region,currency,total_amount,source_sha256,imported_by)
		VALUES($1,$2,$3,$4,$4,'US','USD',1,$5,$6)`, statementID, providerID, "orphan:"+statementID,
		utcStart.Format("2006-01-02"), strings.Repeat("d", 64), requesterID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO provider_statement_line(id,provider_statement_id,external_line_id,request_id,usage_date,amount,currency)
		VALUES($1,$2,$3,$4,$5,1,'USD')`, lineID, statementID, "orphan-line:"+lineID, orphanRequestID,
		utcStart.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	var classification string
	if err = s.pool.QueryRow(ctx, `SELECT classification FROM (`+providerUsageToBillSQL+`) candidate WHERE observation_key=$2`,
		utcStart.Format("2006-01-02"), "statement-line:"+lineID).Scan(&classification); err != nil || classification != "PROVIDER_STATEMENT_LINE_WITHOUT_USAGE" {
		t.Fatalf("orphan statement classification=%s err=%v", classification, err)
	}

	// A waived customer charge cannot waive Provider traceability. If the
	// request reached an upstream provider, both attempt and request-log
	// evidence (including their shared upstream request ID) are required.
	waivedOperationID := id.UUID()
	waivedRequestID := "finclose-waived-upstream-" + id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO funding_operation(id,wallet_id,organization_id,project_id,request_id,idempotency_key,
		request_fingerprint,status,currency,maximum_amount,estimated_input_tokens,max_output_tokens,usage_source)
		VALUES($1,$2,$3,$4,$5,$5,$5,'RELEASED','USD',0,0,0,'PROVIDER_REPORTED')`, waivedOperationID, walletID,
		organizationID, projectID, waivedRequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,provider_id,model,
		input_tokens,cached_input_tokens,output_tokens,amount,currency,status,provider_cost_amount,customer_sale_amount,final_user_amount,
		funding_operation_id,created_at) VALUES($1,$2,$3,$4,$5,'waived-upstream',1,0,1,0,'USD','WAIVED',1,0,0,$6,$7)`,
		id.UUID(), waivedRequestID, organizationID, projectID, providerID, waivedOperationID, utcStart.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	classification = ""
	if err = s.pool.QueryRow(ctx, `SELECT candidate.classification FROM (`+usageToProviderUsageSQL+`) AS candidate(`+reconciliationCandidateColumns+`) WHERE candidate.observation_key=$2`,
		utcStart.Format("2006-01-02"), waivedRequestID).Scan(&classification); err != nil || classification != "REQUEST_LOG_MISSING" {
		t.Fatalf("waived upstream trace classification=%s err=%v", classification, err)
	}
	// An explicit pre-upstream failure may omit request/attempt evidence only
	// when it also carries zero Provider cost. Cost evidence can never be
	// silently hidden behind NO_PROVIDER_USAGE.
	if _, err = s.pool.Exec(ctx, `UPDATE funding_operation SET usage_source='NO_PROVIDER_USAGE' WHERE id=$1`, waivedOperationID); err != nil {
		t.Fatal(err)
	}
	classification = ""
	if err = s.pool.QueryRow(ctx, `SELECT candidate.classification FROM (`+usageToProviderUsageSQL+`) AS candidate(`+reconciliationCandidateColumns+`) WHERE candidate.observation_key=$2`,
		utcStart.Format("2006-01-02"), waivedRequestID).Scan(&classification); err != nil || classification != "PROVIDER_COST_WITHOUT_USAGE" {
		t.Fatalf("no-provider usage with cost classification=%s err=%v", classification, err)
	}

	if _, err = s.CreateWalletTransaction(ctx, domain.WalletTransaction{WalletID: walletID, TransactionType: "ADJUSTMENT",
		Amount: domain.Decimal("1"), IdempotencyKey: "recon-source-guard:" + id.UUID(), Reference: "source reversal guard"}); err != nil {
		t.Fatal(err)
	}
	var sourceJournalID string
	if err = s.pool.QueryRow(ctx, `SELECT journal_id FROM wallet_transactions WHERE wallet_id=$1 AND reference='source reversal guard'
		ORDER BY created_at DESC,id DESC LIMIT 1`, walletID).Scan(&sourceJournalID); err != nil {
		t.Fatal(err)
	}
	runID := id.UUID()
	caseOneID, caseTwoID := id.UUID(), id.UUID()
	if _, err = s.pool.Exec(ctx, `INSERT INTO financial_reconciliation_run(id,run_key,business_date,status,trigger_source,completed_at)
		VALUES($1,$2,$3,'COMPLETED','TEST',now())`, runID, "test:source-reversal:"+runID, utcStart.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO financial_reconciliation_case(id,case_key,check_type,classification,severity,
		organization_id,first_seen_run_id,last_seen_run_id) VALUES
		($1,$3,'USAGE_TO_USER_CHARGE','SYNTHETIC_SOURCE_GUARD','HIGH',$5,$6,$6),
		($2,$4,'USAGE_TO_USER_CHARGE','SYNTHETIC_SOURCE_GUARD','HIGH',$5,$6,$6)`, caseOneID, caseTwoID,
		"test:source-one:"+caseOneID, "test:source-two:"+caseTwoID, organizationID, runID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ResolveReconciliationCase(ctx, caseOneID, "REVERSE_JOURNAL", sourceJournalID,
		"first verified source reversal", "recon-source-one", requesterID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ResolveReconciliationCase(ctx, caseTwoID, "REVERSE_JOURNAL", sourceJournalID,
		"duplicate source reversal must fail", "recon-source-two", requesterID); !errors.Is(err, ErrFinanceState) {
		t.Fatalf("same source journal reversed twice: %v", err)
	}
}
