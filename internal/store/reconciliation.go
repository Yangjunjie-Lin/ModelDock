package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

type reconciliationCandidate struct {
	observationKey        string
	matched               bool
	classification        string
	severity              string
	organizationID        *string
	providerID            *string
	rechargeOrderID       *string
	fundingOperationID    *string
	subscriptionInvoiceID *string
	expectedAmount        *string
	actualAmount          *string
	currency              *string
	details               map[string]any
}

var reconciliationChecks = []struct{ checkType, query string }{
	{"PAYMENT_TO_RECHARGE", paymentToRechargeSQL},
	{"RECHARGE_TO_WALLET", rechargeToWalletSQL},
	{"USAGE_TO_USER_CHARGE", usageToUserChargeSQL},
	{"USAGE_TO_PROVIDER_USAGE", usageToProviderUsageSQL},
	{"PROVIDER_USAGE_TO_BILL", providerUsageToBillSQL},
	{"SUBSCRIPTION_TO_STATE", subscriptionToStateSQL},
	{"SUPPLIER_PAYABLE_TO_USAGE", supplierPayableToUsageSQL},
	{"SUPPLIER_BILL_TO_PAYABLE", supplierBillToPayableSQL},
	{"SUPPLIER_PAYOUT_TO_LEDGER", supplierPayoutToLedgerSQL},
}

const reconciliationCandidateColumns = `observation_key,matched,classification,severity,organization_id,provider_id,
	recharge_order_id,funding_operation_id,subscription_invoice_id,expected_amount,actual_amount,currency,details`

func (s *Store) RunFinancialReconciliation(ctx context.Context, businessDate time.Time, triggerSource string, actor *string) (domain.ReconciliationRun, bool, error) {
	date := businessDate.UTC().Format("2006-01-02")
	triggerSource = strings.ToUpper(strings.TrimSpace(triggerSource))
	if triggerSource != "SCHEDULED" && triggerSource != "MANUAL" && triggerSource != "TEST" {
		return domain.ReconciliationRun{}, false, errors.New("invalid reconciliation trigger")
	}
	runKey := "daily:" + date
	runID := id.UUID()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReconciliationRun{}, false, err
	}
	defer tx.Rollback(ctx)
	var run domain.ReconciliationRun
	err = tx.QueryRow(ctx, `INSERT INTO financial_reconciliation_run(id,run_key,business_date,trigger_source,started_by)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(run_key) DO NOTHING
		RETURNING id,run_key,business_date::text,status,trigger_source,summary,started_by,started_at,completed_at,COALESCE(error_code,'')`,
		runID, runKey, date, triggerSource, actor).Scan(&run.ID, &run.RunKey, &run.BusinessDate, &run.Status, &run.TriggerSource,
		&run.Summary, &run.StartedBy, &run.StartedAt, &run.CompletedAt, &run.ErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = scanReconciliationRun(tx.QueryRow(ctx, reconciliationRunSelect+` WHERE run_key=$1`, runKey), &run); err != nil {
			return run, false, err
		}
		if run.Status == "COMPLETED" || run.Status == "RUNNING" {
			return run, true, tx.Commit(ctx)
		}
		// A FAILED row is durable evidence of its prior attempt, but it must not
		// permanently poison the business date. Its history is appended to the
		// summary before the same serialized daily run is retried.
		err = tx.QueryRow(ctx, `UPDATE financial_reconciliation_run SET status='RUNNING',completed_at=NULL,error_code=NULL,
			summary=jsonb_build_object('previous_attempts',COALESCE(summary->'previous_attempts','[]'::jsonb)
				|| jsonb_build_array(jsonb_build_object('status','FAILED','completed_at',completed_at,'error_code',error_code,'summary',summary))),
			trigger_source=$2,started_by=$3,started_at=now()
			WHERE id=$1 AND status='FAILED'
			RETURNING id,run_key,business_date::text,status,trigger_source,summary,started_by,started_at,completed_at,COALESCE(error_code,'')`,
			run.ID, triggerSource, actor).Scan(&run.ID, &run.RunKey, &run.BusinessDate, &run.Status, &run.TriggerSource,
			&run.Summary, &run.StartedBy, &run.StartedAt, &run.CompletedAt, &run.ErrorCode)
		if errors.Is(err, pgx.ErrNoRows) {
			if err = scanReconciliationRun(tx.QueryRow(ctx, reconciliationRunSelect+` WHERE run_key=$1`, runKey), &run); err != nil {
				return run, false, err
			}
			return run, true, tx.Commit(ctx)
		}
		if err != nil {
			return run, false, err
		}
	}
	if err != nil {
		return run, false, err
	}
	// The run row is the durable failure boundary.  Commit it before starting
	// the snapshot transaction so a failed check cannot roll its own RUNNING
	// row back and make the later FAILED update a no-op.
	if err = tx.Commit(ctx); err != nil {
		return run, false, err
	}
	tx, err = s.pool.Begin(ctx)
	if err != nil {
		_ = s.failFinancialReconciliationRunDetached(run.ID, "snapshot_begin_failed", "")
		return run, false, err
	}
	defer tx.Rollback(ctx)

	summary := map[string]any{"business_date": date, "matched": 0, "differences": 0, "by_check": map[string]any{}}
	if previous, ok := run.Summary["previous_attempts"]; ok {
		summary["previous_attempts"] = previous
	}
	for _, item := range reconciliationChecks {
		matched, differences, processErr := s.processReconciliationQuery(ctx, tx, run.ID, item.checkType, item.query, date)
		if processErr != nil {
			_ = tx.Rollback(ctx)
			_ = s.failFinancialReconciliationRunDetached(run.ID, "check_failed:"+strings.ToLower(item.checkType), item.checkType)
			return run, false, processErr
		}
		summary["matched"] = summary["matched"].(int) + matched
		summary["differences"] = summary["differences"].(int) + differences
		summary["by_check"].(map[string]any)[item.checkType] = map[string]any{"matched": matched, "differences": differences}
	}
	err = tx.QueryRow(ctx, `UPDATE financial_reconciliation_run SET status='COMPLETED',completed_at=now(),summary=$2
		WHERE id=$1 AND status='RUNNING' RETURNING completed_at`, run.ID, jsonBytes(summary)).Scan(&run.CompletedAt)
	if err != nil {
		_ = tx.Rollback(ctx)
		_ = s.failFinancialReconciliationRunDetached(run.ID, "run_completion_failed", "")
		return run, false, err
	}
	run.Status = "COMPLETED"
	run.Summary = summary
	if err = writeAuditTx(ctx, tx, actor, "finance.reconciliation_completed", "financial_reconciliation_run", run.ID, summary); err != nil {
		_ = tx.Rollback(ctx)
		_ = s.failFinancialReconciliationRunDetached(run.ID, "completion_audit_failed", "")
		return run, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		_ = s.failFinancialReconciliationRunDetached(run.ID, "completion_commit_failed", "")
		return run, false, err
	}
	return run, false, nil
}

