package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	settlementcalc "github.com/relayedock/relayedock/internal/settlement"
)

var (
	ErrSupplierSettlementState = errors.New("supplier settlement state does not permit this operation")
	ErrSupplierPayoutBlocked   = errors.New("supplier payout is blocked by eligibility, invoice, tax, or dispute controls")
	ErrMinimumSettlement       = errors.New("supplier payable amount is below the configured minimum")
	ErrSupplierBillMismatch    = errors.New("supplier bill total does not equal its lines")
)

type SupplierBillInput struct {
	SupplierID    string
	ProviderID    string
	BillReference string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Currency      string
	TotalAmount   string
	SourceSHA256  string
	DeclaredBy    string
	Lines         []SupplierBillLineInput
}

type SupplierBillLineInput struct {
	ExternalLineID    string         `json:"external_line_id"`
	RequestID         string         `json:"request_id"`
	UpstreamRequestID string         `json:"upstream_request_id"`
	UsageDate         time.Time      `json:"usage_date"`
	InputTokens       *int64         `json:"input_tokens"`
	CachedInputTokens *int64         `json:"cached_input_tokens"`
	OutputTokens      *int64         `json:"output_tokens"`
	Amount            string         `json:"amount"`
	Currency          string         `json:"currency"`
	Metadata          map[string]any `json:"metadata"`
}

type SupplierPayoutJob struct {
	Batch              domain.SupplierSettlementBatch
	AttemptID          string
	AttemptNo          int
	PayoutAccount      []byte
	PayoutAccountOwner string
}

const supplierPolicySelect = `SELECT supplier_id,enabled,settlement_cycle,minimum_payout::text,commission_bps,
	risk_reserve_bps,reserve_hold_days,payout_adapter,payout_region,tax_verification_required,invoice_required,
	next_settlement_at,last_period_end::text,created_at,updated_at FROM supplier_settlement_policy`

func scanSupplierPolicy(row pgx.Row) (domain.SupplierSettlementPolicy, error) {
	var value domain.SupplierSettlementPolicy
	var minimum string
	var lastPeriodEnd *string
	err := row.Scan(&value.SupplierID, &value.Enabled, &value.SettlementCycle, &minimum, &value.CommissionBPS,
		&value.RiskReserveBPS, &value.ReserveHoldDays, &value.PayoutAdapter, &value.PayoutRegion,
		&value.TaxVerificationRequired, &value.InvoiceRequired, &value.NextSettlementAt, &lastPeriodEnd,
		&value.CreatedAt, &value.UpdatedAt)
	value.MinimumPayout = domain.Decimal(minimum)
	value.LastPeriodEnd = lastPeriodEnd
	return value, err
}

func (s *Store) SupplierSettlementPolicy(ctx context.Context, supplierID string) (domain.SupplierSettlementPolicy, error) {
	value, err := scanSupplierPolicy(s.pool.QueryRow(ctx, supplierPolicySelect+` WHERE supplier_id=$1`, supplierID))
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	return value, err
}

