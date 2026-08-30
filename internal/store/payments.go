package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
	"github.com/relayedock/relayedock/internal/payment"
)

var (
	ErrPaymentState    = errors.New("payment order state does not allow this operation")
	ErrPaymentMismatch = errors.New("verified payment does not match the recharge order")
)

type CreateRechargeOrderRequest struct {
	PlatformOrderNo        string
	OrganizationID         string
	PaymentProvider        string
	Amount                 domain.Decimal
	Currency               string
	Region                 string
	IdempotencyKey         string
	ExpiresAt              time.Time
	CreatedBy              *string
	TargetProviderID       string
	TargetProvisioningMode string
}

type CreateRefundOrderRequest struct {
	PlatformRefundNo    string
	RechargeOrderID     string
	RefundApplicationID string
	Amount              domain.Decimal
	Reason              string
	IdempotencyKey      string
	CreatedBy           *string
}

func rechargeFingerprint(request CreateRechargeOrderRequest) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{request.OrganizationID, request.PaymentProvider,
		request.Amount.String(), request.Currency, request.Region, request.TargetProviderID, request.TargetProvisioningMode}, "\x00")))
	return hex.EncodeToString(sum[:])
}

const rechargeOrderSelect = `SELECT id,platform_order_no,organization_id,wallet_id,created_by,payment_provider,
	COALESCE(provider_order_no,''),status,amount::text,currency,region,idempotency_key,wallet_transaction_id,
	ledger_journal_id,expires_at,paid_at,credited_at,provider_closed_at,COALESCE(failure_code,''),metadata,created_at,updated_at,
	target_provider_id,target_provisioning_mode FROM recharge_order`

func scanRechargeOrder(row pgx.Row) (domain.RechargeOrder, error) {
	var order domain.RechargeOrder
	var amount string
	var metadata []byte
	err := row.Scan(&order.ID, &order.PlatformOrderNo, &order.OrganizationID, &order.WalletID, &order.CreatedBy,
		&order.PaymentProvider, &order.ProviderOrderNo, &order.Status, &amount, &order.Currency, &order.Region,
		&order.IdempotencyKey, &order.WalletTransactionID, &order.LedgerJournalID, &order.ExpiresAt, &order.PaidAt,
		&order.CreditedAt, &order.ProviderClosedAt, &order.FailureCode, &metadata, &order.CreatedAt, &order.UpdatedAt,
		&order.TargetProviderID, &order.TargetProvisioningMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return order, ErrNotFound
	}
	if err == nil {
		order.Amount, err = parseStoredDecimal(amount, "payment_order.amount")
		_ = json.Unmarshal(metadata, &order.Metadata)
	}
	return order, err
}

func (s *Store) CreateRechargeOrder(ctx context.Context, request CreateRechargeOrderRequest) (domain.RechargeOrder, bool, error) {
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.Region = strings.ToUpper(strings.TrimSpace(request.Region))
	request.PaymentProvider = strings.ToLower(strings.TrimSpace(request.PaymentProvider))
	request.TargetProviderID = strings.TrimSpace(request.TargetProviderID)
	request.TargetProvisioningMode = strings.ToUpper(strings.TrimSpace(request.TargetProvisioningMode))
	amountPositive, amountErr := request.Amount.IsPositive()
	if request.PlatformOrderNo == "" || request.OrganizationID == "" || request.IdempotencyKey == "" ||
		len(request.IdempotencyKey) > 200 || amountErr != nil || !amountPositive || len(request.Currency) != 3 ||
		len(request.Region) != 2 || request.ExpiresAt.Before(time.Now().UTC()) ||
		((request.TargetProviderID == "") != (request.TargetProvisioningMode == "")) ||
		(request.TargetProvisioningMode != "" && !validProvisioningMode(request.TargetProvisioningMode)) {
		return domain.RechargeOrder{}, false, errors.New("invalid recharge order")
	}
	fingerprint := rechargeFingerprint(request)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	defer tx.Rollback(ctx)
	// Serialize the logical idempotency scope before checking for an existing
	// row. This closes the concurrent first-insert race across replicas.
	lockDigest := sha256.Sum256([]byte(request.OrganizationID + "\x00" + request.IdempotencyKey))
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, hex.EncodeToString(lockDigest[:])); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	existing, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE organization_id=$1 AND idempotency_key=$2 FOR UPDATE`, request.OrganizationID, request.IdempotencyKey))
	if err == nil {
		var existingFingerprint string
		if err = tx.QueryRow(ctx, `SELECT request_fingerprint FROM recharge_order WHERE id=$1`, existing.ID).Scan(&existingFingerprint); err != nil {
			return domain.RechargeOrder{}, false, err
		}
		if existingFingerprint != fingerprint {
			return domain.RechargeOrder{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return domain.RechargeOrder{}, false, err
	}
	// Public control-plane callers always supply CreatedBy. Preserve the legacy
	// internal contract for trusted import/recovery callers that predate users.
	if request.CreatedBy != nil && strings.TrimSpace(*request.CreatedBy) != "" {
		if err = checkRechargeRisk(ctx, tx, request.OrganizationID, *request.CreatedBy, request.Amount.String(), true); err != nil {
			return domain.RechargeOrder{}, false, err
		}
	}
	if request.TargetProviderID != "" {
		if request.CreatedBy == nil || strings.TrimSpace(*request.CreatedBy) == "" {
			return domain.RechargeOrder{}, false, errors.New("target provider recharge requires a user")
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`,
			providerBindingAdvisoryKey(request.OrganizationID, *request.CreatedBy, request.TargetProviderID)); err != nil {
			return domain.RechargeOrder{}, false, err
		}
		var targetAllowed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM providers WHERE id=$1 AND enabled)
			AND EXISTS(SELECT 1 FROM organization_memberships WHERE organization_id=$2 AND user_id=$3 AND status='ACTIVE')`,
			request.TargetProviderID, request.OrganizationID, *request.CreatedBy).Scan(&targetAllowed); err != nil {
			return domain.RechargeOrder{}, false, err
		}
		if !targetAllowed {
			return domain.RechargeOrder{}, false, ErrNotFound
		}
		bindingID := id.UUID()
		err = tx.QueryRow(ctx, `INSERT INTO provider_account_binding(id,organization_id,user_id,provider_id,provisioning_mode,status)
			VALUES($1,$2,$3,$4,$5,'PENDING') ON CONFLICT(organization_id,user_id,provider_id) DO UPDATE SET updated_at=now()
			WHERE provider_account_binding.provisioning_mode=EXCLUDED.provisioning_mode RETURNING id`, bindingID,
			request.OrganizationID, *request.CreatedBy, request.TargetProviderID, request.TargetProvisioningMode).Scan(&bindingID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RechargeOrder{}, false, ErrBindingMode
		}
		if err != nil {
			return domain.RechargeOrder{}, false, err
		}
	}
	var walletID, walletCurrency, walletStatus string
	err = tx.QueryRow(ctx, `SELECT id,currency,status FROM wallets WHERE organization_id=$1 FOR UPDATE`, request.OrganizationID).
		Scan(&walletID, &walletCurrency, &walletStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RechargeOrder{}, false, ErrNotFound
	}
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if walletStatus == "CLOSED" || walletCurrency != request.Currency {
		return domain.RechargeOrder{}, false, ErrWalletUnavailable
	}
	orderID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO recharge_order(id,platform_order_no,organization_id,wallet_id,created_by,
		payment_provider,status,amount,currency,region,idempotency_key,request_fingerprint,expires_at,target_provider_id,target_provisioning_mode)
		VALUES($1,$2,$3,$4,$5,$6,'CREATED',$7,$8,$9,$10,$11,$12,$13,$14)`, orderID, request.PlatformOrderNo,
		request.OrganizationID, walletID, request.CreatedBy, request.PaymentProvider, request.Amount.String(),
		request.Currency, request.Region, request.IdempotencyKey, fingerprint, request.ExpiresAt,
		nullString(request.TargetProviderID), nullString(request.TargetProvisioningMode))
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "payment.recharge_created", "recharge_order", orderID, map[string]any{
		"platform_order_no": request.PlatformOrderNo, "organization_id": request.OrganizationID,
		"payment_provider": request.PaymentProvider, "amount": request.Amount.String(), "currency": request.Currency,
		"target_provider_id": request.TargetProviderID, "target_provisioning_mode": request.TargetProvisioningMode,
	}); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	order, err := s.RechargeOrderByID(ctx, orderID)
	return order, false, err
}