func (s *Store) failFinancialReconciliationRun(ctx context.Context, runID, errorCode, failedCheck string) error {
	summary := map[string]any{"failed": true}
	if failedCheck != "" {
		summary["failed_check"] = failedCheck
	}
	tag, err := s.pool.Exec(ctx, `UPDATE financial_reconciliation_run SET status='FAILED',completed_at=now(),
		error_code=$2,summary=CASE WHEN summary?'previous_attempts'
			THEN $3::jsonb||jsonb_build_object('previous_attempts',summary->'previous_attempts') ELSE $3::jsonb END
		WHERE id=$1 AND status='RUNNING'`, runID, errorCode, jsonBytes(summary))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrFinanceState
	}
	return nil
}

func (s *Store) failFinancialReconciliationRunDetached(runID, errorCode, failedCheck string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.failFinancialReconciliationRun(ctx, runID, errorCode, failedCheck)
}

func (s *Store) processReconciliationQuery(ctx context.Context, tx pgx.Tx, runID, checkType, query, date string) (int, int, error) {
	rows, err := tx.Query(ctx, query, date)
	if err != nil {
		return 0, 0, err
	}
	candidates := make([]reconciliationCandidate, 0)
	for rows.Next() {
		var v reconciliationCandidate
		var details []byte
		if err = rows.Scan(&v.observationKey, &v.matched, &v.classification, &v.severity, &v.organizationID, &v.providerID, &v.rechargeOrderID, &v.fundingOperationID, &v.subscriptionInvoiceID, &v.expectedAmount, &v.actualAmount, &v.currency, &details); err != nil {
			rows.Close()
			return 0, 0, err
		}
		_ = json.Unmarshal(details, &v.details)
		candidates = append(candidates, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()

	matchedCount, differenceCount := 0, 0
	for _, v := range candidates {
		caseKey := checkType + ":" + v.observationKey
		if v.matched {
			matchedCount++
			// A later match is evidence, not an operator decision. Keep existing
			// differences in the queue until a named handler accepts or reverses
			// them; this prevents transient mismatches from being silently closed.
			_, err = tx.Exec(ctx, `UPDATE financial_reconciliation_case SET last_seen_run_id=$2,updated_at=now()
			WHERE case_key=$1 AND status IN ('OPEN','IN_REVIEW')`, caseKey, runID)
			if err != nil {
				return 0, 0, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO financial_reconciliation_observation(id,run_id,observation_key,check_type,result,details) VALUES($1,$2,$3,$4,'MATCHED',$5) ON CONFLICT(run_id,observation_key) DO NOTHING`, id.UUID(), runID, checkType+":"+v.observationKey, checkType, jsonBytes(v.details))
			if err != nil {
				return 0, 0, err
			}
			continue
		}
		differenceCount++
		var caseID string
		err = tx.QueryRow(ctx, `INSERT INTO financial_reconciliation_case(id,case_key,check_type,classification,severity,status,organization_id,provider_id,recharge_order_id,funding_operation_id,subscription_invoice_id,expected_amount,actual_amount,currency,details,first_seen_run_id,last_seen_run_id)
			VALUES($1,$2,$3,$4,$5,'OPEN',$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)
			ON CONFLICT(case_key) DO UPDATE SET classification=EXCLUDED.classification,severity=EXCLUDED.severity,
			last_seen_run_id=EXCLUDED.last_seen_run_id,occurrence_count=financial_reconciliation_case.occurrence_count+1,
			expected_amount=EXCLUDED.expected_amount,actual_amount=EXCLUDED.actual_amount,currency=EXCLUDED.currency,
			details=EXCLUDED.details,status=CASE WHEN financial_reconciliation_case.status='RESOLVED' THEN 'OPEN' ELSE financial_reconciliation_case.status END,updated_at=now()
			RETURNING id`, id.UUID(), caseKey, checkType, v.classification, v.severity, v.organizationID, v.providerID, v.rechargeOrderID, v.fundingOperationID, v.subscriptionInvoiceID, v.expectedAmount, v.actualAmount, v.currency, jsonBytes(v.details), runID).Scan(&caseID)
		if err != nil {
			return 0, 0, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO financial_reconciliation_observation(id,run_id,observation_key,check_type,result,case_id,details) VALUES($1,$2,$3,$4,'MISMATCH',$5,$6) ON CONFLICT(run_id,observation_key) DO NOTHING`, id.UUID(), runID, checkType+":"+v.observationKey, checkType, caseID, jsonBytes(v.details))
		if err != nil {
			return 0, 0, err
		}
	}
	return matchedCount, differenceCount, nil
}

const reconciliationRunSelect = `SELECT id,run_key,business_date::text,status,trigger_source,summary,started_by,started_at,completed_at,COALESCE(error_code,'') FROM financial_reconciliation_run`

func scanReconciliationRun(row pgx.Row, out *domain.ReconciliationRun) error {
	return row.Scan(&out.ID, &out.RunKey, &out.BusinessDate, &out.Status, &out.TriggerSource, &out.Summary, &out.StartedBy, &out.StartedAt, &out.CompletedAt, &out.ErrorCode)
}
func (s *Store) ListReconciliationRuns(ctx context.Context, limit, offset int) ([]domain.ReconciliationRun, error) {
	rows, err := s.pool.Query(ctx, reconciliationRunSelect+` ORDER BY business_date DESC,started_at DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReconciliationRun{}
	for rows.Next() {
		var v domain.ReconciliationRun
		if err = scanReconciliationRun(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const reconciliationCaseSelect = `SELECT id,case_key,check_type,classification,severity,status,organization_id,provider_id,recharge_order_id,funding_operation_id,subscription_invoice_id,expected_amount::text,actual_amount::text,COALESCE(currency,''),details,occurrence_count,handled_by,COALESCE(handling_reason,''),handled_at,created_at,updated_at FROM financial_reconciliation_case`

func scanReconciliationCase(row pgx.Row) (domain.ReconciliationCase, error) {
	var v domain.ReconciliationCase
	err := row.Scan(&v.ID, &v.CaseKey, &v.CheckType, &v.Classification, &v.Severity, &v.Status, &v.OrganizationID, &v.ProviderID, &v.RechargeOrderID, &v.FundingOperationID, &v.SubscriptionInvoiceID, &v.ExpectedAmount, &v.ActualAmount, &v.Currency, &v.Details, &v.OccurrenceCount, &v.HandledBy, &v.HandlingReason, &v.HandledAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *Store) ListReconciliationCases(ctx context.Context, status, checkType string, limit, offset int) ([]domain.ReconciliationCase, error) {
	rows, err := s.pool.Query(ctx, reconciliationCaseSelect+` WHERE ($1='' OR status=$1) AND ($2='' OR check_type=$2) ORDER BY CASE severity WHEN 'CRITICAL' THEN 1 WHEN 'HIGH' THEN 2 WHEN 'MEDIUM' THEN 3 ELSE 4 END,created_at,id LIMIT $3 OFFSET $4`, strings.ToUpper(status), strings.ToUpper(checkType), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ReconciliationCase{}
	for rows.Next() {
		v, e := scanReconciliationCase(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ResolveReconciliationCase(ctx context.Context, caseID, action, sourceJournalID, reason, idempotencyKey, actor string) (domain.ReconciliationCase, bool, error) {
	action = strings.ToUpper(strings.TrimSpace(action))
	reason = strings.TrimSpace(reason)
	if action != "ACCEPT_EXCEPTION" && action != "REVERSE_JOURNAL" || reason == "" || idempotencyKey == "" || actor == "" {
		return domain.ReconciliationCase{}, false, ErrFinanceState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ReconciliationCase{}, false, err
	}
	defer tx.Rollback(ctx)
	caseValue, err := scanReconciliationCase(tx.QueryRow(ctx, reconciliationCaseSelect+` WHERE id=$1 FOR UPDATE`, caseID))
	if err != nil {
		return caseValue, false, err
	}
	if action == "ACCEPT_EXCEPTION" && strings.TrimSpace(sourceJournalID) != "" ||
		action == "REVERSE_JOURNAL" && strings.TrimSpace(sourceJournalID) == "" {
		return caseValue, false, ErrFinanceState
	}
	var existingAction string
	var existingSourceJournalID *string
	var existingReason string
	err = tx.QueryRow(ctx, `SELECT action,source_journal_id,reason FROM financial_reconciliation_resolution
		WHERE reconciliation_case_id=$1 AND idempotency_key=$2`, caseID, idempotencyKey).Scan(&existingAction, &existingSourceJournalID, &existingReason)
	if err == nil {
		if existingAction != action || derefFinanceID(existingSourceJournalID) != sourceJournalID || existingReason != reason {
			return caseValue, false, ErrIdempotencyConflict
		}
		return caseValue, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return caseValue, false, err
	}
	if caseValue.Status != "OPEN" && caseValue.Status != "IN_REVIEW" {
		return caseValue, false, ErrFinanceState
	}
	resolutionID := id.UUID()
	var reversalID any
	if action == "REVERSE_JOURNAL" {
		journalID, e := s.reverseJournalForCase(ctx, tx, caseValue, sourceJournalID, idempotencyKey, actor, reason)
		if e != nil {
			return caseValue, false, e
		}
		reversalID = journalID
		_, err = tx.Exec(ctx, `INSERT INTO financial_reconciliation_resolution(id,reconciliation_case_id,action,source_journal_id,reversal_journal_id,reason,idempotency_key,resolved_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, resolutionID, caseID, action, sourceJournalID, reversalID, reason, idempotencyKey, actor)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO financial_reconciliation_resolution(id,reconciliation_case_id,action,reason,idempotency_key,resolved_by) VALUES($1,$2,$3,$4,$5,$6)`, resolutionID, caseID, action, reason, idempotencyKey, actor)
	}
	if err != nil {
		return caseValue, false, err
	}
	status := "RESOLVED"
	if action == "ACCEPT_EXCEPTION" {
		status = "ACCEPTED"
	}
	_, err = tx.Exec(ctx, `UPDATE financial_reconciliation_case SET status=$2,handled_by=$3,handling_reason=$4,handled_at=now(),updated_at=now() WHERE id=$1`, caseID, status, actor, reason)
	if err != nil {
		return caseValue, false, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "finance.reconciliation_case_"+strings.ToLower(action), "financial_reconciliation_case", caseID, map[string]any{"reason": reason, "source_journal_id": sourceJournalID, "reversal_journal_id": reversalID}); err != nil {
		return caseValue, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return caseValue, false, err
	}
	loaded, err := scanReconciliationCase(s.pool.QueryRow(ctx, reconciliationCaseSelect+` WHERE id=$1`, caseID))
	return loaded, false, err
}

func (s *Store) reverseJournalForCase(ctx context.Context, tx pgx.Tx, caseValue domain.ReconciliationCase, sourceJournalID, idempotencyKey, actor, reason string) (string, error) {
	var walletID *string
	var currency, status string
	err := tx.QueryRow(ctx, `SELECT wallet_id,currency,status FROM ledger_journal WHERE id=$1 FOR UPDATE`, sourceJournalID).Scan(&walletID, &currency, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "POSTED" {
		return "", ErrFinanceState
	}
	var priorReversalID string
	err = tx.QueryRow(ctx, `SELECT id FROM ledger_journal WHERE reversal_of_journal_id=$1 LIMIT 1`, sourceJournalID).Scan(&priorReversalID)
	if err == nil {
		return "", ErrFinanceState
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if caseValue.OrganizationID != nil && walletID != nil {
		var journalOrganizationID string
		if err = tx.QueryRow(ctx, `SELECT organization_id FROM wallets WHERE id=$1`, *walletID).Scan(&journalOrganizationID); err != nil {
			return "", ErrFinanceState
		}
		if journalOrganizationID != *caseValue.OrganizationID {
			return "", ErrFinanceState
		}
	}
	if caseValue.OrganizationID != nil && walletID == nil && caseValue.SubscriptionInvoiceID == nil {
		return "", ErrFinanceState
	}
	if caseValue.RechargeOrderID != nil {
		var linked bool
		if err = tx.QueryRow(ctx, `SELECT recharge_order_id=$2 FROM ledger_journal WHERE id=$1`, sourceJournalID, *caseValue.RechargeOrderID).Scan(&linked); err != nil || !linked {
			return "", ErrFinanceState
		}
	}
	if caseValue.FundingOperationID != nil {
		var linked bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wallet_transactions
			WHERE journal_id=$1 AND funding_operation_id=$2)`, sourceJournalID, *caseValue.FundingOperationID).Scan(&linked); err != nil || !linked {
			return "", ErrFinanceState
		}
	}
	if caseValue.SubscriptionInvoiceID != nil {
		var linked bool
		if err = tx.QueryRow(ctx, `SELECT subscription_invoice_id=$2 FROM ledger_journal WHERE id=$1`, sourceJournalID, *caseValue.SubscriptionInvoiceID).Scan(&linked); err != nil || !linked {
			return "", ErrFinanceState
		}
	}
	reversalID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO ledger_journal(id,wallet_id,journal_type,external_key,currency,reference,metadata,created_by,reversal_of_journal_id,reconciliation_case_id) VALUES($1,$2,'RECONCILIATION_REVERSAL',$3,$4,$5,$6,$7,$8,$9)`, reversalID, walletID, "reconciliation:"+caseValue.ID+":"+idempotencyKey, currency, reason, jsonBytes(map[string]any{"case_id": caseValue.ID, "source_journal_id": sourceJournalID, "reason": reason}), actor, sourceJournalID, caseValue.ID)
	if err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, `SELECT account_id,entry_side,amount::text,description FROM ledger_journal_entry WHERE journal_id=$1 ORDER BY id`, sourceJournalID)
	if err != nil {
		return "", err
	}
	type line struct{ accountID, side, amount, description string }
	lines := []line{}
	for rows.Next() {
		var v line
		if err = rows.Scan(&v.accountID, &v.side, &v.amount, &v.description); err != nil {
			rows.Close()
			return "", err
		}
		lines = append(lines, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	if len(lines) < 2 {
		return "", ErrFinanceState
	}
	for _, v := range lines {
		side := "CREDIT"
		if v.side == "CREDIT" {
			side = "DEBIT"
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger_journal_entry(id,journal_id,account_id,currency,entry_side,amount,description) VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), reversalID, v.accountID, currency, side, v.amount, "Reversal: "+v.description)
		if err != nil {
			return "", err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ledger_journal SET status='POSTED',posted_at=now() WHERE id=$1`, reversalID)
	if err != nil {
		return "", err
	}
	if walletID != nil {
		var availableDelta, reservedDelta string
		err = tx.QueryRow(ctx, `SELECT COALESCE(sum(CASE WHEN account.account_key='wallet:'||$2||':available' THEN CASE WHEN entry.entry_side='CREDIT' THEN entry.amount ELSE -entry.amount END ELSE 0 END),0)::text,COALESCE(sum(CASE WHEN account.account_key='wallet:'||$2||':reserved' THEN CASE WHEN entry.entry_side='CREDIT' THEN entry.amount ELSE -entry.amount END ELSE 0 END),0)::text FROM ledger_journal_entry entry JOIN ledger_account account ON account.id=entry.account_id WHERE entry.journal_id=$1`, reversalID, *walletID).Scan(&availableDelta, &reservedDelta)
		if err != nil {
			return "", err
		}
		var balanceAfter string
		err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance+$2::numeric,reserved_balance=reserved_balance+$3::numeric,version=version+1,updated_at=now() WHERE id=$1 AND reserved_balance+$3::numeric>=0 RETURNING available_balance::text`, *walletID, availableDelta, reservedDelta).Scan(&balanceAfter)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrFinanceState
		}
		if err != nil {
			return "", err
		}
		if mustFundingRat(availableDelta).Sign() != 0 {
			transactionID := id.UUID()
			_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,reference,metadata,created_by,journal_id) VALUES($1,$2,'ADJUSTMENT',$3,$4,$5,$6,$7,$8,$9)`, transactionID, *walletID, availableDelta, balanceAfter, "reconciliation:"+caseValue.ID+":"+idempotencyKey, reason, jsonBytes(map[string]any{"reconciliation_case_id": caseValue.ID, "source_journal_id": sourceJournalID}), actor, reversalID)
			if err != nil {
				return "", err
			}
			if mustFundingRat(availableDelta).Sign() < 0 {
				if err = allocateCashDebitTx(ctx, tx, *walletID, transactionID, formatRat(new(big.Rat).Abs(mustFundingRat(availableDelta)))); err != nil {
					return "", err
				}
			} else {
				var sourceTransactionID string
				if err = tx.QueryRow(ctx, `SELECT id FROM wallet_transactions WHERE journal_id=$1 ORDER BY created_at,id LIMIT 1`, sourceJournalID).Scan(&sourceTransactionID); err != nil {
					return "", ErrFinanceState
				}
				if err = restoreCashAllocationTx(ctx, tx, *walletID, sourceTransactionID, transactionID, availableDelta); err != nil {
					return "", err
				}
			}
		}
	}
	return reversalID, nil
}