func (s *Store) UpdateSupplierSettlementPolicy(ctx context.Context, value domain.SupplierSettlementPolicy, actor string) (domain.SupplierSettlementPolicy, error) {
	value.SettlementCycle = strings.ToUpper(strings.TrimSpace(value.SettlementCycle))
	value.PayoutAdapter = strings.ToLower(strings.TrimSpace(value.PayoutAdapter))
	value.PayoutRegion = strings.ToUpper(strings.TrimSpace(value.PayoutRegion))
	minimumNegative, minimumErr := value.MinimumPayout.IsNegative()
	if value.SettlementCycle != "DAILY" && value.SettlementCycle != "WEEKLY" && value.SettlementCycle != "MONTHLY" ||
		value.CommissionBPS < 0 || value.CommissionBPS > 10000 || value.RiskReserveBPS < 0 || value.RiskReserveBPS > 10000 ||
		value.ReserveHoldDays < 0 || value.ReserveHoldDays > 3660 || minimumErr != nil || minimumNegative ||
		value.PayoutAdapter == "" || value.Enabled && (len(value.PayoutRegion) != 2 || value.PayoutAdapter == "disabled") {
		return domain.SupplierSettlementPolicy{}, ErrSupplierSettlementState
	}
	if value.Enabled && value.NextSettlementAt == nil {
		next := nextSettlementBoundary(time.Now().UTC(), value.SettlementCycle)
		value.NextSettlementAt = &next
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierSettlementPolicy{}, err
	}
	defer tx.Rollback(ctx)
	var status, kyb, contract, payoutLast4 string
	err = tx.QueryRow(ctx, `SELECT status,kyb_status,contract_status,payout_account_last4 FROM supplier_organizations WHERE id=$1 FOR UPDATE`, value.SupplierID).
		Scan(&status, &kyb, &contract, &payoutLast4)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementPolicy{}, ErrNotFound
	}
	if err != nil {
		return domain.SupplierSettlementPolicy{}, err
	}
	if value.Enabled && (status != "APPROVED" || kyb != "VERIFIED" || contract != "ACTIVE" || payoutLast4 == "") {
		return domain.SupplierSettlementPolicy{}, ErrSupplierPayoutBlocked
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_settlement_policy SET enabled=$2,settlement_cycle=$3,minimum_payout=$4,
		commission_bps=$5,risk_reserve_bps=$6,reserve_hold_days=$7,payout_adapter=$8,payout_region=$9,
		tax_verification_required=$10,invoice_required=$11,next_settlement_at=$12,updated_by=$13,updated_at=now()
		WHERE supplier_id=$1`, value.SupplierID, value.Enabled, value.SettlementCycle, value.MinimumPayout.String(),
		value.CommissionBPS, value.RiskReserveBPS, value.ReserveHoldDays, value.PayoutAdapter, value.PayoutRegion,
		value.TaxVerificationRequired, value.InvoiceRequired, value.NextSettlementAt, actor)
	if err != nil {
		return domain.SupplierSettlementPolicy{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.settlement_policy_updated", "supplier_settlement_policy", value.SupplierID,
		map[string]any{"enabled": value.Enabled, "settlement_cycle": value.SettlementCycle, "minimum_payout": value.MinimumPayout.String(),
			"commission_bps": value.CommissionBPS, "risk_reserve_bps": value.RiskReserveBPS, "reserve_hold_days": value.ReserveHoldDays,
			"payout_adapter": value.PayoutAdapter, "payout_region": value.PayoutRegion}); err != nil {
		return domain.SupplierSettlementPolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierSettlementPolicy{}, err
	}
	return s.SupplierSettlementPolicy(ctx, value.SupplierID)
}

func nextSettlementBoundary(now time.Time, cycle string) time.Time {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch cycle {
	case "DAILY":
		return day.AddDate(0, 0, 1)
	case "WEEKLY":
		days := (8 - int(day.Weekday())) % 7
		if days == 0 {
			days = 7
		}
		return day.AddDate(0, 0, days)
	default:
		return time.Date(day.Year(), day.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
}

func previousSettlementPeriod(boundary time.Time, cycle string) (time.Time, time.Time) {
	end := time.Date(boundary.UTC().Year(), boundary.UTC().Month(), boundary.UTC().Day(), 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
	switch cycle {
	case "DAILY":
		return end.AddDate(0, 0, -1).Add(time.Nanosecond), end
	case "WEEKLY":
		return end.AddDate(0, 0, -7).Add(time.Nanosecond), end
	default:
		start := time.Date(boundary.UTC().Year(), boundary.UTC().Month()-1, 1, 0, 0, 0, 0, time.UTC)
		return start, end
	}
}

func (s *Store) AccrueEligibleSupplierUsage(ctx context.Context, limit int) (int, error) {
	limit = clampFinanceLimitTo(limit, 500)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT usage.id,usage.request_id,usage.provider_id,usage.usage_price_snapshot_id,
		usage.funding_operation_id,snapshot.provider_cost_amount::text,snapshot.provider_currency,
		COALESCE(operation.settled_at,snapshot.settled_at),link.supplier_id,policy.commission_bps,
		policy.risk_reserve_bps,policy.reserve_hold_days
		FROM billing_usage_records usage
		JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
		JOIN funding_operation operation ON operation.id=usage.funding_operation_id
		JOIN supplier_provider_links link ON link.provider_id=usage.provider_id AND link.status='ACTIVE'
		JOIN supplier_organizations supplier ON supplier.id=link.supplier_id
		JOIN supplier_settlement_policy policy ON policy.supplier_id=supplier.id AND policy.enabled=true
		JOIN providers provider ON provider.id=usage.provider_id
		WHERE usage.status='CHARGED' AND operation.status IN ('SETTLED','PARTIALLY_SETTLED')
		AND snapshot.credential_owner='PLATFORM' AND snapshot.provider_cost_amount>=0
		AND snapshot.settled_at>=link.linked_at AND supplier.status='APPROVED' AND supplier.kyb_status='VERIFIED'
		AND supplier.contract_status='ACTIVE' AND provider.enabled=true AND provider.contract_status='ACTIVE'
		AND provider.pricing_disabled=false AND provider.emergency_kill_switch=false
		AND NOT EXISTS(SELECT 1 FROM supplier_payable_accrual accrual WHERE accrual.billing_usage_record_id=usage.id)
		ORDER BY snapshot.settled_at,usage.id FOR UPDATE OF usage SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		usageID, requestID, providerID, snapshotID, operationID, gross, currency, supplierID string
		settledAt                                                                            time.Time
		commissionBPS, reserveBPS, holdDays                                                  int
	}
	items := make([]candidate, 0)
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.usageID, &value.requestID, &value.providerID, &value.snapshotID, &value.operationID,
			&value.gross, &value.currency, &value.settledAt, &value.supplierID, &value.commissionBPS, &value.reserveBPS, &value.holdDays); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, value)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	created := 0
	for _, value := range items {
		split, splitErr := settlementcalc.Split(domain.Decimal(value.gross), value.commissionBPS, value.reserveBPS)
		if splitErr != nil {
			return 0, splitErr
		}
		accrualID := id.UUID()
		reserveAt := value.settledAt.AddDate(0, 0, value.holdDays)
		tag, insertErr := tx.Exec(ctx, `INSERT INTO supplier_payable_accrual(id,idempotency_key,supplier_id,provider_id,
			billing_usage_record_id,usage_price_snapshot_id,funding_operation_id,request_id,gross_amount,commission_bps,
			commission_amount,reserve_bps,reserve_amount,initial_payable_amount,currency,usage_settled_at,reserve_releasable_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) ON CONFLICT(idempotency_key) DO NOTHING`,
			accrualID, "usage:"+value.usageID, value.supplierID, value.providerID, value.usageID, value.snapshotID,
			value.operationID, value.requestID, split.Gross.String(), value.commissionBPS, split.Commission.String(),
			value.reserveBPS, split.Reserve.String(), split.Payable.String(), value.currency, value.settledAt, reserveAt)
		if insertErr != nil {
			return 0, insertErr
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		created++
		payablePositive, decimalErr := split.Payable.IsPositive()
		if decimalErr != nil {
			return 0, decimalErr
		}
		if payablePositive {
			_, err = tx.Exec(ctx, `INSERT INTO supplier_payable_entry(id,idempotency_key,supplier_id,provider_id,accrual_id,
				entry_type,entry_side,amount,currency,available_at,reference,metadata)
				VALUES($1,$2,$3,$4,$5,'USAGE_ACCRUAL','CREDIT',$6,$7,$8,$9,$10)`, id.UUID(), "usage-accrual:"+accrualID,
				value.supplierID, value.providerID, accrualID, split.Payable.String(), value.currency, value.settledAt,
				value.requestID, jsonBytes(map[string]any{"platform_measured": true, "usage_price_snapshot_id": value.snapshotID}))
			if err != nil {
				return 0, err
			}
		}
	}
	if created > 0 {
		if err = writeAuditTx(ctx, tx, nil, "supplier.payables_accrued", "supplier_payable_accrual", "batch", map[string]any{"count": created}); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return created, nil
}

func (s *Store) ReleaseMatureSupplierReserves(ctx context.Context, now time.Time, limit int) (int, error) {
	limit = clampFinanceLimitTo(limit, 500)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO supplier_payable_entry(id,idempotency_key,supplier_id,provider_id,accrual_id,
		entry_type,entry_side,amount,currency,available_at,reference,metadata)
		SELECT gen_random_uuid(),'reserve-release:'||accrual.id,accrual.supplier_id,accrual.provider_id,accrual.id,
		'RESERVE_RELEASE','CREDIT',accrual.reserve_amount,accrual.currency,$1,accrual.request_id,
		jsonb_build_object('platform_measured',true,'reserve_releasable_at',accrual.reserve_releasable_at)
		FROM supplier_payable_accrual accrual
		WHERE accrual.reserve_amount>0 AND accrual.reserve_releasable_at<=$1
		AND NOT EXISTS(SELECT 1 FROM supplier_payable_entry entry WHERE entry.accrual_id=accrual.id AND entry.entry_type='RESERVE_RELEASE')
		AND NOT EXISTS(SELECT 1 FROM supplier_appeal appeal WHERE appeal.accrual_id=accrual.id AND appeal.status IN ('OPEN','UNDER_REVIEW'))
		ORDER BY accrual.reserve_releasable_at,accrual.id LIMIT $2 ON CONFLICT(idempotency_key) DO NOTHING`, now.UTC(), limit)
	if err != nil {
		return 0, err
	}
	count := int(tag.RowsAffected())
	if count > 0 {
		if err = writeAuditTx(ctx, tx, nil, "supplier.reserves_released", "supplier_payable_entry", "batch", map[string]any{"count": count}); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) SupplierPayableSummary(ctx context.Context, supplierID string) ([]domain.SupplierPayableSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT entry.currency,
		COALESCE(sum(entry.amount) FILTER(WHERE entry.entry_type IN ('USAGE_ACCRUAL','RESERVE_RELEASE') AND entry.entry_side='CREDIT'),0)::text,
		COALESCE((SELECT sum(accrual.reserve_amount) FROM supplier_payable_accrual accrual WHERE accrual.supplier_id=$1 AND accrual.currency=entry.currency
			AND NOT EXISTS(SELECT 1 FROM supplier_payable_entry released WHERE released.accrual_id=accrual.id AND released.entry_type='RESERVE_RELEASE')),0)::text,
		COALESCE(sum(entry.amount) FILTER(WHERE entry.entry_type='REFUND_SHARE'),0)::text,
		COALESCE(sum(entry.amount) FILTER(WHERE entry.entry_type='PAYOUT'),0)::text,
		COALESCE(sum(CASE WHEN entry.entry_side='CREDIT' THEN entry.amount ELSE -entry.amount END),0)::text,
		(SELECT count(*) FROM supplier_appeal appeal WHERE appeal.supplier_id=$1 AND appeal.status IN ('OPEN','UNDER_REVIEW')),
		(SELECT count(*) FROM supplier_payable_accrual accrual WHERE accrual.supplier_id=$1 AND accrual.currency=entry.currency
			AND NOT EXISTS(SELECT 1 FROM supplier_usage_statement_match matched WHERE matched.accrual_id=accrual.id))
		FROM supplier_payable_entry entry WHERE entry.supplier_id=$1 GROUP BY entry.currency ORDER BY entry.currency`, supplierID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupplierPayableSummary, 0)
	for rows.Next() {
		var value domain.SupplierPayableSummary
		var accrued, reserve, refund, paid, available string
		value.SupplierID = supplierID
		if err = rows.Scan(&value.Currency, &accrued, &reserve, &refund, &paid, &available, &value.OpenAppeals, &value.UnmatchedAccruals); err != nil {
			return nil, err
		}
		value.Accrued, value.ReserveHeld, value.RefundShare, value.Paid, value.Available = domain.Decimal(accrued), domain.Decimal(reserve), domain.Decimal(refund), domain.Decimal(paid), domain.Decimal(available)
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) ListSupplierPayableAccruals(ctx context.Context, supplierID string, limit, offset int) ([]domain.SupplierPayableAccrual, error) {
	rows, err := s.pool.Query(ctx, `SELECT accrual.id,accrual.supplier_id,accrual.provider_id,accrual.billing_usage_record_id,
		accrual.usage_price_snapshot_id,accrual.funding_operation_id,accrual.request_id,accrual.gross_amount::text,
		accrual.commission_bps,accrual.commission_amount::text,accrual.reserve_bps,accrual.reserve_amount::text,
		accrual.initial_payable_amount::text,accrual.currency,accrual.usage_settled_at,accrual.reserve_releasable_at,
		EXISTS(SELECT 1 FROM supplier_usage_statement_match matched WHERE matched.accrual_id=accrual.id),
		EXISTS(SELECT 1 FROM supplier_appeal appeal WHERE appeal.accrual_id=accrual.id AND appeal.status IN ('OPEN','UNDER_REVIEW')),
		accrual.created_at FROM supplier_payable_accrual accrual WHERE ($1='' OR accrual.supplier_id=$1::uuid)
		ORDER BY accrual.usage_settled_at DESC,accrual.id LIMIT $2 OFFSET $3`, supplierID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupplierPayableAccrual, 0)
	for rows.Next() {
		var value domain.SupplierPayableAccrual
		var gross, commission, reserve, payable string
		if err = rows.Scan(&value.ID, &value.SupplierID, &value.ProviderID, &value.BillingUsageRecordID,
			&value.UsagePriceSnapshotID, &value.FundingOperationID, &value.RequestID, &gross, &value.CommissionBPS,
			&commission, &value.ReserveBPS, &reserve, &payable, &value.Currency, &value.UsageSettledAt,
			&value.ReserveReleasableAt, &value.StatementMatched, &value.OpenAppeal, &value.CreatedAt); err != nil {
			return nil, err
		}
		value.GrossAmount, value.CommissionAmount, value.ReserveAmount, value.InitialPayableAmount = domain.Decimal(gross), domain.Decimal(commission), domain.Decimal(reserve), domain.Decimal(payable)
		out = append(out, value)
	}
	return out, rows.Err()
}

const supplierBatchSelect = `SELECT batch.id,batch.batch_number,batch.supplier_id,supplier.display_name,batch.provider_id,provider.name,
	batch.period_start::text,batch.period_end::text,batch.currency,batch.gross_usage_amount::text,batch.commission_amount::text,
	batch.reserve_held_amount::text,batch.adjustment_amount::text,batch.payout_amount::text,batch.status,batch.tax_status,
	batch.invoice_status,batch.provider_statement_id,batch.payout_adapter,batch.payout_region,batch.provider_payout_reference,
	batch.retry_count,batch.max_attempts,batch.next_retry_at,batch.last_failure_code,batch.approved_by,batch.approval_reason,
	batch.approved_at,batch.paid_at,batch.created_at,batch.updated_at
	FROM supplier_settlement_batch batch JOIN supplier_organizations supplier ON supplier.id=batch.supplier_id
	JOIN providers provider ON provider.id=batch.provider_id`

func scanSupplierBatch(row pgx.Row) (domain.SupplierSettlementBatch, error) {
	var value domain.SupplierSettlementBatch
	var gross, commission, reserve, adjustment, payout string
	err := row.Scan(&value.ID, &value.BatchNumber, &value.SupplierID, &value.SupplierName, &value.ProviderID, &value.ProviderName,
		&value.PeriodStart, &value.PeriodEnd, &value.Currency, &gross, &commission, &reserve, &adjustment, &payout,
		&value.Status, &value.TaxStatus, &value.InvoiceStatus, &value.ProviderStatementID, &value.PayoutAdapter,
		&value.PayoutRegion, &value.ProviderPayoutReference, &value.RetryCount, &value.MaxAttempts, &value.NextRetryAt,
		&value.LastFailureCode, &value.ApprovedBy, &value.ApprovalReason, &value.ApprovedAt, &value.PaidAt,
		&value.CreatedAt, &value.UpdatedAt)
	value.GrossUsageAmount, value.CommissionAmount, value.ReserveHeldAmount = domain.Decimal(gross), domain.Decimal(commission), domain.Decimal(reserve)
	value.AdjustmentAmount, value.PayoutAmount = domain.Decimal(adjustment), domain.Decimal(payout)
	return value, err
}

func (s *Store) ListSupplierSettlementBatches(ctx context.Context, supplierID, status string, limit, offset int) ([]domain.SupplierSettlementBatch, error) {
	rows, err := s.pool.Query(ctx, supplierBatchSelect+` WHERE ($1='' OR batch.supplier_id=$1::uuid) AND ($2='' OR batch.status=$2)
		ORDER BY batch.created_at DESC,batch.id LIMIT $3 OFFSET $4`, supplierID, strings.ToUpper(strings.TrimSpace(status)), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupplierSettlementBatch, 0)
	for rows.Next() {
		value, scanErr := scanSupplierBatch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) SupplierSettlementBatchByID(ctx context.Context, batchID string, includeItems bool) (domain.SupplierSettlementBatch, error) {
	value, err := scanSupplierBatch(s.pool.QueryRow(ctx, supplierBatchSelect+` WHERE batch.id=$1`, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	if err != nil || !includeItems {
		return value, err
	}
	rows, err := s.pool.Query(ctx, `SELECT item.id,item.payable_entry_id,item.accrual_id,item.entry_side,item.amount::text,
		COALESCE(accrual.request_id,''),entry.entry_type,item.created_at FROM supplier_settlement_item item
		JOIN supplier_payable_entry entry ON entry.id=item.payable_entry_id
		LEFT JOIN supplier_payable_accrual accrual ON accrual.id=item.accrual_id
		WHERE item.settlement_batch_id=$1 ORDER BY item.created_at,item.id`, batchID)
	if err != nil {
		return value, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.SupplierSettlementItem
		var amount string
		if err = rows.Scan(&item.ID, &item.PayableEntryID, &item.AccrualID, &item.EntrySide, &amount, &item.RequestID, &item.EntryType, &item.CreatedAt); err != nil {
			return value, err
		}
		item.Amount = domain.Decimal(amount)
		value.Items = append(value.Items, item)
	}
	return value, rows.Err()
}

func (s *Store) CreateSupplierSettlementBatch(ctx context.Context, supplierID, providerID string, periodStart, periodEnd time.Time, idempotencyKey string, actor *string) (domain.SupplierSettlementBatch, bool, error) {
	if supplierID == "" || providerID == "" || idempotencyKey == "" || periodEnd.Before(periodStart) {
		return domain.SupplierSettlementBatch{}, false, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	defer tx.Rollback(ctx)
	var policy domain.SupplierSettlementPolicy
	var minimum string
	err = tx.QueryRow(ctx, `SELECT policy.supplier_id,policy.enabled,policy.settlement_cycle,policy.minimum_payout::text,
		policy.commission_bps,policy.risk_reserve_bps,policy.reserve_hold_days,policy.payout_adapter,policy.payout_region,
		policy.tax_verification_required,policy.invoice_required,policy.next_settlement_at,policy.last_period_end::text,
		policy.created_at,policy.updated_at FROM supplier_settlement_policy policy JOIN supplier_organizations supplier ON supplier.id=policy.supplier_id
		WHERE policy.supplier_id=$1 AND supplier.status='APPROVED' AND supplier.kyb_status='VERIFIED' AND supplier.contract_status='ACTIVE'
		AND supplier.payout_account_encrypted IS NOT NULL FOR UPDATE OF policy`, supplierID).Scan(&policy.SupplierID, &policy.Enabled,
		&policy.SettlementCycle, &minimum, &policy.CommissionBPS, &policy.RiskReserveBPS, &policy.ReserveHoldDays,
		&policy.PayoutAdapter, &policy.PayoutRegion, &policy.TaxVerificationRequired, &policy.InvoiceRequired,
		&policy.NextSettlementAt, &policy.LastPeriodEnd, &policy.CreatedAt, &policy.UpdatedAt)
	policy.MinimumPayout = domain.Decimal(minimum)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementBatch{}, false, ErrSupplierPayoutBlocked
	}
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if !policy.Enabled || policy.PayoutAdapter == "disabled" || len(policy.PayoutRegion) != 2 {
		return domain.SupplierSettlementBatch{}, false, ErrSupplierPayoutBlocked
	}
	var replayID, replayKey string
	err = tx.QueryRow(ctx, `SELECT id,idempotency_key FROM supplier_settlement_batch WHERE idempotency_key=$1
		OR (supplier_id=$2 AND provider_id=$3 AND period_start=$4 AND period_end=$5) FOR SHARE`, idempotencyKey, supplierID, providerID,
		periodStart.UTC().Format("2006-01-02"), periodEnd.UTC().Format("2006-01-02")).Scan(&replayID, &replayKey)
	if err == nil {
		if replayKey != idempotencyKey {
			return domain.SupplierSettlementBatch{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.SupplierSettlementBatch{}, false, err
		}
		value, loadErr := s.SupplierSettlementBatchByID(ctx, replayID, true)
		return value, true, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementBatch{}, false, err
	}
	rows, err := tx.Query(ctx, `SELECT entry.id,entry.accrual_id,entry.entry_type,entry.entry_side,entry.amount::text,
		entry.currency,COALESCE(accrual.gross_amount,0)::text,COALESCE(accrual.commission_amount,0)::text,
		COALESCE(accrual.reserve_amount,0)::text
		FROM supplier_payable_entry entry LEFT JOIN supplier_payable_accrual accrual ON accrual.id=entry.accrual_id
		WHERE entry.supplier_id=$1 AND entry.provider_id=$2 AND entry.available_at<$3
		AND entry.currency=(SELECT payout_currency FROM supplier_organizations WHERE id=$1)
		AND entry.entry_type<>'PAYOUT' AND NOT EXISTS(SELECT 1 FROM supplier_settlement_item used
			JOIN supplier_settlement_batch used_batch ON used_batch.id=used.settlement_batch_id
			WHERE used.payable_entry_id=entry.id AND used_batch.status<>'CANCELLED')
		AND NOT EXISTS(SELECT 1 FROM supplier_appeal appeal WHERE appeal.accrual_id=entry.accrual_id AND appeal.status IN ('OPEN','UNDER_REVIEW'))
		ORDER BY entry.available_at,entry.id FOR UPDATE OF entry SKIP LOCKED`, supplierID, providerID, periodEnd.UTC().AddDate(0, 0, 1))
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	type entryValue struct {
		id                                                            string
		accrualID                                                     *string
		entryType, side, amount, currency, gross, commission, reserve string
	}
	entries := make([]entryValue, 0)
	var currency string
	net, gross, commission, reserve := new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)
	for rows.Next() {
		var value entryValue
		if err = rows.Scan(&value.id, &value.accrualID, &value.entryType, &value.side, &value.amount, &value.currency,
			&value.gross, &value.commission, &value.reserve); err != nil {
			rows.Close()
			return domain.SupplierSettlementBatch{}, false, err
		}
		if currency != "" && currency != value.currency {
			continue
		}
		currency = value.currency
		amount, _ := new(big.Rat).SetString(value.amount)
		if value.side == "CREDIT" {
			net.Add(net, amount)
		} else {
			net.Sub(net, amount)
		}
		if value.entryType == "USAGE_ACCRUAL" {
			v, _ := new(big.Rat).SetString(value.gross)
			gross.Add(gross, v)
			v, _ = new(big.Rat).SetString(value.commission)
			commission.Add(commission, v)
			v, _ = new(big.Rat).SetString(value.reserve)
			reserve.Add(reserve, v)
		}
		entries = append(entries, value)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	minimumRat, _ := new(big.Rat).SetString(policy.MinimumPayout.String())
	if len(entries) == 0 || net.Sign() <= 0 || net.Cmp(minimumRat) < 0 {
		return domain.SupplierSettlementBatch{}, false, ErrMinimumSettlement
	}
	batchID := id.UUID()
	batchNumber := financeNumber("ST")
	adjustment := new(big.Rat).Sub(net, new(big.Rat).Sub(new(big.Rat).Sub(gross, commission), reserve))
	taxStatus, invoiceStatus := "PENDING", "MISSING"
	if !policy.TaxVerificationRequired {
		taxStatus = "NOT_REQUIRED"
	}
	if !policy.InvoiceRequired {
		invoiceStatus = "NOT_REQUIRED"
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_batch(id,batch_number,idempotency_key,supplier_id,provider_id,
		period_start,period_end,currency,gross_usage_amount,commission_amount,reserve_held_amount,adjustment_amount,payout_amount,
		tax_status,invoice_status,payout_adapter,payout_region,payout_idempotency_key,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, batchID, batchNumber,
		idempotencyKey, supplierID, providerID, periodStart.UTC().Format("2006-01-02"), periodEnd.UTC().Format("2006-01-02"), currency,
		gross.FloatString(12), commission.FloatString(12), reserve.FloatString(12), adjustment.FloatString(12), net.FloatString(12),
		taxStatus, invoiceStatus, policy.PayoutAdapter, policy.PayoutRegion, "supplier-payout:"+batchID, actor)
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	for _, entry := range entries {
		_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_item(id,settlement_batch_id,payable_entry_id,accrual_id,entry_side,amount)
			VALUES($1,$2,$3,$4,$5,$6)`, id.UUID(), batchID, entry.id, entry.accrualID, entry.side, entry.amount)
		if err != nil {
			return domain.SupplierSettlementBatch{}, false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,to_status,actor_id,metadata)
		VALUES($1,$2,'CREATED','PENDING_APPROVAL',$3,$4)`, id.UUID(), batchID, actor, jsonBytes(map[string]any{"entry_count": len(entries)}))
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if err = writeAuditTx(ctx, tx, actor, "supplier.settlement_created", "supplier_settlement_batch", batchID,
		map[string]any{"supplier_id": supplierID, "provider_id": providerID, "payout_amount": net.FloatString(12), "currency": currency}); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	value, err := s.SupplierSettlementBatchByID(ctx, batchID, true)
	return value, false, err
}

func (s *Store) RunDueSupplierSettlementCycles(ctx context.Context, now time.Time, limit int) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT policy.supplier_id,policy.settlement_cycle,policy.next_settlement_at,
		link.provider_id FROM supplier_settlement_policy policy JOIN supplier_provider_links link ON link.supplier_id=policy.supplier_id AND link.status='ACTIVE'
		WHERE policy.enabled=true AND policy.next_settlement_at<=$1 ORDER BY policy.next_settlement_at,policy.supplier_id LIMIT $2`, now.UTC(), clampFinanceLimitTo(limit, 100))
	if err != nil {
		return 0, err
	}
	type due struct {
		supplierID, cycle, providerID string
		boundary                      time.Time
	}
	items := make([]due, 0)
	for rows.Next() {
		var v due
		if err = rows.Scan(&v.supplierID, &v.cycle, &v.boundary, &v.providerID); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	created := 0
	for _, v := range items {
		start, end := previousSettlementPeriod(v.boundary, v.cycle)
		key := "cycle:" + v.supplierID + ":" + v.providerID + ":" + end.Format("2006-01-02")
		if _, replayed, createErr := s.CreateSupplierSettlementBatch(ctx, v.supplierID, v.providerID, start, end, key, nil); createErr == nil && !replayed {
			created++
		} else if createErr != nil && !errors.Is(createErr, ErrMinimumSettlement) {
			return created, createErr
		}
		next := nextSettlementBoundary(v.boundary.Add(time.Minute), v.cycle)
		_, err = s.pool.Exec(ctx, `UPDATE supplier_settlement_policy SET last_period_end=$2,next_settlement_at=$3,updated_at=now() WHERE supplier_id=$1 AND next_settlement_at=$4`, v.supplierID, end.Format("2006-01-02"), next, v.boundary)
		if err != nil {
			return created, err
		}
	}
	return created, nil
}