func (s *Store) RechargeOrderByID(ctx context.Context, orderID string) (domain.RechargeOrder, error) {
	return scanRechargeOrder(s.pool.QueryRow(ctx, rechargeOrderSelect+` WHERE id=$1`, orderID))
}

func (s *Store) RechargeOrderByPlatformNo(ctx context.Context, platformOrderNo string) (domain.RechargeOrder, error) {
	return scanRechargeOrder(s.pool.QueryRow(ctx, rechargeOrderSelect+` WHERE platform_order_no=$1`, platformOrderNo))
}

func (s *Store) ListRechargeOrders(ctx context.Context, organizationID string, limit, offset int) ([]domain.RechargeOrder, error) {
	query := rechargeOrderSelect
	args := []any{}
	if organizationID != "" {
		query += ` WHERE organization_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`
		args = []any{organizationID, clamp(limit), max(offset, 0)}
	} else {
		query += ` ORDER BY created_at DESC,id DESC LIMIT $1 OFFSET $2`
		args = []any{clamp(limit), max(offset, 0)}
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RechargeOrder, 0)
	for rows.Next() {
		order, scanErr := scanRechargeOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, order)
	}
	return out, rows.Err()
}

// ListRechargeOrdersForReconciliation returns a stable page of recharge
// orders whose financial state changed during one UTC business date. The
// cursor is the UUID text from the previous page; callers must keep the same
// business date while advancing it.
func (s *Store) ListRechargeOrdersForReconciliation(ctx context.Context, businessDate time.Time, afterID string, limit int) ([]domain.RechargeOrder, error) {
	date := businessDate.UTC().Format("2006-01-02")
	rows, err := s.pool.Query(ctx, rechargeOrderSelect+` WHERE id>$2::uuid
		AND (created_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND created_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')
			OR paid_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND paid_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')
			OR credited_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND credited_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC')
			OR updated_at>=($1::date::timestamp AT TIME ZONE 'UTC') AND updated_at<(($1::date+1)::timestamp AT TIME ZONE 'UTC'))
		AND status IN ('PAID','CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')
		ORDER BY id LIMIT $3`, date, firstNonEmptyStore(afterID, "00000000-0000-0000-0000-000000000000"), clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RechargeOrder, 0)
	for rows.Next() {
		order, scanErr := scanRechargeOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, order)
	}
	return out, rows.Err()
}

func (s *Store) StartPaymentAttempt(ctx context.Context, orderID, operation, requestHash string, metadata map[string]any) (domain.PaymentAttempt, error) {
	metadata = paymentObject(metadata)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PaymentAttempt{}, err
	}
	defer tx.Rollback(ctx)
	var attemptNo int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(attempt_no),0)+1 FROM payment_attempt WHERE recharge_order_id=$1
		AND EXISTS(SELECT 1 FROM recharge_order WHERE id=$1 FOR UPDATE)`, orderID).Scan(&attemptNo); err != nil {
		return domain.PaymentAttempt{}, err
	}
	attempt := domain.PaymentAttempt{ID: id.UUID(), RechargeOrderID: orderID, AttemptNo: attemptNo, Operation: operation,
		Status: "STARTED", RequestHash: requestHash, StartedAt: time.Now().UTC(), Metadata: metadata}
	_, err = tx.Exec(ctx, `INSERT INTO payment_attempt(id,recharge_order_id,attempt_no,operation,status,request_hash,started_at,metadata)
		VALUES($1,$2,$3,$4,'STARTED',$5,$6,$7)`, attempt.ID, orderID, attemptNo, operation, nullString(requestHash), attempt.StartedAt, jsonBytes(metadata))
	if err != nil {
		return domain.PaymentAttempt{}, err
	}
	return attempt, tx.Commit(ctx)
}

func (s *Store) FinishPaymentAttempt(ctx context.Context, attemptID, status, providerOrderNo, responseCode, errorCode string, metadata map[string]any) error {
	metadata = paymentObject(metadata)
	if status != "SUCCEEDED" && status != "FAILED" && status != "PENDING" {
		return errors.New("invalid payment attempt status")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE payment_attempt SET status=$2,provider_order_no=$3,response_code=$4,error_code=$5,
		finished_at=now(),metadata=metadata||$6::jsonb WHERE id=$1 AND status='STARTED'`, attemptID, status,
		nullString(providerOrderNo), nullString(responseCode), nullString(errorCode), jsonBytes(metadata))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrPaymentState
	}
	return err
}