// All candidate queries expose one standardized row shape. A missing or
// mismatched source is always materialized as a queue case.
const paymentToRechargeSQL = `SELECT recharge.id::text observation_key,
	(EXISTS(SELECT 1 FROM payment_reconciliation_record record WHERE record.recharge_order_id=recharge.id
		AND record.result='MATCHED' AND record.provider_status=CASE WHEN recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED') THEN 'PAID' ELSE recharge.status END
		AND record.provider_amount=recharge.amount
		AND record.currency=recharge.currency AND COALESCE(record.details->>'evidence_source','') IN ('PROVIDER_API','PROVIDER_SIGNED_WEBHOOK','OPERATOR_VERIFIED_MANUAL'))
	 OR EXISTS(SELECT 1 FROM payment_webhook_event event WHERE event.recharge_order_id=recharge.id AND event.processing_status='PROCESSED'
		AND event.payment_status=CASE WHEN recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED') THEN 'PAID' ELSE recharge.status END
		AND event.amount=recharge.amount AND event.currency=recharge.currency)
	 OR (recharge.payment_provider='manual_transfer' AND COALESCE(recharge.metadata->>'manual_evidence_reference','')<>''
		AND EXISTS(SELECT 1 FROM payment_attempt attempt WHERE attempt.recharge_order_id=recharge.id AND attempt.operation='MANUAL_REVIEW' AND attempt.status='SUCCEEDED'))) matched,
	CASE WHEN EXISTS(SELECT 1 FROM payment_reconciliation_record record WHERE record.recharge_order_id=recharge.id
		AND COALESCE(record.details->>'evidence_source','')='PROVIDER_API_ERROR') THEN 'PAYMENT_CHANNEL_QUERY_FAILED'
	 WHEN recharge.payment_provider NOT IN ('sandbox','manual_transfer')
		AND NOT EXISTS(SELECT 1 FROM payment_reconciliation_record record WHERE record.recharge_order_id=recharge.id
			AND COALESCE(record.details->>'evidence_source','') IN ('PROVIDER_API','PROVIDER_SIGNED_WEBHOOK','OPERATOR_VERIFIED_MANUAL'))
		THEN 'PAYMENT_CHANNEL_RECONCILIATION_UNSUPPORTED'
	 WHEN EXISTS(SELECT 1 FROM payment_reconciliation_record record WHERE record.recharge_order_id=recharge.id
		AND (record.result<>'MATCHED' OR record.provider_status<>CASE WHEN recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED') THEN 'PAID' ELSE recharge.status END
		OR record.provider_amount<>recharge.amount
		OR record.currency<>recharge.currency)) THEN 'PAYMENT_CHANNEL_MISMATCH' ELSE 'PAYMENT_CHANNEL_EVIDENCE_MISSING' END classification,
	'HIGH' severity,recharge.organization_id,NULL::uuid,recharge.id,NULL::uuid,NULL::uuid,recharge.amount::text,
	(SELECT record.provider_amount::text FROM payment_reconciliation_record record WHERE record.recharge_order_id=recharge.id ORDER BY record.reconciled_at DESC LIMIT 1),recharge.currency,
	jsonb_build_object('platform_order_no',recharge.platform_order_no,'provider_order_no',COALESCE(recharge.provider_order_no,''),'local_status',recharge.status,'payment_provider',recharge.payment_provider) details
	FROM recharge_order recharge WHERE ((recharge.created_at>=($1::date::timestamp AT TIME ZONE 'UTC')
		AND recharge.created_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (recharge.paid_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND recharge.paid_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (recharge.credited_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND recharge.credited_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (recharge.updated_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND recharge.updated_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')))
	AND recharge.status IN ('PAID','CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')`