func (s *Store) UpdateSupplierSettlementCompliance(ctx context.Context, batchID, taxStatus, invoiceStatus, reason, actor string) (domain.SupplierSettlementBatch, error) {
	taxStatus, invoiceStatus = strings.ToUpper(strings.TrimSpace(taxStatus)), strings.ToUpper(strings.TrimSpace(invoiceStatus))
	if !containsString([]string{"NOT_REQUIRED", "PENDING", "VERIFIED", "REJECTED"}, taxStatus) || !containsString([]string{"NOT_REQUIRED", "MISSING", "SUBMITTED", "APPROVED", "REJECTED"}, invoiceStatus) || strings.TrimSpace(reason) == "" {
		return domain.SupplierSettlementBatch{}, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `UPDATE supplier_settlement_batch SET tax_status=$2,invoice_status=$3,updated_at=now()
		WHERE id=$1 AND status IN ('PENDING_APPROVAL','DISPUTED','FAILED') RETURNING status`, batchID, taxStatus, invoiceStatus).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementBatch{}, ErrSupplierSettlementState
	}
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,from_status,to_status,reason,actor_id,metadata)
		VALUES($1,$2,'COMPLIANCE_UPDATED',$3,$3,$4,$5,$6)`, id.UUID(), batchID, status, reason, actor, jsonBytes(map[string]any{"tax_status": taxStatus, "invoice_status": invoiceStatus}))
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.settlement_compliance_updated", "supplier_settlement_batch", batchID, map[string]any{"tax_status": taxStatus, "invoice_status": invoiceStatus, "reason": reason}); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	return s.SupplierSettlementBatchByID(ctx, batchID, true)
}

func (s *Store) ApproveSupplierSettlement(ctx context.Context, batchID, providerStatementID, reason, actor string) (domain.SupplierSettlementBatch, error) {
	if strings.TrimSpace(reason) == "" || actor == "" {
		return domain.SupplierSettlementBatch{}, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	defer tx.Rollback(ctx)
	var supplierID, providerID, currency, status, taxStatus, invoiceStatus, minimum, payout, createdBy, payoutAdapter string
	var currentStatement *string
	err = tx.QueryRow(ctx, `SELECT batch.supplier_id,batch.provider_id,batch.currency,batch.status,batch.tax_status,batch.invoice_status,
		policy.minimum_payout::text,batch.payout_amount::text,COALESCE(batch.created_by::text,''),batch.provider_statement_id,batch.payout_adapter
		FROM supplier_settlement_batch batch JOIN supplier_settlement_policy policy ON policy.supplier_id=batch.supplier_id
		JOIN supplier_organizations supplier ON supplier.id=batch.supplier_id
		WHERE batch.id=$1 AND policy.enabled=true AND supplier.status='APPROVED' AND supplier.kyb_status='VERIFIED'
		AND supplier.contract_status='ACTIVE' AND supplier.payout_account_encrypted IS NOT NULL FOR UPDATE OF batch`, batchID).
		Scan(&supplierID, &providerID, &currency, &status, &taxStatus, &invoiceStatus, &minimum, &payout, &createdBy, &currentStatement, &payoutAdapter)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
	}
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	payoutDecimal, decimalErr := domain.ParseDecimal(payout)
	if decimalErr != nil {
		return domain.SupplierSettlementBatch{}, decimalErr
	}
	payoutNegative, decimalErr := payoutDecimal.IsNegative()
	if decimalErr != nil {
		return domain.SupplierSettlementBatch{}, decimalErr
	}
	payoutPositive, decimalErr := payoutDecimal.IsPositive()
	if decimalErr != nil {
		return domain.SupplierSettlementBatch{}, decimalErr
	}
	if status != "PENDING_APPROVAL" && status != "DISPUTED" && status != "FAILED" || createdBy == actor || payoutNegative || !payoutPositive {
		return domain.SupplierSettlementBatch{}, ErrSupplierSettlementState
	}
	minRat, _ := new(big.Rat).SetString(minimum)
	payoutRat, _ := new(big.Rat).SetString(payout)
	if payoutRat.Cmp(minRat) < 0 {
		return domain.SupplierSettlementBatch{}, ErrMinimumSettlement
	}
	var taxRequired, invoiceRequired bool
	if err = tx.QueryRow(ctx, `SELECT tax_verification_required,invoice_required FROM supplier_settlement_policy WHERE supplier_id=$1`, supplierID).Scan(&taxRequired, &invoiceRequired); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if taxRequired && taxStatus != "VERIFIED" || invoiceRequired && invoiceStatus != "APPROVED" {
		return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
	}
	if payoutAdapter != "sandbox" {
		ready, readyErr := s.productionPayoutReadyTx(ctx, tx, supplierID)
		if readyErr != nil {
			return domain.SupplierSettlementBatch{}, readyErr
		}
		if !ready {
			return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
		}
	}
	statementID := strings.TrimSpace(providerStatementID)
	if statementID == "" && currentStatement != nil {
		statementID = *currentStatement
	}
	var unmatched int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM supplier_settlement_item item JOIN supplier_payable_entry entry ON entry.id=item.payable_entry_id
		WHERE item.settlement_batch_id=$1 AND entry.entry_type IN ('USAGE_ACCRUAL','RESERVE_RELEASE')
		AND NOT EXISTS(SELECT 1 FROM supplier_usage_statement_match matched WHERE matched.accrual_id=item.accrual_id)`, batchID).Scan(&unmatched); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if unmatched > 0 && statementID == "" {
		return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
	}
	if statementID != "" {
		var statementProvider, statementCurrency string
		if err = tx.QueryRow(ctx, `SELECT provider_id,currency FROM provider_statement WHERE id=$1 FOR SHARE`, statementID).Scan(&statementProvider, &statementCurrency); errors.Is(err, pgx.ErrNoRows) {
			return domain.SupplierSettlementBatch{}, ErrNotFound
		} else if err != nil {
			return domain.SupplierSettlementBatch{}, err
		}
		if statementProvider != providerID || statementCurrency != currency {
			return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
		}
		rows, matchErr := tx.Query(ctx, `SELECT DISTINCT item.accrual_id,line.id,accrual.gross_amount::text
			FROM supplier_settlement_item item JOIN supplier_payable_accrual accrual ON accrual.id=item.accrual_id
			JOIN provider_statement_line line ON line.provider_statement_id=$2 AND line.request_id=accrual.request_id
				AND line.amount=accrual.gross_amount AND line.currency=accrual.currency
			WHERE item.settlement_batch_id=$1 AND NOT EXISTS(SELECT 1 FROM supplier_usage_statement_match matched WHERE matched.accrual_id=item.accrual_id)`, batchID, statementID)
		if matchErr != nil {
			return domain.SupplierSettlementBatch{}, matchErr
		}
		type statementMatch struct{ accrualID, lineID, amount string }
		matches := make([]statementMatch, 0, unmatched)
		for rows.Next() {
			var match statementMatch
			if err = rows.Scan(&match.accrualID, &match.lineID, &match.amount); err != nil {
				rows.Close()
				return domain.SupplierSettlementBatch{}, err
			}
			matches = append(matches, match)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return domain.SupplierSettlementBatch{}, err
		}
		for _, match := range matches {
			_, err = tx.Exec(ctx, `INSERT INTO supplier_usage_statement_match(id,accrual_id,provider_statement_id,provider_statement_line_id,matched_amount,currency,matched_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(accrual_id) DO NOTHING`, id.UUID(), match.accrualID, statementID, match.lineID, match.amount, currency, actor)
			if err != nil {
				return domain.SupplierSettlementBatch{}, err
			}
		}
		if len(matches) < unmatched {
			return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
		}
	}
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM supplier_settlement_item item JOIN supplier_payable_accrual accrual ON accrual.id=item.accrual_id
		JOIN billing_usage_records usage ON usage.id=accrual.billing_usage_record_id
		JOIN funding_operation operation ON operation.id=accrual.funding_operation_id
		WHERE item.settlement_batch_id=$1 AND (usage.status<>'CHARGED' OR operation.status NOT IN ('SETTLED','PARTIALLY_SETTLED')
		OR NOT EXISTS(SELECT 1 FROM supplier_usage_statement_match matched WHERE matched.accrual_id=accrual.id)
			OR EXISTS(SELECT 1 FROM supplier_appeal appeal WHERE (appeal.accrual_id=accrual.id OR appeal.settlement_batch_id=$1)
				AND appeal.status IN ('OPEN','UNDER_REVIEW'))))`, batchID).Scan(&blocked)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if blocked {
		return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status='APPROVED',provider_statement_id=COALESCE($2::uuid,provider_statement_id),
		approved_by=$3,approval_reason=$4,approved_at=now(),next_retry_at=now(),last_failure_code='',updated_at=now() WHERE id=$1`, batchID, nullString(statementID), actor, reason)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,from_status,to_status,reason,actor_id) VALUES($1,$2,'APPROVED',$3,'APPROVED',$4,$5)`, id.UUID(), batchID, status, reason, actor)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.settlement_approved", "supplier_settlement_batch", batchID, map[string]any{"reason": reason, "provider_statement_id": statementID, "payout_amount": payout, "currency": currency}); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	return s.SupplierSettlementBatchByID(ctx, batchID, true)
}

func (s *Store) ClaimSupplierPayout(ctx context.Context, now time.Time) (SupplierPayoutJob, error) {
	_ = now // Queue eligibility uses the database clock shared by every replica.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SupplierPayoutJob{}, err
	}
	defer tx.Rollback(ctx)
	var job SupplierPayoutJob
	var amount string
	err = tx.QueryRow(ctx, `SELECT batch.id,batch.batch_number,batch.supplier_id,supplier.display_name,batch.provider_id,provider.name,
		batch.period_start::text,batch.period_end::text,batch.currency,batch.gross_usage_amount::text,batch.commission_amount::text,
		batch.reserve_held_amount::text,batch.adjustment_amount::text,batch.payout_amount::text,batch.status,batch.tax_status,
		batch.invoice_status,batch.provider_statement_id,batch.payout_adapter,batch.payout_region,batch.provider_payout_reference,
		batch.retry_count,batch.max_attempts,batch.next_retry_at,batch.last_failure_code,batch.approved_by,batch.approval_reason,
		batch.approved_at,batch.paid_at,batch.created_at,batch.updated_at,supplier.payout_account_encrypted,supplier.owner_user_id,
		batch.payout_amount::text
		FROM supplier_settlement_batch batch JOIN supplier_organizations supplier ON supplier.id=batch.supplier_id
		JOIN providers provider ON provider.id=batch.provider_id
		WHERE batch.status IN ('APPROVED','FAILED') AND COALESCE(batch.next_retry_at,now())<=now() AND batch.retry_count<batch.max_attempts
		AND (batch.payout_adapter='sandbox' OR EXISTS(SELECT 1 FROM supplier_payout_readiness_review readiness
			WHERE readiness.supplier_id=batch.supplier_id AND readiness.production_payout_enabled
			AND supplier.contract_status='ACTIVE' AND supplier.kyb_status='VERIFIED'
			AND supplier.contract_start_at IS NOT NULL AND supplier.contract_start_at<=now()
			AND (supplier.contract_end_at IS NULL OR supplier.contract_end_at>now())
			AND supplier.tax_id<>'' AND supplier.tax_country<>'' AND supplier.tax_form_type<>''
			AND supplier.payout_account_encrypted IS NOT NULL
			AND EXISTS(SELECT 1 FROM supplier_security_questionnaires questionnaire
				WHERE questionnaire.supplier_id=supplier.id AND questionnaire.status='APPROVED')))
		AND NOT EXISTS(SELECT 1 FROM supplier_appeal appeal LEFT JOIN supplier_settlement_item item ON item.accrual_id=appeal.accrual_id AND item.settlement_batch_id=batch.id
			WHERE (appeal.settlement_batch_id=batch.id OR item.id IS NOT NULL) AND appeal.status IN ('OPEN','UNDER_REVIEW'))
		ORDER BY batch.next_retry_at,batch.created_at FOR UPDATE OF batch SKIP LOCKED LIMIT 1`).Scan(
		&job.Batch.ID, &job.Batch.BatchNumber, &job.Batch.SupplierID, &job.Batch.SupplierName, &job.Batch.ProviderID, &job.Batch.ProviderName,
		&job.Batch.PeriodStart, &job.Batch.PeriodEnd, &job.Batch.Currency, &job.Batch.GrossUsageAmount, &job.Batch.CommissionAmount,
		&job.Batch.ReserveHeldAmount, &job.Batch.AdjustmentAmount, &job.Batch.PayoutAmount, &job.Batch.Status, &job.Batch.TaxStatus,
		&job.Batch.InvoiceStatus, &job.Batch.ProviderStatementID, &job.Batch.PayoutAdapter, &job.Batch.PayoutRegion, &job.Batch.ProviderPayoutReference,
		&job.Batch.RetryCount, &job.Batch.MaxAttempts, &job.Batch.NextRetryAt, &job.Batch.LastFailureCode, &job.Batch.ApprovedBy, &job.Batch.ApprovalReason,
		&job.Batch.ApprovedAt, &job.Batch.PaidAt, &job.Batch.CreatedAt, &job.Batch.UpdatedAt, &job.PayoutAccount, &job.PayoutAccountOwner, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return SupplierPayoutJob{}, ErrNotFound
	}
	if err != nil {
		return SupplierPayoutJob{}, err
	}
	job.Batch.PayoutAmount = domain.Decimal(amount)
	job.AttemptID = id.UUID()
	job.AttemptNo = job.Batch.RetryCount + 1
	_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status='PROCESSING',retry_count=retry_count+1,next_retry_at=NULL,updated_at=now() WHERE id=$1`, job.Batch.ID)
	if err != nil {
		return SupplierPayoutJob{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_payout_attempt(id,settlement_batch_id,attempt_no,adapter,idempotency_key) VALUES($1,$2,$3,$4,$5)`, job.AttemptID, job.Batch.ID, job.AttemptNo, job.Batch.PayoutAdapter, "supplier-payout:"+job.Batch.ID)
	if err != nil {
		return SupplierPayoutJob{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,from_status,to_status,metadata) VALUES($1,$2,'PAYOUT_STARTED',$3,'PROCESSING',$4)`, id.UUID(), job.Batch.ID, job.Batch.Status, jsonBytes(map[string]any{"attempt_no": job.AttemptNo, "adapter": job.Batch.PayoutAdapter}))
	if err != nil {
		return SupplierPayoutJob{}, err
	}
	if err = writeAuditTx(ctx, tx, nil, "supplier.payout_started", "supplier_settlement_batch", job.Batch.ID,
		map[string]any{"attempt_no": job.AttemptNo, "adapter": job.Batch.PayoutAdapter}); err != nil {
		return SupplierPayoutJob{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SupplierPayoutJob{}, err
	}
	job.Batch.Status = "PROCESSING"
	return job, nil
}

func (s *Store) FailSupplierPayout(ctx context.Context, batchID, attemptID, failureCode string, now time.Time) error {
	if failureCode == "" {
		failureCode = "adapter_failed"
	}
	if len(failureCode) > 200 {
		failureCode = failureCode[:200]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var retryCount, maxAttempts int
	var batchStatus string
	err = tx.QueryRow(ctx, `SELECT retry_count,max_attempts,status FROM supplier_settlement_batch WHERE id=$1 FOR UPDATE`, batchID).Scan(&retryCount, &maxAttempts, &batchStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if batchStatus == "FAILED" {
		var attemptStatus, existingCode string
		if err = tx.QueryRow(ctx, `SELECT status,failure_code FROM supplier_payout_attempt WHERE id=$1 AND settlement_batch_id=$2`, attemptID, batchID).Scan(&attemptStatus, &existingCode); err != nil {
			return err
		}
		if attemptStatus != "FAILED" || existingCode != failureCode {
			return ErrIdempotencyConflict
		}
		return tx.Commit(ctx)
	}
	if batchStatus != "PROCESSING" {
		return ErrSupplierSettlementState
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_payout_attempt SET status='FAILED',failure_code=$2,finished_at=$3 WHERE id=$1 AND settlement_batch_id=$4 AND status='STARTED'`, attemptID, failureCode, now.UTC(), batchID)
	if err != nil {
		return err
	}
	var next any
	if retryCount < maxAttempts {
		backoff := time.Minute * time.Duration(1<<min(retryCount-1, 8))
		next = now.UTC().Add(backoff)
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status='FAILED',last_failure_code=$2,next_retry_at=$3,updated_at=now() WHERE id=$1`, batchID, failureCode, next)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,from_status,to_status,reason,metadata) VALUES($1,$2,'PAYOUT_FAILED','PROCESSING','FAILED',$3,$4)`, id.UUID(), batchID, failureCode, jsonBytes(map[string]any{"retry_count": retryCount, "retry_scheduled": next != nil}))
	if err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, nil, "supplier.payout_failed", "supplier_settlement_batch", batchID,
		map[string]any{"failure_code": failureCode, "retry_count": retryCount, "retry_scheduled": next != nil}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteSupplierPayout(ctx context.Context, batchID, attemptID, providerReference string, metadata map[string]any, now time.Time) (domain.SupplierSettlementBatch, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	defer tx.Rollback(ctx)
	var supplierID, currency, status, payout, commission, existingReference, payoutAdapter string
	err = tx.QueryRow(ctx, `SELECT supplier_id,currency,status,payout_amount::text,commission_amount::text,provider_payout_reference,payout_adapter FROM supplier_settlement_batch WHERE id=$1 FOR UPDATE`, batchID).Scan(&supplierID, &currency, &status, &payout, &commission, &existingReference, &payoutAdapter)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementBatch{}, false, ErrNotFound
	}
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if status == "PAID" {
		if existingReference != providerReference {
			return domain.SupplierSettlementBatch{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.SupplierSettlementBatch{}, false, err
		}
		v, e := s.SupplierSettlementBatchByID(ctx, batchID, true)
		return v, true, e
	}
	if status != "PROCESSING" || providerReference == "" {
		return domain.SupplierSettlementBatch{}, false, ErrSupplierSettlementState
	}
	if payoutAdapter != "sandbox" {
		ready, readyErr := s.productionPayoutReadyTx(ctx, tx, supplierID)
		if readyErr != nil {
			return domain.SupplierSettlementBatch{}, false, readyErr
		}
		if !ready {
			return domain.SupplierSettlementBatch{}, false, ErrSupplierPayoutBlocked
		}
	}
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM supplier_settlement_item item JOIN supplier_payable_accrual accrual ON accrual.id=item.accrual_id
		JOIN billing_usage_records usage ON usage.id=accrual.billing_usage_record_id JOIN funding_operation operation ON operation.id=accrual.funding_operation_id
		WHERE item.settlement_batch_id=$1 AND (usage.status<>'CHARGED' OR operation.status NOT IN ('SETTLED','PARTIALLY_SETTLED')
		OR NOT EXISTS(SELECT 1 FROM supplier_usage_statement_match matched WHERE matched.accrual_id=accrual.id)
		OR EXISTS(SELECT 1 FROM supplier_appeal appeal WHERE (appeal.accrual_id=accrual.id OR appeal.settlement_batch_id=$1)
			AND appeal.status IN ('OPEN','UNDER_REVIEW') AND appeal.created_at<=(SELECT started_at FROM supplier_payout_attempt WHERE id=$2))))`, batchID, attemptID).Scan(&blocked)
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if blocked {
		return domain.SupplierSettlementBatch{}, false, ErrSupplierPayoutBlocked
	}
	var refund string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(item.amount) FILTER(WHERE entry.entry_type='REFUND_SHARE' AND item.entry_side='DEBIT'),0)::text FROM supplier_settlement_item item JOIN supplier_payable_entry entry ON entry.id=item.payable_entry_id WHERE item.settlement_batch_id=$1`, batchID).Scan(&refund); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	payoutRat, _ := new(big.Rat).SetString(payout)
	commissionRat, _ := new(big.Rat).SetString(commission)
	refundRat, _ := new(big.Rat).SetString(refund)
	debit := new(big.Rat).Add(payoutRat, new(big.Rat).Add(commissionRat, refundRat))
	for _, account := range []struct{ key, name, typ, side string }{
		{systemAccountKey("provider-payable", currency), "Provider payable", "LIABILITY", "CREDIT"},
		{systemAccountKey("cash", currency), "Platform cash", "ASSET", "DEBIT"},
		{systemAccountKey("supplier-commission", currency), "Supplier commission revenue", "REVENUE", "CREDIT"},
		{systemAccountKey("supplier-refund-recovery", currency), "Supplier refund recovery", "REVENUE", "CREDIT"},
	} {
		_, err = tx.Exec(ctx, `INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency) VALUES($1,$2,$3,$4,$5) ON CONFLICT(account_key) DO NOTHING`, account.key, account.name, account.typ, account.side, currency)
		if err != nil {
			return domain.SupplierSettlementBatch{}, false, err
		}
	}
	journalID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO ledger_journal(id,journal_type,external_key,currency,reference,metadata,supplier_id,supplier_settlement_batch_id)
		VALUES($1,'SUPPLIER_PAYOUT',$2,$3,$4,$5,$6,$7)`, journalID, "supplier-payout:"+batchID, currency, providerReference, jsonBytes(map[string]any{"adapter_response": metadata}), supplierID, batchID)
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	lines := []journalLine{{systemAccountKey("provider-payable", currency), "DEBIT", debit.FloatString(12), "Settle approved supplier payable"}, {systemAccountKey("cash", currency), "CREDIT", payout, "Supplier payout"}}
	if commissionRat.Sign() > 0 {
		lines = append(lines, journalLine{systemAccountKey("supplier-commission", currency), "CREDIT", commission, "Platform commission"})
	}
	if refundRat.Sign() > 0 {
		lines = append(lines, journalLine{systemAccountKey("supplier-refund-recovery", currency), "CREDIT", refund, "Supplier refund share"})
	}
	for _, line := range lines {
		var accountID string
		if err = tx.QueryRow(ctx, `SELECT id FROM ledger_account WHERE account_key=$1`, line.accountKey).Scan(&accountID); err != nil {
			return domain.SupplierSettlementBatch{}, false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger_journal_entry(id,journal_id,account_id,currency,entry_side,amount,description) VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), journalID, accountID, currency, line.side, line.amount, line.description)
		if err != nil {
			return domain.SupplierSettlementBatch{}, false, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE ledger_journal SET status='POSTED',posted_at=$2 WHERE id=$1`, journalID, now.UTC()); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_payable_entry(id,idempotency_key,supplier_id,provider_id,settlement_batch_id,entry_type,entry_side,amount,currency,available_at,reference,metadata)
		SELECT gen_random_uuid(),'payout:'||id,supplier_id,provider_id,id,'PAYOUT','DEBIT',payout_amount,currency,$2,$3,$4 FROM supplier_settlement_batch WHERE id=$1`, batchID, now.UTC(), providerReference, jsonBytes(metadata))
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_payout_attempt SET status='SUCCEEDED',provider_reference=$2,response_metadata=$3,finished_at=$4 WHERE id=$1 AND settlement_batch_id=$5 AND status='STARTED'`, attemptID, providerReference, jsonBytes(metadata), now.UTC(), batchID)
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status='PAID',provider_payout_reference=$2,paid_at=$3,next_retry_at=NULL,last_failure_code='',updated_at=now() WHERE id=$1`, batchID, providerReference, now.UTC())
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,from_status,to_status,metadata) VALUES($1,$2,'PAID','PROCESSING','PAID',$3)`, id.UUID(), batchID, jsonBytes(map[string]any{"provider_reference": providerReference, "journal_id": journalID}))
	if err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if err = writeAuditTx(ctx, tx, nil, "supplier.payout_completed", "supplier_settlement_batch", batchID, map[string]any{"payout_amount": payout, "currency": currency, "journal_id": journalID}); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierSettlementBatch{}, false, err
	}
	v, e := s.SupplierSettlementBatchByID(ctx, batchID, true)
	return v, false, e
}

func (s *Store) RetrySupplierSettlement(ctx context.Context, batchID, reason, actor string) (domain.SupplierSettlementBatch, error) {
	if strings.TrimSpace(reason) == "" {
		return domain.SupplierSettlementBatch{}, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	defer tx.Rollback(ctx)
	var retryCount, maxAttempts int
	err = tx.QueryRow(ctx, `SELECT retry_count,max_attempts FROM supplier_settlement_batch WHERE id=$1 AND status='FAILED' FOR UPDATE`, batchID).Scan(&retryCount, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierSettlementBatch{}, ErrSupplierSettlementState
	}
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if retryCount >= 100 {
		return domain.SupplierSettlementBatch{}, ErrSupplierPayoutBlocked
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status='APPROVED',max_attempts=GREATEST(max_attempts,retry_count+1),next_retry_at=now(),last_failure_code='',updated_at=now() WHERE id=$1`, batchID)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_event(id,settlement_batch_id,event_type,from_status,to_status,reason,actor_id) VALUES($1,$2,'RETRY_APPROVED','FAILED','APPROVED',$3,$4)`, id.UUID(), batchID, reason, actor)
	if err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.payout_retry_approved", "supplier_settlement_batch", batchID, map[string]any{"reason": reason, "retry_count": retryCount}); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierSettlementBatch{}, err
	}
	return s.SupplierSettlementBatchByID(ctx, batchID, true)
}

func (s *Store) CreateSupplierRefundShare(ctx context.Context, accrualID, amount, reference, idempotencyKey, actor string) (domain.SupplierPayableEntry, bool, error) {
	refundAmount, decimalErr := domain.ParseDecimal(amount)
	amountPositive := false
	if decimalErr == nil {
		amountPositive, decimalErr = refundAmount.IsPositive()
	}
	if decimalErr != nil || !amountPositive || strings.TrimSpace(reference) == "" || strings.TrimSpace(idempotencyKey) == "" {
		return domain.SupplierPayableEntry{}, false, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "supplier-refund:"+idempotencyKey); err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	var supplierID, providerID, currency, gross, commission string
	err = tx.QueryRow(ctx, `SELECT supplier_id,provider_id,currency,gross_amount::text,commission_amount::text FROM supplier_payable_accrual WHERE id=$1 FOR SHARE`, accrualID).Scan(&supplierID, &providerID, &currency, &gross, &commission)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierPayableEntry{}, false, ErrNotFound
	}
	if err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	var existingID, existingAmount string
	err = tx.QueryRow(ctx, `SELECT id,amount::text FROM supplier_payable_entry WHERE idempotency_key=$1 FOR SHARE`, idempotencyKey).Scan(&existingID, &existingAmount)
	if err == nil {
		if existingAmount != domain.Decimal(amount).String() {
			return domain.SupplierPayableEntry{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.SupplierPayableEntry{}, false, err
		}
		v, e := s.SupplierPayableEntryByID(ctx, existingID)
		return v, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierPayableEntry{}, false, err
	}
	var prior string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount),0)::text FROM supplier_payable_entry WHERE accrual_id=$1 AND entry_type='REFUND_SHARE'`, accrualID).Scan(&prior); err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	allowed, _ := new(big.Rat).SetString(gross)
	comm, _ := new(big.Rat).SetString(commission)
	allowed.Sub(allowed, comm)
	used, _ := new(big.Rat).SetString(prior)
	requested, _ := new(big.Rat).SetString(amount)
	used.Add(used, requested)
	if used.Cmp(allowed) > 0 {
		return domain.SupplierPayableEntry{}, false, ErrSupplierPayoutBlocked
	}
	entryID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO supplier_payable_entry(id,idempotency_key,supplier_id,provider_id,accrual_id,entry_type,entry_side,amount,currency,available_at,reference,metadata,created_by) VALUES($1,$2,$3,$4,$5,'REFUND_SHARE','DEBIT',$6,$7,now(),$8,$9,$10)`, entryID, idempotencyKey, supplierID, providerID, accrualID, amount, currency, reference, jsonBytes(map[string]any{"allocation_source": "platform_review"}), actor)
	if err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	var batchID, status string
	err = tx.QueryRow(ctx, `SELECT batch.id,batch.status FROM supplier_settlement_item item JOIN supplier_settlement_batch batch ON batch.id=item.settlement_batch_id WHERE item.accrual_id=$1 AND batch.status NOT IN ('PAID','PROCESSING') ORDER BY batch.created_at DESC LIMIT 1 FOR UPDATE OF batch`, accrualID).Scan(&batchID, &status)
	if err == nil {
		_, err = tx.Exec(ctx, `INSERT INTO supplier_settlement_item(id,settlement_batch_id,payable_entry_id,accrual_id,entry_side,amount) VALUES($1,$2,$3,$4,'DEBIT',$5)`, id.UUID(), batchID, entryID, accrualID, amount)
		if err != nil {
			return domain.SupplierPayableEntry{}, false, err
		}
		_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET adjustment_amount=adjustment_amount-$2::numeric,
			payout_amount=CASE WHEN payout_amount>$2::numeric THEN payout_amount-$2::numeric ELSE payout_amount END,
			status=CASE WHEN payout_amount<=$2::numeric THEN 'CANCELLED' WHEN status IN ('APPROVED','FAILED') THEN 'DISPUTED' ELSE status END,
			approved_by=CASE WHEN payout_amount<=$2::numeric OR status IN ('APPROVED','FAILED') THEN NULL ELSE approved_by END,
			approved_at=CASE WHEN payout_amount<=$2::numeric OR status IN ('APPROVED','FAILED') THEN NULL ELSE approved_at END,
			approval_reason=CASE WHEN payout_amount<=$2::numeric OR status IN ('APPROVED','FAILED') THEN '' ELSE approval_reason END,
			updated_at=now() WHERE id=$1`, batchID, amount)
		if err != nil {
			return domain.SupplierPayableEntry{}, false, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierPayableEntry{}, false, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.refund_share_allocated", "supplier_payable_accrual", accrualID, map[string]any{"amount": amount, "currency": currency, "reference": reference}); err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierPayableEntry{}, false, err
	}
	v, e := s.SupplierPayableEntryByID(ctx, entryID)
	return v, false, e
}