func (s *Store) MarkRechargePending(ctx context.Context, orderID, providerOrderNo string, instructions map[string]any) (domain.RechargeOrder, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE recharge_order SET provider_order_no=$2,status='PENDING',metadata=metadata||$3::jsonb,
		updated_at=now() WHERE id=$1 AND status='CREATED'`, orderID, providerOrderNo, jsonBytes(map[string]any{"payment_instructions": instructions}))
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	if tag.RowsAffected() == 0 {
		return domain.RechargeOrder{}, ErrPaymentState
	}
	return s.RechargeOrderByID(ctx, orderID)
}

func (s *Store) MarkRechargeFailed(ctx context.Context, orderID, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE recharge_order SET status='FAILED',failure_code=$2,updated_at=now()
		WHERE id=$1 AND status IN ('CREATED','PENDING')`, orderID, code)
	return err
}

func validateVerifiedPayment(order domain.RechargeOrder, verified payment.VerifiedWebhook) error {
	if order.PaymentProvider == "" || order.ProviderOrderNo != verified.ProviderOrderNo ||
		!decimalEqual(order.Amount.String(), verified.Amount.String()) || order.Currency != verified.Currency {
		return ErrPaymentMismatch
	}
	return nil
}

func (s *Store) ApplyPaymentQueryResult(ctx context.Context, orderID string, result payment.PaymentResult, actor *string) (domain.RechargeOrder, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	defer tx.Rollback(ctx)
	order, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE id=$1 FOR UPDATE`, orderID))
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	if result.ProviderOrderNo != order.ProviderOrderNo || (result.Amount.String() != "0" && !decimalEqual(result.Amount.String(), order.Amount.String())) ||
		(result.Currency != "" && result.Currency != order.Currency) {
		return domain.RechargeOrder{}, ErrPaymentMismatch
	}
	now := time.Now().UTC()
	switch strings.ToUpper(result.Status) {
	case "PAID":
		if order.Status == "CREATED" || order.Status == "PENDING" || order.Status == "EXPIRED" {
			_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='PAID',paid_at=$2,updated_at=$2 WHERE id=$1`, order.ID, now)
		}
	case "FAILED":
		if order.Status == "CREATED" || order.Status == "PENDING" {
			_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='FAILED',failure_code='provider_query_failed',updated_at=$2 WHERE id=$1`, order.ID, now)
		}
	case "EXPIRED":
		if order.Status == "CREATED" || order.Status == "PENDING" {
			_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='EXPIRED',failure_code='provider_query_expired',updated_at=$2 WHERE id=$1`, order.ID, now)
		}
	}
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	if err = writeAuditTx(ctx, tx, actor, "payment.query_applied", "recharge_order", order.ID, map[string]any{
		"provider_status": result.Status, "provider_order_no": result.ProviderOrderNo,
	}); err != nil {
		return domain.RechargeOrder{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RechargeOrder{}, err
	}
	return s.RechargeOrderByID(ctx, order.ID)
}