const rechargeToWalletSQL = `SELECT recharge.id::text,
	(recharge.wallet_transaction_id IS NOT NULL AND recharge.ledger_journal_id IS NOT NULL AND transaction.recharge_order_id=recharge.id
	 AND transaction.amount=recharge.amount AND journal.status='POSTED' AND balance.debits=balance.credits) matched,
	CASE WHEN recharge.wallet_transaction_id IS NULL OR recharge.ledger_journal_id IS NULL THEN 'WALLET_LINK_MISSING' WHEN transaction.amount<>recharge.amount THEN 'WALLET_AMOUNT_MISMATCH' ELSE 'LEDGER_UNBALANCED' END,
	'CRITICAL',recharge.organization_id,NULL::uuid,recharge.id,NULL::uuid,NULL::uuid,recharge.amount::text,transaction.amount::text,recharge.currency,
	jsonb_build_object('wallet_transaction_id',recharge.wallet_transaction_id,'ledger_journal_id',recharge.ledger_journal_id,'status',recharge.status)
	FROM recharge_order recharge LEFT JOIN wallet_transactions transaction ON transaction.id=recharge.wallet_transaction_id LEFT JOIN ledger_journal journal ON journal.id=recharge.ledger_journal_id
	LEFT JOIN LATERAL(SELECT COALESCE(sum(amount) FILTER(WHERE entry_side='DEBIT'),0) debits,COALESCE(sum(amount) FILTER(WHERE entry_side='CREDIT'),0) credits FROM ledger_journal_entry WHERE journal_id=journal.id) balance ON true
	WHERE recharge.credited_at>=($1::date::timestamp AT TIME ZONE 'UTC')
	AND recharge.credited_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')
	AND recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')`