func (s *Store) SupplierPayableEntryByID(ctx context.Context, entryID string) (domain.SupplierPayableEntry, error) {
	var v domain.SupplierPayableEntry
	var amount string
	var metadata []byte
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,provider_id,accrual_id,settlement_batch_id,entry_type,entry_side,amount::text,currency,available_at,reference,metadata,created_at FROM supplier_payable_entry WHERE id=$1`, entryID).Scan(&v.ID, &v.SupplierID, &v.ProviderID, &v.AccrualID, &v.SettlementBatchID, &v.EntryType, &v.EntrySide, &amount, &v.Currency, &v.AvailableAt, &v.Reference, &metadata, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	v.Amount = domain.Decimal(amount)
	_ = json.Unmarshal(metadata, &v.Metadata)
	return v, err
}

func (s *Store) ImportSupplierBill(ctx context.Context, input SupplierBillInput) (domain.SupplierBill, bool, error) {
	input.SupplierID = strings.TrimSpace(input.SupplierID)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.BillReference = strings.TrimSpace(input.BillReference)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.SourceSHA256 = strings.ToLower(strings.TrimSpace(input.SourceSHA256))
	if input.SupplierID == "" || input.ProviderID == "" || input.BillReference == "" || len(input.Currency) != 3 || len(input.SourceSHA256) != 64 || input.DeclaredBy == "" || input.PeriodEnd.Before(input.PeriodStart) {
		return domain.SupplierBill{}, false, ErrSupplierSettlementState
	}
	total := new(big.Rat)
	for _, line := range input.Lines {
		amount, ok := new(big.Rat).SetString(line.Amount)
		if !ok || amount.Sign() < 0 || strings.ToUpper(line.Currency) != input.Currency || line.ExternalLineID == "" || (line.RequestID == "" && line.UpstreamRequestID == "") {
			return domain.SupplierBill{}, false, ErrSupplierSettlementState
		}
		total.Add(total, amount)
	}
	if !decimalEqual(total.FloatString(12), input.TotalAmount) {
		return domain.SupplierBill{}, false, ErrSupplierBillMismatch
	}
	payload := struct {
		SupplierID, ProviderID, BillReference, PeriodStart, PeriodEnd, Currency, TotalAmount, SourceSHA256 string
		Lines                                                                                              []SupplierBillLineInput
	}{input.SupplierID, input.ProviderID, input.BillReference, input.PeriodStart.UTC().Format("2006-01-02"), input.PeriodEnd.UTC().Format("2006-01-02"), input.Currency, total.FloatString(12), input.SourceSHA256, input.Lines}
	b, _ := json.Marshal(payload)
	h := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(h[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierBill{}, false, err
	}
	defer tx.Rollback(ctx)
	var existing, existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,import_fingerprint_sha256 FROM supplier_bill WHERE supplier_id=$1 AND provider_id=$2 AND (bill_reference=$3 OR source_sha256=$4) FOR SHARE`, input.SupplierID, input.ProviderID, input.BillReference, input.SourceSHA256).Scan(&existing, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return domain.SupplierBill{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.SupplierBill{}, false, err
		}
		v, e := s.SupplierBillByID(ctx, existing, true)
		return v, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierBill{}, false, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM supplier_provider_links link JOIN supplier_organizations supplier ON supplier.id=link.supplier_id WHERE link.supplier_id=$1 AND link.provider_id=$2 AND link.status='ACTIVE' AND supplier.status='APPROVED' AND supplier.contract_status='ACTIVE')`, input.SupplierID, input.ProviderID).Scan(&allowed); err != nil {
		return domain.SupplierBill{}, false, err
	}
	if !allowed {
		return domain.SupplierBill{}, false, ErrSupplierPayoutBlocked
	}
	billID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO supplier_bill(id,supplier_id,provider_id,bill_reference,period_start,period_end,currency,total_amount,source_sha256,import_fingerprint_sha256,declared_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, billID, input.SupplierID, input.ProviderID, input.BillReference, input.PeriodStart, input.PeriodEnd, input.Currency, total.FloatString(12), input.SourceSHA256, fingerprint, input.DeclaredBy)
	if err != nil {
		return domain.SupplierBill{}, false, err
	}
	for _, line := range input.Lines {
		_, err = tx.Exec(ctx, `INSERT INTO supplier_bill_line(id,supplier_bill_id,external_line_id,request_id,upstream_request_id,usage_date,input_tokens,cached_input_tokens,output_tokens,amount,currency,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id.UUID(), billID, line.ExternalLineID, nullString(line.RequestID), nullString(line.UpstreamRequestID), line.UsageDate, line.InputTokens, line.CachedInputTokens, line.OutputTokens, line.Amount, input.Currency, jsonBytes(line.Metadata))
		if err != nil {
			return domain.SupplierBill{}, false, err
		}
	}
	if err = writeAuditTx(ctx, tx, &input.DeclaredBy, "supplier.bill_declared", "supplier_bill", billID, map[string]any{"supplier_id": input.SupplierID, "provider_id": input.ProviderID, "total_amount": total.FloatString(12), "currency": input.Currency, "line_count": len(input.Lines), "payout_source": false}); err != nil {
		return domain.SupplierBill{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierBill{}, false, err
	}
	v, e := s.SupplierBillByID(ctx, billID, true)
	return v, false, e
}