func (s *Store) RecordVerifiedPaymentWebhook(ctx context.Context, provider string, verified payment.VerifiedWebhook, rawBodySHA256 string) (domain.RechargeOrder, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	defer tx.Rollback(ctx)
	order, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE platform_order_no=$1 FOR UPDATE`, verified.PlatformOrderNo))
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if order.PaymentProvider != provider || validateVerifiedPayment(order, verified) != nil {
		return domain.RechargeOrder{}, false, ErrPaymentMismatch
	}
	eventID := id.UUID()
	var insertedID string
	err = tx.QueryRow(ctx, `INSERT INTO payment_webhook_event(id,payment_provider,provider_event_id,replay_key,provider_order_no,
		recharge_order_id,event_type,payment_status,amount,currency,provider_timestamp,raw_body_sha256,signature_valid,
		timestamp_valid,processing_status,normalized_payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,true,true,'RECEIVED',$13)
		ON CONFLICT DO NOTHING RETURNING id`, eventID, provider, verified.ProviderEventID, verified.ReplayKey,
		verified.ProviderOrderNo, order.ID, verified.EventType, verified.Status, verified.Amount.String(), verified.Currency,
		verified.ProviderTimestamp, rawBodySHA256, jsonBytes(paymentObject(verified.NormalizedPayload))).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Distinguish a byte-for-byte replay from an event-ID collision. The
		// latter is rejected and must never inherit a previously credited result.
		var existingHash, existingOrderID, existingProviderOrderNo, existingStatus, existingAmount, existingCurrency string
		lookupErr := tx.QueryRow(ctx, `SELECT raw_body_sha256,COALESCE(recharge_order_id::text,''),provider_order_no,
			payment_status,amount::text,currency FROM payment_webhook_event WHERE payment_provider=$1
			AND (provider_event_id=$2 OR replay_key=$3)`, provider, verified.ProviderEventID, verified.ReplayKey).
			Scan(&existingHash, &existingOrderID, &existingProviderOrderNo, &existingStatus, &existingAmount, &existingCurrency)
		if lookupErr != nil {
			return domain.RechargeOrder{}, false, lookupErr
		}
		if existingHash != rawBodySHA256 || existingOrderID != order.ID || existingProviderOrderNo != verified.ProviderOrderNo ||
			existingStatus != verified.Status || !decimalEqual(existingAmount, verified.Amount.String()) || existingCurrency != verified.Currency {
			return domain.RechargeOrder{}, false, ErrPaymentMismatch
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.RechargeOrder{}, false, err
		}
		current, loadErr := s.RechargeOrderByID(ctx, order.ID)
		return current, true, loadErr
	}
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	attemptID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO payment_attempt(id,recharge_order_id,attempt_no,operation,status,provider_order_no,
		request_hash,response_code,finished_at,metadata) SELECT $1,$2,COALESCE(max(attempt_no),0)+1,'VERIFY_WEBHOOK',
		'SUCCEEDED',$3,$4,$5,now(),$6 FROM payment_attempt WHERE recharge_order_id=$2`, attemptID, order.ID,
		verified.ProviderOrderNo, rawBodySHA256, verified.ProviderEventID, jsonBytes(map[string]any{"event_type": verified.EventType}))
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	now := time.Now().UTC()
	switch verified.Status {
	case "PAID":
		if order.Status == "CREATED" || order.Status == "PENDING" || order.Status == "EXPIRED" {
			_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='PAID',paid_at=$2,updated_at=$2 WHERE id=$1`, order.ID, now)
		}
	case "FAILED":
		if order.Status == "CREATED" || order.Status == "PENDING" {
			_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='FAILED',failure_code='provider_failed',updated_at=$2 WHERE id=$1`, order.ID, now)
		}
	case "CHARGEBACK":
		if order.Status == "CREDITED" {
			err = applyChargebackTx(ctx, tx, order, verified.ProviderEventID, now)
		}
	}
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE payment_webhook_event SET processing_status='PROCESSED',processed_at=$2 WHERE id=$1`, insertedID, now)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = writeAuditTx(ctx, tx, nil, "payment.webhook_verified", "recharge_order", order.ID, map[string]any{
		"provider_event_id": verified.ProviderEventID, "status": verified.Status, "raw_body_sha256": rawBodySHA256,
	}); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	current, err := s.RechargeOrderByID(ctx, order.ID)
	return current, false, err
}

func applyChargebackTx(ctx context.Context, tx pgx.Tx, order domain.RechargeOrder, providerEventID string, now time.Time) error {
	var walletStatus, walletCurrency, balanceAfter string
	if err := tx.QueryRow(ctx, `SELECT status,currency,available_balance::text FROM wallets WHERE id=$1 FOR UPDATE`, order.WalletID).
		Scan(&walletStatus, &walletCurrency, &balanceAfter); err != nil {
		return err
	}
	if walletStatus == "CLOSED" || walletCurrency != order.Currency {
		return ErrWalletUnavailable
	}
	if err := tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance-$2::numeric,version=version+1,updated_at=$3
		WHERE id=$1 RETURNING available_balance::text`, order.WalletID, order.Amount.String(), now).Scan(&balanceAfter); err != nil {
		return err
	}
	refundID := id.UUID()
	platformRefundNo := "CB" + strings.ReplaceAll(order.PlatformOrderNo, "_", "")
	providerRefundNo := "chargeback:" + providerEventID
	_, err := tx.Exec(ctx, `INSERT INTO refund_order(id,platform_refund_no,recharge_order_id,payment_provider,provider_refund_no,
		status,amount,currency,reason,idempotency_key,created_at,updated_at,completed_at)
		VALUES($1,$2,$3,$4,$5,'CREATED',$6,$7,'Provider chargeback',$8,$9,$9,NULL)`, refundID, platformRefundNo,
		order.ID, order.PaymentProvider, providerRefundNo, order.Amount.String(), order.Currency, providerRefundNo, now)
	if err != nil {
		return err
	}
	journalID, err := postLinkedJournalTx(ctx, tx, order.WalletID, "PAYMENT_REFUND", "chargeback:"+order.ID,
		order.Currency, providerEventID, map[string]any{"recharge_order_id": order.ID, "refund_order_id": refundID,
			"provider_event_id": providerEventID, "reason": "chargeback"}, nil, nil, &refundID, []journalLine{
			{walletAccountKey(order.WalletID, "available"), "DEBIT", order.Amount.String(), "Chargeback wallet debit"},
			{systemAccountKey("cash", order.Currency), "CREDIT", order.Amount.String(), "Chargeback cash reversal"},
		})
	if err != nil {
		return err
	}
	transactionID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,
		reference,metadata,journal_id,refund_order_id) VALUES($1,$2,'REFUND',-$3::numeric,$4,$5,$6,$7,$8,$9)`, transactionID,
		order.WalletID, order.Amount.String(), balanceAfter, "chargeback:"+order.ID, providerEventID,
		jsonBytes(map[string]any{"recharge_order_id": order.ID, "refund_order_id": refundID, "provider_event_id": providerEventID}), journalID, refundID)
	if err != nil {
		return err
	}
	lotDebited, creditDebited, err := allocateChargebackTx(ctx, tx, order.WalletID, order.ID, transactionID, order.Amount.String())
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE refund_order SET status='SUCCEEDED',wallet_transaction_id=$2,ledger_journal_id=$3,
		updated_at=$4,completed_at=$4 WHERE id=$1`, refundID, transactionID, journalID, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='CHARGEBACK',updated_at=$2 WHERE id=$1`, order.ID, now)
	if err != nil {
		return err
	}
	return writeAuditTx(ctx, tx, nil, "payment.chargeback_applied", "recharge_order", order.ID, map[string]any{
		"provider_event_id": providerEventID, "refund_order_id": refundID, "wallet_transaction_id": transactionID,
		"ledger_journal_id": journalID, "amount": order.Amount.String(), "cash_lot_debited": lotDebited,
		"credit_debited": creditDebited, "currency": order.Currency,
	})
}

func (s *Store) MarkRechargePaid(ctx context.Context, orderID, evidenceReference string, actor *string) (domain.RechargeOrder, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	defer tx.Rollback(ctx)
	order, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE id=$1 FOR UPDATE`, orderID))
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	if order.Status == "PAID" || order.Status == "CREDITED" {
		return order, tx.Commit(ctx)
	}
	if order.Status != "PENDING" || order.PaymentProvider != "manual_transfer" || strings.TrimSpace(evidenceReference) == "" {
		return domain.RechargeOrder{}, ErrPaymentState
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='PAID',paid_at=now(),metadata=metadata||$2::jsonb,updated_at=now() WHERE id=$1`,
		order.ID, jsonBytes(map[string]any{"manual_evidence_reference": strings.TrimSpace(evidenceReference)}))
	if err != nil {
		return domain.RechargeOrder{}, err
	}
	if err = writeAuditTx(ctx, tx, actor, "payment.manual_approved", "recharge_order", order.ID, map[string]any{"evidence_reference": evidenceReference}); err != nil {
		return domain.RechargeOrder{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RechargeOrder{}, err
	}
	return s.RechargeOrderByID(ctx, order.ID)
}

// CreditPaidRecharge is the sole recharge-to-wallet write path. The order
// transition, balance, immutable wallet transaction, balanced journal, links,
// and audit record commit together under a wallet row lock.
func (s *Store) CreditPaidRecharge(ctx context.Context, orderID string) (domain.RechargeOrder, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	defer tx.Rollback(ctx)
	order, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE id=$1 FOR UPDATE`, orderID))
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if order.Status == "CREDITED" || order.Status == "REFUND_PENDING" || order.Status == "REFUNDED" || order.Status == "CHARGEBACK" {
		return order, true, tx.Commit(ctx)
	}
	if order.Status != "PAID" {
		return domain.RechargeOrder{}, false, ErrPaymentState
	}
	var walletStatus, walletCurrency, balanceAfter string
	err = tx.QueryRow(ctx, `SELECT status,currency,available_balance::text FROM wallets WHERE id=$1 FOR UPDATE`, order.WalletID).
		Scan(&walletStatus, &walletCurrency, &balanceAfter)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if walletStatus == "CLOSED" || walletCurrency != order.Currency {
		return domain.RechargeOrder{}, false, ErrWalletUnavailable
	}
	err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance+$2::numeric,version=version+1,updated_at=now()
		WHERE id=$1 RETURNING available_balance::text`, order.WalletID, order.Amount.String()).Scan(&balanceAfter)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	linkedOrderID := order.ID
	journalID, err := postLinkedJournalTx(ctx, tx, order.WalletID, "PAYMENT_CREDIT", "recharge:"+order.ID,
		order.Currency, order.PlatformOrderNo, map[string]any{"recharge_order_id": order.ID, "payment_provider": order.PaymentProvider,
			"provider_order_no": order.ProviderOrderNo}, nil, &linkedOrderID, nil, []journalLine{
			{systemAccountKey("cash", order.Currency), "DEBIT", order.Amount.String(), "Verified payment cash"},
			{walletAccountKey(order.WalletID, "available"), "CREDIT", order.Amount.String(), "Verified recharge credit"},
		})
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	transactionID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,
		reference,metadata,journal_id,recharge_order_id) VALUES($1,$2,'TOPUP',$3,$4,$5,$6,$7,$8,$9)`, transactionID,
		order.WalletID, order.Amount.String(), balanceAfter, "recharge:"+order.ID, order.PlatformOrderNo,
		jsonBytes(map[string]any{"recharge_order_id": order.ID, "payment_provider": order.PaymentProvider,
			"provider_order_no": order.ProviderOrderNo}), journalID, order.ID)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = createCashLotTx(ctx, tx, order.WalletID, order.ID, transactionID, "RECHARGE", order.PlatformOrderNo,
		order.Amount.String(), order.Currency, true); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='CREDITED',wallet_transaction_id=$2,ledger_journal_id=$3,
		credited_at=now(),updated_at=now() WHERE id=$1`, order.ID, transactionID, journalID)
	if err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = enqueueProviderAllocationTx(ctx, tx, order); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = writeAuditTx(ctx, tx, nil, "payment.recharge_credited", "recharge_order", order.ID, map[string]any{
		"wallet_id": order.WalletID, "wallet_transaction_id": transactionID, "ledger_journal_id": journalID,
		"amount": order.Amount.String(), "currency": order.Currency,
	}); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RechargeOrder{}, false, err
	}
	current, err := s.RechargeOrderByID(ctx, order.ID)
	return current, false, err
}

func (s *Store) ListRecoverableRechargeOrders(ctx context.Context, limit int) ([]domain.RechargeOrder, error) {
	rows, err := s.pool.Query(ctx, rechargeOrderSelect+` WHERE status='PAID' ORDER BY paid_at,id LIMIT $1`, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RechargeOrder, 0)
	for rows.Next() {
		order, scanErr := scanRechargeOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, order)
	}
	return out, rows.Err()
}

func (s *Store) ExpireRechargeOrders(ctx context.Context, now time.Time, limit int) ([]domain.RechargeOrder, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, rechargeOrderSelect+` WHERE status IN ('CREATED','PENDING') AND expires_at<=$1
		ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $2`, now, clamp(limit))
	if err != nil {
		return nil, err
	}
	orders := make([]domain.RechargeOrder, 0)
	for rows.Next() {
		order, scanErr := scanRechargeOrder(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		orders = append(orders, order)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, order := range orders {
		if _, err = tx.Exec(ctx, `UPDATE recharge_order SET status='EXPIRED',failure_code='expired',updated_at=$2 WHERE id=$1`, order.ID, now); err != nil {
			return nil, err
		}
		order.Status = "EXPIRED"
	}
	return orders, tx.Commit(ctx)
}

func (s *Store) ListUnclosedExpiredRechargeOrders(ctx context.Context, limit int) ([]domain.RechargeOrder, error) {
	rows, err := s.pool.Query(ctx, rechargeOrderSelect+` WHERE status='EXPIRED' AND provider_closed_at IS NULL
		ORDER BY updated_at,id LIMIT $1`, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RechargeOrder, 0)
	for rows.Next() {
		order, scanErr := scanRechargeOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, order)
	}
	return out, rows.Err()
}

func (s *Store) MarkRechargeProviderClosed(ctx context.Context, orderID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE recharge_order SET provider_closed_at=now(),updated_at=now()
		WHERE id=$1 AND status='EXPIRED' AND provider_closed_at IS NULL`, orderID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrPaymentState
	}
	return err
}