const usageToUserChargeSQL = `SELECT usage.request_id observation_key,
	COALESCE((CASE WHEN usage.status='WAIVED' AND operation.id IS NULL THEN
		COALESCE(usage.final_user_amount,usage.amount)=0 AND charge.transaction_count=0
	 WHEN usage.status IN ('WAIVED') THEN operation.status IN ('RELEASED','FAILED')
	 ELSE operation.id IS NOT NULL AND charge.transaction_count>0 AND charge.posted_count=charge.transaction_count
		AND charge.charged_amount=COALESCE(usage.final_user_amount,usage.amount) END),false),
	CASE WHEN operation.id IS NULL THEN 'FUNDING_OPERATION_MISSING' WHEN usage.status<>'WAIVED' AND charge.transaction_count=0 THEN 'USER_CHARGE_MISSING'
	 WHEN usage.status<>'WAIVED' AND charge.charged_amount<>COALESCE(usage.final_user_amount,usage.amount) THEN 'USER_CHARGE_AMOUNT_MISMATCH' ELSE 'USER_CHARGE_STATUS_MISMATCH' END,
	'CRITICAL',usage.organization_id,usage.provider_id,NULL::uuid,usage.funding_operation_id,NULL::uuid,
	COALESCE(usage.final_user_amount,usage.amount)::text,charge.charged_amount::text,usage.currency,
	jsonb_build_object('request_id',usage.request_id,'usage_status',usage.status,'funding_status',operation.status,
		'transaction_count',charge.transaction_count,'posted_count',charge.posted_count,'journal_ids',charge.journal_ids)
	FROM billing_usage_records usage LEFT JOIN funding_operation operation ON operation.id=usage.funding_operation_id
	LEFT JOIN LATERAL(SELECT count(*) transaction_count,count(*) FILTER(WHERE journal.status='POSTED') posted_count,
		COALESCE(-sum(transaction.amount),0) charged_amount,jsonb_agg(journal.id ORDER BY transaction.created_at,transaction.id) journal_ids
		FROM wallet_transactions transaction LEFT JOIN ledger_journal journal ON journal.id=transaction.journal_id
		WHERE transaction.funding_operation_id=operation.id) charge ON true
	WHERE usage.created_at>=($1::date::timestamp AT TIME ZONE 'UTC')
	AND usage.created_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')`