func (s *Store) SupplierBillByID(ctx context.Context, billID string, includeLines bool) (domain.SupplierBill, error) {
	var v domain.SupplierBill
	var amount string
	err := s.pool.QueryRow(ctx, `SELECT id,supplier_id,provider_id,bill_reference,period_start::text,period_end::text,currency,total_amount::text,source_sha256,status,declared_by,declared_at FROM supplier_bill WHERE id=$1`, billID).Scan(&v.ID, &v.SupplierID, &v.ProviderID, &v.BillReference, &v.PeriodStart, &v.PeriodEnd, &v.Currency, &amount, &v.SourceSHA256, &v.Status, &v.DeclaredBy, &v.DeclaredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.TotalAmount = domain.Decimal(amount)
	if !includeLines {
		return v, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT id,external_line_id,COALESCE(request_id,''),COALESCE(upstream_request_id,''),usage_date::text,input_tokens,cached_input_tokens,output_tokens,amount::text,currency,metadata FROM supplier_bill_line WHERE supplier_bill_id=$1 ORDER BY created_at,id`, billID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var line domain.SupplierBillLine
		var a string
		var meta []byte
		if err = rows.Scan(&line.ID, &line.ExternalLineID, &line.RequestID, &line.UpstreamRequestID, &line.UsageDate, &line.InputTokens, &line.CachedInputTokens, &line.OutputTokens, &a, &line.Currency, &meta); err != nil {
			return v, err
		}
		line.Amount = domain.Decimal(a)
		_ = json.Unmarshal(meta, &line.Metadata)
		v.Lines = append(v.Lines, line)
	}
	return v, rows.Err()
}

func (s *Store) ListSupplierBills(ctx context.Context, supplierID, status string, limit, offset int) ([]domain.SupplierBill, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM supplier_bill WHERE ($1='' OR supplier_id=$1::uuid) AND ($2='' OR status=$2) ORDER BY declared_at DESC,id LIMIT $3 OFFSET $4`, supplierID, strings.ToUpper(strings.TrimSpace(status)), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.SupplierBill, 0, len(ids))
	for _, billID := range ids {
		v, e := s.SupplierBillByID(ctx, billID, false)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Store) ReconcileSupplierBill(ctx context.Context, billID, actor string) (domain.SupplierBill, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierBill{}, err
	}
	defer tx.Rollback(ctx)
	var differences int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM supplier_bill_line line LEFT JOIN supplier_payable_accrual accrual ON accrual.request_id=line.request_id AND accrual.supplier_id=(SELECT supplier_id FROM supplier_bill WHERE id=$1) WHERE line.supplier_bill_id=$1 AND (accrual.id IS NULL OR accrual.gross_amount<>line.amount OR accrual.currency<>line.currency)`, billID).Scan(&differences)
	if err != nil {
		return domain.SupplierBill{}, err
	}
	status := "RECONCILED"
	if differences > 0 {
		status = "DISCREPANT"
	}
	tag, err := tx.Exec(ctx, `UPDATE supplier_bill SET status=$2 WHERE id=$1 AND status IN ('DECLARED','RECONCILED','DISCREPANT')`, billID, status)
	if err != nil {
		return domain.SupplierBill{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.SupplierBill{}, ErrSupplierSettlementState
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.bill_reconciled", "supplier_bill", billID, map[string]any{"status": status, "differences": differences, "supplier_declared_only": true}); err != nil {
		return domain.SupplierBill{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierBill{}, err
	}
	return s.SupplierBillByID(ctx, billID, true)
}

func (s *Store) CreateSupplierAppeal(ctx context.Context, value domain.SupplierAppeal, actor string) (domain.SupplierAppeal, bool, error) {
	value.AppealType = strings.ToUpper(strings.TrimSpace(value.AppealType))
	if value.SupplierID == "" || value.Reason == "" || value.ID == "" || value.AppealNumber == "" {
		return domain.SupplierAppeal{}, false, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierAppeal{}, false, err
	}
	defer tx.Rollback(ctx)
	var existing, existingReason string
	err = tx.QueryRow(ctx, `SELECT id,reason FROM supplier_appeal WHERE supplier_id=$1 AND idempotency_key=$2 FOR SHARE`, value.SupplierID, value.ID).Scan(&existing, &existingReason)
	if err == nil {
		if existingReason != value.Reason {
			return domain.SupplierAppeal{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.SupplierAppeal{}, false, err
		}
		v, e := s.SupplierAppealByID(ctx, existing)
		return v, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierAppeal{}, false, err
	}
	valueID := id.UUID()
	appealNumber := financeNumber("SA")
	_, err = tx.Exec(ctx, `INSERT INTO supplier_appeal(id,appeal_number,idempotency_key,supplier_id,appeal_type,accrual_id,settlement_batch_id,supplier_bill_id,reconciliation_case_id,reason,evidence,submitted_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, valueID, appealNumber, value.ID, value.SupplierID, value.AppealType, value.AccrualID, value.SettlementBatchID, value.SupplierBillID, value.ReconciliationCaseID, value.Reason, jsonBytes(value.Evidence), actor)
	if err != nil {
		return domain.SupplierAppeal{}, false, err
	}
	if value.SettlementBatchID != nil {
		_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status='DISPUTED',approved_by=NULL,approved_at=NULL,approval_reason='',updated_at=now() WHERE id=$1 AND supplier_id=$2 AND status NOT IN ('PAID','PROCESSING')`, *value.SettlementBatchID, value.SupplierID)
	} else if value.AccrualID != nil {
		_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch batch SET status='DISPUTED',approved_by=NULL,approved_at=NULL,approval_reason='',updated_at=now() FROM supplier_settlement_item item WHERE item.settlement_batch_id=batch.id AND item.accrual_id=$1 AND batch.supplier_id=$2 AND batch.status NOT IN ('PAID','PROCESSING')`, *value.AccrualID, value.SupplierID)
	}
	if err != nil {
		return domain.SupplierAppeal{}, false, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.appeal_submitted", "supplier_appeal", valueID, map[string]any{"supplier_id": value.SupplierID, "appeal_type": value.AppealType}); err != nil {
		return domain.SupplierAppeal{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierAppeal{}, false, err
	}
	v, e := s.SupplierAppealByID(ctx, valueID)
	return v, false, e
}

func (s *Store) SupplierAppealByID(ctx context.Context, appealID string) (domain.SupplierAppeal, error) {
	var v domain.SupplierAppeal
	var evidence []byte
	err := s.pool.QueryRow(ctx, `SELECT id,appeal_number,supplier_id,appeal_type,accrual_id,settlement_batch_id,supplier_bill_id,reconciliation_case_id,status,reason,evidence,resolution_reason,submitted_by,resolved_by,resolved_at,created_at,updated_at FROM supplier_appeal WHERE id=$1`, appealID).Scan(&v.ID, &v.AppealNumber, &v.SupplierID, &v.AppealType, &v.AccrualID, &v.SettlementBatchID, &v.SupplierBillID, &v.ReconciliationCaseID, &v.Status, &v.Reason, &evidence, &v.ResolutionReason, &v.SubmittedBy, &v.ResolvedBy, &v.ResolvedAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	_ = json.Unmarshal(evidence, &v.Evidence)
	return v, err
}

func (s *Store) ListSupplierAppeals(ctx context.Context, supplierID, status string, limit, offset int) ([]domain.SupplierAppeal, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM supplier_appeal WHERE ($1='' OR supplier_id=$1::uuid) AND ($2='' OR status=$2) ORDER BY created_at DESC,id LIMIT $3 OFFSET $4`, supplierID, strings.ToUpper(strings.TrimSpace(status)), clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]domain.SupplierAppeal, 0, len(ids))
	for _, appealID := range ids {
		v, e := s.SupplierAppealByID(ctx, appealID)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Store) ResolveSupplierAppeal(ctx context.Context, appealID, decision, reason, actor string) (domain.SupplierAppeal, error) {
	decision = strings.ToUpper(strings.TrimSpace(decision))
	if decision != "UPHELD" && decision != "REJECTED" || strings.TrimSpace(reason) == "" {
		return domain.SupplierAppeal{}, ErrSupplierSettlementState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.SupplierAppeal{}, err
	}
	defer tx.Rollback(ctx)
	var supplierID string
	var batchID, accrualID *string
	err = tx.QueryRow(ctx, `SELECT supplier_id,settlement_batch_id,accrual_id FROM supplier_appeal WHERE id=$1 AND status IN ('OPEN','UNDER_REVIEW') FOR UPDATE`, appealID).Scan(&supplierID, &batchID, &accrualID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SupplierAppeal{}, ErrSupplierSettlementState
	}
	if err != nil {
		return domain.SupplierAppeal{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE supplier_appeal SET status=$2,resolution_reason=$3,resolved_by=$4,resolved_at=now(),updated_at=now() WHERE id=$1`, appealID, decision, reason, actor)
	if err != nil {
		return domain.SupplierAppeal{}, err
	}
	if batchID != nil {
		newStatus := "PENDING_APPROVAL"
		if decision == "UPHELD" {
			newStatus = "CANCELLED"
		}
		_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch SET status=$2,approved_by=NULL,approved_at=NULL,approval_reason='',updated_at=now() WHERE id=$1 AND status='DISPUTED'`, *batchID, newStatus)
	} else if accrualID != nil && decision == "REJECTED" {
		_, err = tx.Exec(ctx, `UPDATE supplier_settlement_batch batch SET status='PENDING_APPROVAL',updated_at=now() FROM supplier_settlement_item item WHERE item.settlement_batch_id=batch.id AND item.accrual_id=$1 AND batch.status='DISPUTED' AND NOT EXISTS(SELECT 1 FROM supplier_appeal other WHERE other.id<>$2 AND other.status IN ('OPEN','UNDER_REVIEW') AND (other.settlement_batch_id=batch.id OR other.accrual_id=item.accrual_id))`, *accrualID, appealID)
	}
	if err != nil {
		return domain.SupplierAppeal{}, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "supplier.appeal_resolved", "supplier_appeal", appealID, map[string]any{"decision": decision, "reason": reason}); err != nil {
		return domain.SupplierAppeal{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.SupplierAppeal{}, err
	}
	return s.SupplierAppealByID(ctx, appealID)
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