func (s *Store) CreateRefundOrder(ctx context.Context, request CreateRefundOrderRequest) (domain.RefundOrder, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	defer tx.Rollback(ctx)
	order, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE id=$1 FOR UPDATE`, request.RechargeOrderID))
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	var refund domain.RefundOrder
	err = scanRefundOrder(tx.QueryRow(ctx, refundOrderSelect+` WHERE recharge_order_id=$1 AND idempotency_key=$2`, order.ID, request.IdempotencyKey), &refund)
	if err == nil {
		if !decimalEqual(refund.Amount.String(), request.Amount.String()) || refund.Reason != strings.TrimSpace(request.Reason) ||
			derefFinanceID(refund.RefundApplicationID) != strings.TrimSpace(request.RefundApplicationID) {
			return domain.RefundOrder{}, false, ErrIdempotencyConflict
		}
		return refund, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.RefundOrder{}, false, err
	}
	amountPositive, amountErr := request.Amount.IsPositive()
	if order.Status != "CREDITED" || amountErr != nil || !amountPositive ||
		request.IdempotencyKey == "" || strings.TrimSpace(request.Reason) == "" {
		return domain.RefundOrder{}, false, ErrPaymentState
	}
	if request.RefundApplicationID == "" {
		// Preserve the legacy direct-refund contract: only a full unused recharge
		// can be refunded without an approved application.
		if !decimalEqual(request.Amount.String(), order.Amount.String()) {
			return domain.RefundOrder{}, false, ErrPaymentState
		}
	} else {
		var applicationAmount string
		err = tx.QueryRow(ctx, `SELECT requested_amount::text FROM refund_application
			WHERE id=$1 AND organization_id=$2 AND source_type='RECHARGE' AND recharge_order_id=$3
			AND status='APPROVED' AND refund_order_id IS NULL FOR UPDATE`, request.RefundApplicationID, order.OrganizationID, order.ID).Scan(&applicationAmount)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !decimalEqual(applicationAmount, request.Amount.String()) {
			return domain.RefundOrder{}, false, ErrPaymentState
		}
		if err != nil {
			return domain.RefundOrder{}, false, err
		}
	}
	var refundableCash string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(remaining_amount,0)::text FROM wallet_cash_lot
		WHERE wallet_id=$1 AND recharge_order_id=$2 AND refundable FOR UPDATE`, order.WalletID, order.ID).Scan(&refundableCash); err != nil {
		return domain.RefundOrder{}, false, ErrRefundNotEligible
	}
	if mustFundingRat(request.Amount.String()).Cmp(mustFundingRat(refundableCash)) > 0 {
		return domain.RefundOrder{}, false, ErrRefundNotEligible
	}
	refund.ID, refund.PlatformRefundNo, refund.RechargeOrderID = id.UUID(), request.PlatformRefundNo, order.ID
	refund.PaymentProvider, refund.Status, refund.Amount = order.PaymentProvider, "CREATED", request.Amount
	refund.Currency, refund.Reason, refund.IdempotencyKey, refund.CreatedBy = order.Currency, strings.TrimSpace(request.Reason), request.IdempotencyKey, request.CreatedBy
	_, err = tx.Exec(ctx, `INSERT INTO refund_order(id,platform_refund_no,recharge_order_id,payment_provider,status,amount,currency,
		reason,idempotency_key,created_by,refund_application_id) VALUES($1,$2,$3,$4,'CREATED',$5,$6,$7,$8,$9,$10)`, refund.ID, refund.PlatformRefundNo,
		order.ID, order.PaymentProvider, refund.Amount.String(), refund.Currency, refund.Reason, refund.IdempotencyKey, refund.CreatedBy, nullString(request.RefundApplicationID))
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	if err = reserveRefundCashTx(ctx, tx, refund.ID, order.WalletID, order.ID, refund.Amount.String()); err != nil {
		return domain.RefundOrder{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='REFUND_PENDING',updated_at=now() WHERE id=$1`, order.ID)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	if request.RefundApplicationID != "" {
		_, err = tx.Exec(ctx, `UPDATE refund_application SET status='PROCESSING',refund_order_id=$2,updated_at=now() WHERE id=$1`, request.RefundApplicationID, refund.ID)
		if err != nil {
			return domain.RefundOrder{}, false, err
		}
	}
	if err = writeAuditTx(ctx, tx, request.CreatedBy, "payment.refund_created", "refund_order", refund.ID, map[string]any{
		"recharge_order_id": order.ID, "amount": refund.Amount.String(), "currency": refund.Currency,
	}); err != nil {
		return domain.RefundOrder{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RefundOrder{}, false, err
	}
	loaded, err := s.RefundOrderByID(ctx, refund.ID)
	return loaded, false, err
}

const refundOrderSelect = `SELECT id,platform_refund_no,recharge_order_id,refund_application_id,payment_provider,COALESCE(provider_refund_no,''),
	status,amount::text,currency,reason,idempotency_key,wallet_transaction_id,ledger_journal_id,created_by,
	COALESCE(failure_code,''),created_at,updated_at,completed_at FROM refund_order`

func scanRefundOrder(row pgx.Row, refund *domain.RefundOrder) error {
	var amount string
	err := row.Scan(&refund.ID, &refund.PlatformRefundNo, &refund.RechargeOrderID, &refund.RefundApplicationID, &refund.PaymentProvider,
		&refund.ProviderRefundNo, &refund.Status, &amount, &refund.Currency, &refund.Reason, &refund.IdempotencyKey,
		&refund.WalletTransactionID, &refund.LedgerJournalID, &refund.CreatedBy, &refund.FailureCode,
		&refund.CreatedAt, &refund.UpdatedAt, &refund.CompletedAt)
	if err == nil {
		refund.Amount, err = parseStoredDecimal(amount, "payment_refund.amount")
	}
	return err
}

func (s *Store) RefundOrderByID(ctx context.Context, refundID string) (domain.RefundOrder, error) {
	var refund domain.RefundOrder
	err := scanRefundOrder(s.pool.QueryRow(ctx, refundOrderSelect+` WHERE id=$1`, refundID), &refund)
	if errors.Is(err, pgx.ErrNoRows) {
		return refund, ErrNotFound
	}
	return refund, err
}

func (s *Store) ListRecoverableRefundOrders(ctx context.Context, limit int) ([]domain.RefundOrder, error) {
	rows, err := s.pool.Query(ctx, refundOrderSelect+` WHERE status IN ('CREATED','PENDING') ORDER BY created_at,id LIMIT $1`, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RefundOrder, 0)
	for rows.Next() {
		var refund domain.RefundOrder
		if err = scanRefundOrder(rows, &refund); err != nil {
			return nil, err
		}
		out = append(out, refund)
	}
	return out, rows.Err()
}

func (s *Store) MarkRefundPending(ctx context.Context, refundID, providerRefundNo string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE refund_order SET status='PENDING',provider_refund_no=$2,updated_at=now()
		WHERE id=$1 AND status='CREATED'`, refundID, providerRefundNo)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrPaymentState
	}
	return err
}