const usageToProviderUsageSQL = `SELECT usage.request_id,
	((operation.id IS NULL AND usage.status='WAIVED' AND COALESCE(usage.provider_cost_amount,0)=0
		AND request_log.id IS NOT NULL AND attempt.id IS NULL)
	 OR (operation.id IS NOT NULL AND CASE
		WHEN request_log.id IS NULL AND attempt.id IS NULL AND COALESCE(operation.usage_source,'')='NO_PROVIDER_USAGE'
			THEN COALESCE(usage.provider_cost_amount,0)=0
		ELSE attempt.id IS NOT NULL AND attempt.provider_id=usage.provider_id AND attempt.status='SUCCEEDED'
			AND attempt.upstream_request_id IS NOT NULL AND request_log.id IS NOT NULL
			AND request_log.provider_id=usage.provider_id AND request_log.upstream_request_id=attempt.upstream_request_id END)),
	CASE WHEN operation.id IS NULL THEN 'FUNDING_OPERATION_MISSING'
		WHEN request_log.id IS NULL AND NOT (attempt.id IS NULL AND COALESCE(operation.usage_source,'')='NO_PROVIDER_USAGE') THEN 'REQUEST_LOG_MISSING'
		WHEN request_log.id IS NULL AND attempt.id IS NULL AND COALESCE(operation.usage_source,'')='NO_PROVIDER_USAGE'
			AND COALESCE(usage.provider_cost_amount,0)<>0 THEN 'PROVIDER_COST_WITHOUT_USAGE'
		WHEN attempt.id IS NULL THEN 'PROVIDER_ATTEMPT_MISSING'
		WHEN attempt.provider_id<>usage.provider_id OR request_log.provider_id<>usage.provider_id THEN 'PROVIDER_LINK_MISMATCH'
		WHEN attempt.status<>'SUCCEEDED' THEN 'PROVIDER_ATTEMPT_NOT_SUCCEEDED'
		WHEN attempt.upstream_request_id IS NULL OR request_log.upstream_request_id IS NULL THEN 'UPSTREAM_REQUEST_ID_MISSING'
		ELSE 'UPSTREAM_REQUEST_ID_MISMATCH' END,
	'HIGH',usage.organization_id,usage.provider_id,NULL::uuid,usage.funding_operation_id,NULL::uuid,NULL::text,NULL::text,COALESCE(snapshot.provider_currency,usage.currency),
	jsonb_build_object('request_id',usage.request_id,'upstream_request_id',COALESCE(attempt.upstream_request_id,''),
		'request_log_upstream_request_id',COALESCE(request_log.upstream_request_id,''),'attempt_status',COALESCE(attempt.status,''),
		'usage_status',usage.status,'usage_source',COALESCE(operation.usage_source,''))
	FROM billing_usage_records usage LEFT JOIN funding_operation operation ON operation.id=usage.funding_operation_id
	LEFT JOIN request_logs request_log ON request_log.request_id=usage.request_id
	LEFT JOIN LATERAL(SELECT id,provider_id,status,upstream_request_id FROM funding_provider_attempt WHERE operation_id=operation.id ORDER BY attempt_no DESC LIMIT 1) attempt ON true
	LEFT JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
	WHERE usage.created_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND usage.created_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')`