func (s *Store) FailRefund(ctx context.Context, refundID, code string) error {
	code = strings.TrimSpace(code)
	if refundID == "" || code == "" {
		return ErrPaymentState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var refund domain.RefundOrder
	err = scanRefundOrder(tx.QueryRow(ctx, refundOrderSelect+` WHERE id=$1 FOR UPDATE`, refundID), &refund)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if refund.Status == "FAILED" {
		if refund.FailureCode != code {
			return ErrIdempotencyConflict
		}
		return tx.Commit(ctx)
	}
	if refund.Status != "CREATED" && refund.Status != "PENDING" {
		return ErrPaymentState
	}
	var walletID string
	if err = tx.QueryRow(ctx, `SELECT wallet_id FROM recharge_order WHERE id=$1 FOR UPDATE`, refund.RechargeOrderID).Scan(&walletID); err != nil {
		return err
	}
	if err = settleRefundCashTx(ctx, tx, refund.ID, walletID, "", false); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE refund_order SET status='FAILED',failure_code=$2,updated_at=now(),completed_at=now()
		WHERE id=$1`, refund.ID, code)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='CREDITED',updated_at=now() WHERE id=$1 AND status='REFUND_PENDING'`, refund.RechargeOrderID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE refund_application SET status='FAILED',completed_at=now(),review_reason=COALESCE(NULLIF(review_reason,''),$2),updated_at=now()
		WHERE refund_order_id=$1 AND status='PROCESSING'`, refund.ID, code)
	if err != nil {
		return err
	}
	if err = writeAuditTx(ctx, tx, refund.CreatedBy, "payment.refund_failed", "refund_order", refund.ID, map[string]any{
		"recharge_order_id": refund.RechargeOrderID, "failure_code": code, "released_amount": refund.Amount.String(),
		"currency": refund.Currency,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteRefund(ctx context.Context, refundID, providerRefundNo string) (domain.RefundOrder, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	defer tx.Rollback(ctx)
	var refund domain.RefundOrder
	err = scanRefundOrder(tx.QueryRow(ctx, refundOrderSelect+` WHERE id=$1 FOR UPDATE`, refundID), &refund)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RefundOrder{}, false, ErrNotFound
	}
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	if refund.Status == "SUCCEEDED" {
		return refund, true, tx.Commit(ctx)
	}
	if refund.Status != "CREATED" && refund.Status != "PENDING" {
		return domain.RefundOrder{}, false, ErrPaymentState
	}
	order, err := scanRechargeOrder(tx.QueryRow(ctx, rechargeOrderSelect+` WHERE id=$1 FOR UPDATE`, refund.RechargeOrderID))
	if err != nil || order.Status != "REFUND_PENDING" {
		if err == nil {
			err = ErrPaymentState
		}
		return domain.RefundOrder{}, false, err
	}
	var balanceAfter string
	err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance-$2::numeric,version=version+1,updated_at=now()
		WHERE id=$1 RETURNING available_balance::text`, order.WalletID, refund.Amount.String()).Scan(&balanceAfter)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	linkedRefundID := refund.ID
	journalID, err := postLinkedJournalTx(ctx, tx, order.WalletID, "PAYMENT_REFUND", "refund:"+refund.ID,
		refund.Currency, refund.PlatformRefundNo, map[string]any{"refund_order_id": refund.ID, "recharge_order_id": order.ID,
			"provider_refund_no": providerRefundNo}, refund.CreatedBy, nil, &linkedRefundID, []journalLine{
			{walletAccountKey(order.WalletID, "available"), "DEBIT", refund.Amount.String(), "Payment refund debit"},
			{systemAccountKey("cash", refund.Currency), "CREDIT", refund.Amount.String(), "Payment refund cash"},
		})
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	transactionID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,transaction_type,amount,balance_after,idempotency_key,
		reference,metadata,created_by,journal_id,refund_order_id) VALUES($1,$2,'REFUND',-$3::numeric,$4,$5,$6,$7,$8,$9,$10)`,
		transactionID, order.WalletID, refund.Amount.String(), balanceAfter, "refund:"+refund.ID, refund.PlatformRefundNo,
		jsonBytes(map[string]any{"refund_order_id": refund.ID, "recharge_order_id": order.ID}), refund.CreatedBy, journalID, refund.ID)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	if err = settleRefundCashTx(ctx, tx, refund.ID, order.WalletID, transactionID, true); err != nil {
		return domain.RefundOrder{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE refund_order SET status='SUCCEEDED',provider_refund_no=$2,wallet_transaction_id=$3,
		ledger_journal_id=$4,completed_at=now(),updated_at=now() WHERE id=$1`, refund.ID, providerRefundNo, transactionID, journalID)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='REFUNDED',updated_at=now() WHERE id=$1`, order.ID)
	if mustFundingRat(refund.Amount.String()).Cmp(mustFundingRat(order.Amount.String())) < 0 {
		_, err = tx.Exec(ctx, `UPDATE recharge_order SET status='CREDITED',updated_at=now() WHERE id=$1`, order.ID)
	}
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE refund_application SET status='COMPLETED',completed_at=now(),updated_at=now()
		WHERE refund_order_id=$1 AND status='PROCESSING'`, refund.ID)
	if err != nil {
		return domain.RefundOrder{}, false, err
	}
	if err = writeAuditTx(ctx, tx, refund.CreatedBy, "payment.refund_completed", "refund_order", refund.ID, map[string]any{
		"recharge_order_id": order.ID, "wallet_transaction_id": transactionID, "ledger_journal_id": journalID,
	}); err != nil {
		return domain.RefundOrder{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RefundOrder{}, false, err
	}
	loaded, err := s.RefundOrderByID(ctx, refund.ID)
	return loaded, false, err
}