const providerUsageToBillSQL = `WITH usage_side AS (
	SELECT usage.request_id observation_key,
		(COALESCE(matches.line_count,0)=1 AND matches.amount=usage.provider_cost_amount AND matches.currency=snapshot.provider_currency) matched,
		CASE WHEN COALESCE(matches.line_count,0)=0 THEN 'PROVIDER_STATEMENT_LINE_MISSING'
			WHEN matches.line_count>1 THEN 'PROVIDER_STATEMENT_LINE_DUPLICATE'
			WHEN matches.currency<>snapshot.provider_currency THEN 'PROVIDER_BILL_CURRENCY_MISMATCH'
			ELSE 'PROVIDER_BILL_AMOUNT_MISMATCH' END classification,
		'MEDIUM' severity,usage.organization_id,usage.provider_id,NULL::uuid recharge_order_id,
		usage.funding_operation_id,NULL::uuid subscription_invoice_id,COALESCE(usage.provider_cost_amount,0)::text expected_amount,
		matches.amount::text actual_amount,COALESCE(snapshot.provider_currency,usage.currency) currency,
		jsonb_build_object('request_id',usage.request_id,'upstream_request_id',COALESCE(attempt.upstream_request_id,''),
			'statement_line_ids',COALESCE(matches.line_ids,'[]'::jsonb),'statement_line_count',COALESCE(matches.line_count,0)) details
	FROM billing_usage_records usage
	LEFT JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
	LEFT JOIN LATERAL(SELECT upstream_request_id FROM funding_provider_attempt WHERE operation_id=usage.funding_operation_id
		AND status='SUCCEEDED' ORDER BY attempt_no DESC LIMIT 1) attempt ON true
	LEFT JOIN LATERAL(SELECT count(*) line_count,sum(statement_line.amount) amount,
		CASE WHEN min(statement_line.currency)=max(statement_line.currency) THEN min(statement_line.currency) END currency,
		jsonb_agg(statement_line.id ORDER BY statement.imported_at,statement_line.id) line_ids
		FROM provider_statement_line statement_line JOIN provider_statement statement ON statement.id=statement_line.provider_statement_id
		WHERE statement.provider_id=usage.provider_id AND (statement_line.request_id=usage.request_id
			OR (attempt.upstream_request_id IS NOT NULL AND statement_line.upstream_request_id=attempt.upstream_request_id))) matches ON true
	WHERE usage.created_at>=($1::date::timestamp AT TIME ZONE 'UTC')
		AND usage.created_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC') AND COALESCE(usage.provider_cost_amount,0)>0
), statement_side AS (
	SELECT 'statement-line:'||line.id::text observation_key,false matched,
		CASE WHEN linked.usage_count=0 THEN 'PROVIDER_STATEMENT_LINE_WITHOUT_USAGE' ELSE 'PROVIDER_STATEMENT_LINE_AMBIGUOUS' END classification,
		'MEDIUM' severity,NULL::uuid organization_id,statement.provider_id,NULL::uuid recharge_order_id,
		NULL::uuid funding_operation_id,NULL::uuid subscription_invoice_id,NULL::text expected_amount,line.amount::text actual_amount,
		line.currency,jsonb_build_object('statement_id',statement.id,'statement_line_id',line.id,'external_line_id',line.external_line_id,
			'request_id',COALESCE(line.request_id,''),'upstream_request_id',COALESCE(line.upstream_request_id,''),
			'linked_usage_count',linked.usage_count,'linked_request_ids',COALESCE(linked.request_ids,'[]'::jsonb)) details
	FROM provider_statement_line line JOIN provider_statement statement ON statement.id=line.provider_statement_id
	LEFT JOIN LATERAL(SELECT count(DISTINCT usage.id) usage_count,jsonb_agg(DISTINCT usage.request_id) request_ids
		FROM billing_usage_records usage LEFT JOIN funding_provider_attempt attempt ON attempt.operation_id=usage.funding_operation_id
		WHERE usage.provider_id=statement.provider_id AND (usage.request_id=line.request_id
			OR (line.upstream_request_id IS NOT NULL AND attempt.upstream_request_id=line.upstream_request_id))) linked ON true
	WHERE line.usage_date=$1::date AND linked.usage_count<>1
)
	SELECT * FROM usage_side UNION ALL SELECT * FROM statement_side`

const subscriptionToStateSQL = `SELECT invoice.id::text,
	(CASE WHEN invoice.status='PAID' THEN
		(invoice.total_amount=0 OR (invoice.ledger_journal_id IS NOT NULL AND journal.status='POSTED'
			AND journal.subscription_invoice_id=invoice.id AND journal.currency=invoice.currency
			AND balance.debits=invoice.total_amount AND balance.credits=invoice.total_amount))
		AND subscription.status IN ('ACTIVE','CANCELED')
	 WHEN invoice.status IN ('OPEN','FAILED') AND invoice.due_at<now() THEN subscription.status IN ('PAST_DUE','GRACE_PERIOD','EXPIRED','CANCELED')
	 ELSE true END),
	CASE WHEN invoice.status='PAID' AND invoice.total_amount>0 AND invoice.ledger_journal_id IS NULL THEN 'SUBSCRIPTION_LEDGER_MISSING'
		WHEN invoice.status='PAID' AND (journal.subscription_invoice_id IS DISTINCT FROM invoice.id OR journal.currency IS DISTINCT FROM invoice.currency) THEN 'SUBSCRIPTION_LEDGER_LINK_MISMATCH'
		WHEN invoice.status='PAID' AND (balance.debits IS DISTINCT FROM invoice.total_amount OR balance.credits IS DISTINCT FROM invoice.total_amount) THEN 'SUBSCRIPTION_LEDGER_AMOUNT_MISMATCH'
		WHEN invoice.status='PAID' THEN 'SUBSCRIPTION_STATE_MISMATCH' ELSE 'OVERDUE_SUBSCRIPTION_STATE_MISMATCH' END,
	'HIGH',invoice.organization_id,NULL::uuid,NULL::uuid,NULL::uuid,invoice.id,invoice.total_amount::text,balance.debits::text,invoice.currency,
	jsonb_build_object('invoice_status',invoice.status,'subscription_status',subscription.status,'ledger_journal_id',invoice.ledger_journal_id,
		'journal_subscription_invoice_id',journal.subscription_invoice_id,'journal_currency',journal.currency,
		'journal_debits',balance.debits,'journal_credits',balance.credits,'due_at',invoice.due_at,'paid_at',invoice.paid_at,'failed_at',invoice.failed_at)
	FROM subscription_invoice invoice JOIN organization_subscription subscription ON subscription.id=invoice.organization_subscription_id
	LEFT JOIN ledger_journal journal ON journal.id=invoice.ledger_journal_id
	LEFT JOIN LATERAL(SELECT COALESCE(sum(amount) FILTER(WHERE entry_side='DEBIT'),0) debits,
		COALESCE(sum(amount) FILTER(WHERE entry_side='CREDIT'),0) credits FROM ledger_journal_entry WHERE journal_id=journal.id) balance ON true
	WHERE (invoice.created_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND invoice.created_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (invoice.due_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND invoice.due_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (invoice.paid_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND invoice.paid_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (invoice.failed_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND invoice.failed_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
	OR (invoice.updated_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND invoice.updated_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))`

const supplierPayableToUsageSQL = `SELECT accrual.id::text,
	(usage.status='CHARGED' AND operation.status IN ('SETTLED','PARTIALLY_SETTLED')
	 AND snapshot.id=accrual.usage_price_snapshot_id AND snapshot.provider_cost_amount=accrual.gross_amount
	 AND snapshot.provider_currency=accrual.currency) matched,
	CASE WHEN usage.status<>'CHARGED' THEN 'SUPPLIER_USAGE_NOT_CHARGED'
	 WHEN operation.status NOT IN ('SETTLED','PARTIALLY_SETTLED') THEN 'SUPPLIER_USAGE_NOT_SETTLED'
	 ELSE 'SUPPLIER_PAYABLE_AMOUNT_MISMATCH' END classification,
	'HIGH' severity,usage.organization_id,accrual.provider_id,NULL::uuid,accrual.funding_operation_id,NULL::uuid,
	accrual.gross_amount,snapshot.provider_cost_amount,accrual.currency,
	jsonb_build_object('supplier_id',accrual.supplier_id,'request_id',accrual.request_id,
	 'usage_price_snapshot_id',accrual.usage_price_snapshot_id,'platform_measured',true) details
	FROM supplier_payable_accrual accrual JOIN billing_usage_records usage ON usage.id=accrual.billing_usage_record_id
	JOIN usage_price_snapshot snapshot ON snapshot.id=accrual.usage_price_snapshot_id
	JOIN funding_operation operation ON operation.id=accrual.funding_operation_id
	WHERE accrual.usage_settled_at::date<=$1::date`

const supplierBillToPayableSQL = `SELECT line.id::text,
	(accrual.id IS NOT NULL AND accrual.gross_amount=line.amount AND accrual.currency=line.currency) matched,
	CASE WHEN accrual.id IS NULL THEN 'SUPPLIER_DECLARATION_WITHOUT_PLATFORM_USAGE'
	 ELSE 'SUPPLIER_BILL_AMOUNT_MISMATCH' END classification,
	'HIGH' severity,usage.organization_id,bill.provider_id,NULL::uuid,accrual.funding_operation_id,NULL::uuid,
	accrual.gross_amount,line.amount,line.currency,
	jsonb_build_object('supplier_id',bill.supplier_id,'supplier_bill_id',bill.id,'external_line_id',line.external_line_id,
	 'request_id',line.request_id,'supplier_declared_only',true) details
	FROM supplier_bill_line line JOIN supplier_bill bill ON bill.id=line.supplier_bill_id
	LEFT JOIN supplier_payable_accrual accrual ON accrual.supplier_id=bill.supplier_id AND accrual.provider_id=bill.provider_id
	 AND accrual.request_id=line.request_id
	LEFT JOIN billing_usage_records usage ON usage.id=accrual.billing_usage_record_id
	WHERE line.usage_date<=$1::date`

const supplierPayoutToLedgerSQL = `SELECT batch.id::text,
	(journal.id IS NOT NULL AND journal.status='POSTED' AND balance.debits=balance.credits
	 AND cash.paid_amount=batch.payout_amount) matched,
	CASE WHEN journal.id IS NULL THEN 'SUPPLIER_PAYOUT_LEDGER_MISSING'
	 WHEN balance.debits<>balance.credits THEN 'SUPPLIER_PAYOUT_LEDGER_UNBALANCED'
	 ELSE 'SUPPLIER_PAYOUT_CASH_MISMATCH' END classification,
	'CRITICAL' severity,NULL::uuid,batch.provider_id,NULL::uuid,NULL::uuid,NULL::uuid,
	batch.payout_amount,cash.paid_amount,batch.currency,
	jsonb_build_object('supplier_id',batch.supplier_id,'settlement_batch_id',batch.id,
	 'provider_payout_reference',batch.provider_payout_reference,'journal_id',journal.id) details
	FROM supplier_settlement_batch batch
	LEFT JOIN ledger_journal journal ON journal.supplier_settlement_batch_id=batch.id
	LEFT JOIN LATERAL(SELECT COALESCE(sum(amount) FILTER(WHERE entry_side='DEBIT'),0) debits,
	 COALESCE(sum(amount) FILTER(WHERE entry_side='CREDIT'),0) credits FROM ledger_journal_entry WHERE journal_id=journal.id) balance ON true
	LEFT JOIN LATERAL(SELECT COALESCE(sum(entry.amount),0) paid_amount FROM ledger_journal_entry entry
	 JOIN ledger_account account ON account.id=entry.account_id WHERE entry.journal_id=journal.id
	 AND account.account_key='system:cash:'||batch.currency AND entry.entry_side='CREDIT') cash ON true
	WHERE batch.status='PAID' AND batch.paid_at::date<=$1::date`

func reconciliationDebug(candidate reconciliationCandidate) string {
	return fmt.Sprintf("%s:%s", candidate.classification, candidate.observationKey)
}