func (s *Store) RecordReconciliation(ctx context.Context, order domain.RechargeOrder, key string, result payment.ReconcileResult, actor *string) (domain.PaymentReconciliationRecord, error) {
	if result.EvidenceSource != "PROVIDER_API" && result.EvidenceSource != "PROVIDER_SIGNED_WEBHOOK" &&
		result.EvidenceSource != "OPERATOR_VERIFIED_MANUAL" {
		return domain.PaymentReconciliationRecord{}, payment.ErrReconcileUnsupported
	}
	result.Details = paymentObject(result.Details)
	result.Details["evidence_source"] = result.EvidenceSource
	record := domain.PaymentReconciliationRecord{ID: id.UUID(), RechargeOrderID: &order.ID, PaymentProvider: order.PaymentProvider,
		ProviderOrderNo: order.ProviderOrderNo, ReconciliationKey: key, ProviderStatus: result.ProviderStatus,
		LocalStatus: order.Status, Currency: order.Currency, Details: result.Details, ReconciledBy: actor}
	localAmount, providerAmount := order.Amount, result.Amount
	record.LocalAmount, record.ProviderAmount = &localAmount, &providerAmount
	record.Result = "MATCHED"
	if result.ProviderStatus != expectedPaymentProviderStatus(order.Status) || !decimalEqual(result.Amount.String(), order.Amount.String()) || result.Currency != order.Currency {
		record.Result = "MISMATCH"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PaymentReconciliationRecord{}, err
	}
	defer tx.Rollback(ctx)
	var existing domain.PaymentReconciliationRecord
	var existingRechargeOrderID, existingProviderOrderNo, existingProviderAmount, existingLocalAmount string
	var existingDetailsMatch, existingActorMatch bool
	err = tx.QueryRow(ctx, `SELECT id,COALESCE(recharge_order_id::text,''),COALESCE(provider_order_no,''),provider_status,
		local_status,COALESCE(provider_amount,0)::text,COALESCE(local_amount,0)::text,currency,result,
		details=$3::jsonb,reconciled_by IS NOT DISTINCT FROM $4::uuid,reconciled_at
		FROM payment_reconciliation_record WHERE payment_provider=$1 AND reconciliation_key=$2 FOR UPDATE`,
		order.PaymentProvider, key, jsonBytes(result.Details), actor).Scan(&existing.ID, &existingRechargeOrderID, &existingProviderOrderNo,
		&existing.ProviderStatus, &existing.LocalStatus, &existingProviderAmount, &existingLocalAmount, &existing.Currency,
		&existing.Result, &existingDetailsMatch, &existingActorMatch, &existing.ReconciledAt)
	if err == nil {
		if existingRechargeOrderID != order.ID || existingProviderOrderNo != order.ProviderOrderNo ||
			existing.ProviderStatus != result.ProviderStatus || existing.LocalStatus != order.Status ||
			!decimalEqual(existingProviderAmount, result.Amount.String()) || !decimalEqual(existingLocalAmount, order.Amount.String()) ||
			existing.Currency != order.Currency || existing.Result != record.Result || !existingDetailsMatch || !existingActorMatch {
			return domain.PaymentReconciliationRecord{}, ErrIdempotencyConflict
		}
		existing.PaymentProvider = order.PaymentProvider
		existing.RechargeOrderID = &existingRechargeOrderID
		existing.ProviderOrderNo = existingProviderOrderNo
		existing.ProviderAmount, existing.LocalAmount = &providerAmount, &localAmount
		existing.Details = result.Details
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentReconciliationRecord{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO payment_reconciliation_record(id,recharge_order_id,payment_provider,provider_order_no,
		reconciliation_key,provider_status,local_status,provider_amount,local_amount,currency,result,details,reconciled_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id,reconciled_at`, record.ID, order.ID, order.PaymentProvider, nullString(order.ProviderOrderNo), key,
		result.ProviderStatus, order.Status, result.Amount.String(), order.Amount.String(), order.Currency, record.Result,
		jsonBytes(paymentObject(result.Details)), actor).Scan(&record.ID, &record.ReconciledAt)
	if err != nil {
		return domain.PaymentReconciliationRecord{}, err
	}
	return record, tx.Commit(ctx)
}

func expectedPaymentProviderStatus(localStatus string) string {
	switch localStatus {
	case "CREDITED", "REFUND_PENDING", "REFUNDED":
		return "PAID"
	default:
		return localStatus
	}
}

// RecordReconciliationFailure persists independent evidence that a scheduled
// channel query could not be performed. The daily SQL turns this immutable
// record into a queue case instead of letting adapter failures disappear in a
// worker log.
func (s *Store) RecordReconciliationFailure(ctx context.Context, order domain.RechargeOrder, key, errorCode string, details map[string]any) (domain.PaymentReconciliationRecord, error) {
	details = paymentObject(details)
	details["evidence_source"] = "PROVIDER_API_ERROR"
	details["error_code"] = strings.TrimSpace(errorCode)
	record := domain.PaymentReconciliationRecord{ID: id.UUID(), RechargeOrderID: &order.ID,
		PaymentProvider: order.PaymentProvider, ProviderOrderNo: order.ProviderOrderNo, ReconciliationKey: key,
		ProviderStatus: "ERROR", LocalStatus: order.Status, Currency: order.Currency, Result: "ERROR", Details: details}
	localAmount := order.Amount
	record.LocalAmount = &localAmount
	err := s.pool.QueryRow(ctx, `INSERT INTO payment_reconciliation_record(id,recharge_order_id,payment_provider,provider_order_no,
		reconciliation_key,provider_status,local_status,local_amount,currency,result,details)
		VALUES($1,$2,$3,$4,$5,'ERROR',$6,$7,$8,'ERROR',$9)
		ON CONFLICT(payment_provider,reconciliation_key) DO UPDATE SET reconciliation_key=EXCLUDED.reconciliation_key
		RETURNING id,reconciled_at`, record.ID, order.ID, order.PaymentProvider, nullString(order.ProviderOrderNo), key,
		order.Status, order.Amount.String(), order.Currency, jsonBytes(details)).Scan(&record.ID, &record.ReconciledAt)
	return record, err
}

func paymentObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
