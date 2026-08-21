package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrFinanceState       = errors.New("financial request state does not allow this operation")
	ErrRefundNotEligible  = errors.New("the requested amount is not refundable")
	ErrInvoiceAmount      = errors.New("invoice amount exceeds eligible settled revenue")
	ErrStatementMismatch  = errors.New("provider statement total does not equal its lines")
	ErrFinanceExportLimit = errors.New("finance export exceeds the maximum row count")
)

type CreateRefundApplicationRequest struct {
	OrganizationID        string
	SourceType            string
	RechargeOrderID       string
	SubscriptionInvoiceID string
	Amount                string
	Reason                string
	IdempotencyKey        string
	RequestedBy           string
}

type CreateInvoiceApplicationRequest struct {
	OrganizationID string
	InvoiceTitle   string
	TaxIdentifier  string
	Amount         string
	Currency       string
	PeriodStart    time.Time
	PeriodEnd      time.Time
	IdempotencyKey string
	RequestedBy    string
}

type FinanceFilter struct {
	OrganizationID  string
	Status          string
	Query           string
	Month           string
	From            time.Time
	To              time.Time
	Limit           int
	Offset          int
	RejectTruncated bool
}

const financeInteractiveMaximumRows = 200

type AccountingRow struct {
	PostedAt    time.Time
	JournalID   string
	ExternalKey string
	JournalType string
	AccountKey  string
	AccountName string
	Debit       string
	Credit      string
	Currency    string
	Reference   string
	CaseID      string
}

type InvoiceExportBatch struct {
	ID             string
	BatchKey       string
	OrganizationID *string
	RequestSHA256  string
	ArtifactSHA256 string
	Artifact       []byte
	RowCount       int
	CreatedBy      string
	CreatedAt      time.Time
}

const invoiceExportFilename = "modeldock-invoice-applications.csv"

type ProviderStatementInput struct {
	ProviderID         string
	StatementReference string
	PeriodStart        time.Time
	PeriodEnd          time.Time
	Region             string
	Currency           string
	TotalAmount        string
	SourceSHA256       string
	ImportedBy         string
	Lines              []ProviderStatementLineInput
}

type ProviderStatementLineInput struct {
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

func (s *Store) WalletBalanceComposition(ctx context.Context, organizationID string) (domain.WalletBalanceComposition, error) {
	var out domain.WalletBalanceComposition
	var cash, refundable, bonus, creditUsed, creditAvailable, gap string
	err := s.pool.QueryRow(ctx, `SELECT wallet.id,wallet.organization_id,wallet.currency,
		COALESCE((SELECT sum(remaining_amount) FROM wallet_cash_lot lot WHERE lot.wallet_id=wallet.id),0)::text,
		COALESCE((SELECT sum(remaining_amount) FROM wallet_cash_lot lot WHERE lot.wallet_id=wallet.id AND lot.refundable),0)::text,
		COALESCE((SELECT sum(amount_remaining) FROM promotion_credit credit WHERE credit.organization_id=wallet.organization_id
			AND credit.currency=wallet.currency AND credit.status='ACTIVE' AND (credit.expires_at IS NULL OR credit.expires_at>now())),0)::text,
		GREATEST(-wallet.available_balance,0)::text,
		GREATEST(wallet.credit_limit-GREATEST(-wallet.available_balance,0),0)::text,
		wallet.credit_limit::text,wallet.reserved_balance::text,wallet.available_balance::text,
		GREATEST(wallet.available_balance-COALESCE((SELECT sum(remaining_amount) FROM wallet_cash_lot lot WHERE lot.wallet_id=wallet.id),0),0)::text
		FROM wallets wallet WHERE wallet.organization_id=$1`, organizationID).
		Scan(&out.WalletID, &out.OrganizationID, &out.Currency, &cash, &refundable, &bonus, &creditUsed,
			&creditAvailable, &out.CreditLimit, &out.ReservedBalance, &out.AggregateBalance, &gap)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.CashAvailable, out.RefundableCash, out.BonusAvailable = cash, refundable, bonus
	out.CreditUsed, out.CreditAvailable, out.AttributionGap, out.AsOf = creditUsed, creditAvailable, gap, time.Now().UTC()
	return out, nil
}

// reserveCashLotsTx mirrors an aggregate wallet reservation onto its FIFO
// source lots.  The wallet row is always locked by the caller first; locking
// lots only after that preserves one lock order across reserve, refund, and
// chargeback paths.  Any remainder is credit, so it has no cash-lot evidence.
func reserveCashLotsTx(ctx context.Context, tx pgx.Tx, operationID, walletID, amount string) error {
	remaining := mustFundingRat(amount)
	if remaining.Sign() <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id,remaining_amount::text FROM wallet_cash_lot
		WHERE wallet_id=$1 AND remaining_amount>0 ORDER BY created_at,id FOR UPDATE`, walletID)
	if err != nil {
		return err
	}
	type lot struct{ id, amount string }
	lots := make([]lot, 0)
	for rows.Next() {
		var item lot
		if err = rows.Scan(&item.id, &item.amount); err != nil {
			rows.Close()
			return err
		}
		lots = append(lots, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range lots {
		if remaining.Sign() <= 0 {
			break
		}
		available := mustFundingRat(item.amount)
		take := new(big.Rat).Set(available)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		amountText := formatRat(take)
		if _, err = tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-$2::numeric WHERE id=$1`, item.id, amountText); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO funding_cash_allocation(id,operation_id,cash_lot_id,reserved_amount)
			VALUES($1,$2,$3,$4)`, id.UUID(), operationID, item.id, amountText); err != nil {
			return err
		}
		remaining.Sub(remaining, take)
	}
	return nil
}

// settleCashLotsTx finalizes the immutable reservation attribution. Cash is
// consumed from held lots up to the actual charge; unused holds return to the
// exact source lots.  If an estimate is exceeded, the overage is allocated
// from currently available FIFO cash and then credit.
func settleCashLotsTx(ctx context.Context, tx pgx.Tx, operationID, walletID, transactionID, actualAmount string) error {
	remaining := mustFundingRat(actualAmount)
	rows, err := tx.Query(ctx, `SELECT allocation.id,allocation.cash_lot_id,allocation.reserved_amount::text
		FROM funding_cash_allocation allocation WHERE allocation.operation_id=$1
		ORDER BY allocation.created_at,allocation.id FOR UPDATE`, operationID)
	if err != nil {
		return err
	}
	type allocation struct{ id, lotID, amount string }
	items := make([]allocation, 0)
	for rows.Next() {
		var item allocation
		if err = rows.Scan(&item.id, &item.lotID, &item.amount); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		reserved := mustFundingRat(item.amount)
		consumed := new(big.Rat)
		if remaining.Sign() > 0 {
			consumed.Set(reserved)
			if consumed.Cmp(remaining) > 0 {
				consumed.Set(remaining)
			}
		}
		released := new(big.Rat).Sub(new(big.Rat).Set(reserved), consumed)
		if released.Sign() > 0 {
			tag, updateErr := tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount+$2::numeric
				WHERE id=$1 AND wallet_id=$3 AND remaining_amount+$2::numeric<=original_amount`, item.lotID, formatRat(released), walletID)
			if updateErr != nil {
				return updateErr
			}
			if tag.RowsAffected() != 1 {
				return ErrFinanceState
			}
		}
		if consumed.Sign() > 0 {
			if transactionID == "" {
				return ErrFinanceState
			}
			if _, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,cash_lot_id,bucket,direction,amount)
				VALUES($1,$2,$3,$4,'CASH','DEBIT',$5)`, id.UUID(), walletID, transactionID, item.lotID, formatRat(consumed)); err != nil {
				return err
			}
			remaining.Sub(remaining, consumed)
		}
		if _, err = tx.Exec(ctx, `UPDATE funding_cash_allocation SET consumed_amount=$2,released_amount=$3,settled_at=now()
			WHERE id=$1`, item.id, formatRat(consumed), formatRat(released)); err != nil {
			return err
		}
	}
	if remaining.Sign() > 0 {
		return allocateCashDebitTx(ctx, tx, walletID, transactionID, formatRat(remaining))
	}
	return nil
}

// debitRechargeCashLotTx removes up to amount from the named recharge lot and
// writes the debit attribution. It is used by chargebacks, whose economic
// amount may exceed the still-unused lot after service consumption.
func debitRechargeCashLotTx(ctx context.Context, tx pgx.Tx, walletID, rechargeOrderID, transactionID, amount string) (string, error) {
	var lotID, available string
	err := tx.QueryRow(ctx, `SELECT id,remaining_amount::text FROM wallet_cash_lot
		WHERE wallet_id=$1 AND recharge_order_id=$2 FOR UPDATE`, walletID, rechargeOrderID).Scan(&lotID, &available)
	if errors.Is(err, pgx.ErrNoRows) {
		return "0", nil
	}
	if err != nil {
		return "", err
	}
	take := mustFundingRat(amount)
	if take.Cmp(mustFundingRat(available)) > 0 {
		take.Set(mustFundingRat(available))
	}
	if take.Sign() <= 0 {
		return "0", nil
	}
	amountText := formatRat(take)
	if _, err = tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-$2::numeric WHERE id=$1`, lotID, amountText); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,cash_lot_id,bucket,direction,amount)
		VALUES($1,$2,$3,$4,'CASH','DEBIT',$5)`, id.UUID(), walletID, transactionID, lotID, amountText); err != nil {
		return "", err
	}
	return amountText, nil
}

// allocateChargebackTx attributes the complete economic debit. The portion
// still present in the original recharge lot is cash; service-consumed or
// otherwise unavailable source cash becomes a credit/receivable debit instead
// of being silently absent from the transaction evidence.
func allocateChargebackTx(ctx context.Context, tx pgx.Tx, walletID, rechargeOrderID, transactionID, amount string) (string, string, error) {
	cashDebited, err := debitRechargeCashLotTx(ctx, tx, walletID, rechargeOrderID, transactionID, amount)
	if err != nil {
		return "", "", err
	}
	creditDebited := new(big.Rat).Sub(mustFundingRat(amount), mustFundingRat(cashDebited))
	if creditDebited.Sign() > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,bucket,direction,amount)
			VALUES($1,$2,$3,'CREDIT','DEBIT',$4)`, id.UUID(), walletID, transactionID, formatRat(creditDebited)); err != nil {
			return "", "", err
		}
	}
	return cashDebited, formatRat(creditDebited), nil
}

func reserveRefundCashTx(ctx context.Context, tx pgx.Tx, refundOrderID, walletID, rechargeOrderID, amount string) error {
	var lotID, available string
	err := tx.QueryRow(ctx, `SELECT id,remaining_amount::text FROM wallet_cash_lot
		WHERE wallet_id=$1 AND recharge_order_id=$2 AND refundable FOR UPDATE`, walletID, rechargeOrderID).Scan(&lotID, &available)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && mustFundingRat(amount).Cmp(mustFundingRat(available)) > 0 {
		return ErrRefundNotEligible
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-$2::numeric WHERE id=$1`, lotID, amount); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO refund_cash_allocation(id,refund_order_id,cash_lot_id,reserved_amount)
		VALUES($1,$2,$3,$4)`, id.UUID(), refundOrderID, lotID, amount)
	return err
}

func settleRefundCashTx(ctx context.Context, tx pgx.Tx, refundOrderID, walletID, transactionID string, succeeded bool) error {
	var allocationID, lotID, amount string
	err := tx.QueryRow(ctx, `SELECT id,cash_lot_id,reserved_amount::text FROM refund_cash_allocation
		WHERE refund_order_id=$1 FOR UPDATE`, refundOrderID).Scan(&allocationID, &lotID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrFinanceState
	}
	if err != nil {
		return err
	}
	if succeeded {
		if transactionID == "" {
			return ErrFinanceState
		}
		if _, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,cash_lot_id,bucket,direction,amount)
			VALUES($1,$2,$3,$4,'CASH','DEBIT',$5)`, id.UUID(), walletID, transactionID, lotID, amount); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE refund_cash_allocation SET consumed_amount=reserved_amount,settled_at=now() WHERE id=$1`, allocationID)
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount+$2::numeric
		WHERE id=$1 AND wallet_id=$3 AND remaining_amount+$2::numeric<=original_amount`, lotID, amount, walletID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrFinanceState
	}
	_, err = tx.Exec(ctx, `UPDATE refund_cash_allocation SET released_amount=reserved_amount,settled_at=now() WHERE id=$1`, allocationID)
	return err
}

// reservePromotionTx moves promotional credit out of the generally available
// pool before a provider request starts. The row locks make concurrent
// requests deterministic; releasePromotionTx returns unused reservations.
func reservePromotionTx(ctx context.Context, tx pgx.Tx, operationID, organizationID, currency, maximum string) (string, error) {
	remaining := mustFundingRat(maximum)
	if remaining.Sign() <= 0 {
		return "0", nil
	}
	rows, err := tx.Query(ctx, `SELECT id,amount_remaining::text FROM promotion_credit
		WHERE organization_id=$1 AND currency=$2 AND status='ACTIVE' AND amount_remaining>0
		AND (expires_at IS NULL OR expires_at>now()) ORDER BY expires_at NULLS LAST,created_at,id FOR UPDATE`, organizationID, currency)
	if err != nil {
		return "", err
	}
	type credit struct{ id, amount string }
	credits := make([]credit, 0)
	for rows.Next() {
		var item credit
		if err = rows.Scan(&item.id, &item.amount); err != nil {
			rows.Close()
			return "", err
		}
		credits = append(credits, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	reserved := new(big.Rat)
	for _, item := range credits {
		if remaining.Sign() <= 0 {
			break
		}
		available := mustFundingRat(item.amount)
		take := new(big.Rat).Set(available)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		amount := formatRat(take)
		_, err = tx.Exec(ctx, `UPDATE promotion_credit SET amount_remaining=amount_remaining-$2::numeric,
			status=CASE WHEN amount_remaining-$2::numeric=0 THEN 'EXHAUSTED' ELSE status END,updated_at=now() WHERE id=$1`, item.id, amount)
		if err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, `INSERT INTO funding_promotion_allocation(id,operation_id,promotion_credit_id,reserved_amount)
			VALUES($1,$2,$3,$4)`, id.UUID(), operationID, item.id, amount)
		if err != nil {
			return "", err
		}
		remaining.Sub(remaining, take)
		reserved.Add(reserved, take)
	}
	return formatRat(reserved), nil
}

func settlePromotionTx(ctx context.Context, tx pgx.Tx, operationID, actualPromotion string) (string, error) {
	remaining := mustFundingRat(actualPromotion)
	rows, err := tx.Query(ctx, `SELECT allocation.id,allocation.promotion_credit_id,allocation.reserved_amount::text
		FROM funding_promotion_allocation allocation WHERE allocation.operation_id=$1 ORDER BY allocation.created_at,allocation.id FOR UPDATE`, operationID)
	if err != nil {
		return "", err
	}
	type allocation struct{ id, creditID, amount string }
	items := make([]allocation, 0)
	for rows.Next() {
		var v allocation
		if err = rows.Scan(&v.id, &v.creditID, &v.amount); err != nil {
			rows.Close()
			return "", err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	consumed := new(big.Rat)
	for _, item := range items {
		reserved := mustFundingRat(item.amount)
		use := new(big.Rat)
		if remaining.Sign() > 0 {
			use.Set(reserved)
			if use.Cmp(remaining) > 0 {
				use.Set(remaining)
			}
		}
		release := new(big.Rat).Sub(new(big.Rat).Set(reserved), use)
		_, err = tx.Exec(ctx, `UPDATE funding_promotion_allocation SET consumed_amount=$2,released_amount=$3,settled_at=now() WHERE id=$1`, item.id, formatRat(use), formatRat(release))
		if err != nil {
			return "", err
		}
		if release.Sign() > 0 {
			_, err = tx.Exec(ctx, `UPDATE promotion_credit SET amount_remaining=amount_remaining+$2::numeric,status='ACTIVE',updated_at=now() WHERE id=$1`, item.creditID, formatRat(release))
			if err != nil {
				return "", err
			}
		}
		remaining.Sub(remaining, use)
		consumed.Add(consumed, use)
	}
	return formatRat(consumed), nil
}

func releaseAllPromotionTx(ctx context.Context, tx pgx.Tx, operationID string) error {
	_, err := settlePromotionTx(ctx, tx, operationID, "0")
	return err
}

func reversePromotionTx(ctx context.Context, tx pgx.Tx, operationID, requested string) (string, error) {
	remaining := mustFundingRat(requested)
	if remaining.Sign() <= 0 {
		return "0", nil
	}
	rows, err := tx.Query(ctx, `SELECT allocation.id,allocation.promotion_credit_id,
		(allocation.consumed_amount-allocation.reversed_amount)::text
		FROM funding_promotion_allocation allocation WHERE allocation.operation_id=$1
		AND allocation.consumed_amount>allocation.reversed_amount ORDER BY allocation.created_at DESC,allocation.id DESC FOR UPDATE`, operationID)
	if err != nil {
		return "", err
	}
	type allocation struct{ id, creditID, amount string }
	items := make([]allocation, 0)
	for rows.Next() {
		var v allocation
		if err = rows.Scan(&v.id, &v.creditID, &v.amount); err != nil {
			rows.Close()
			return "", err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	reversed := new(big.Rat)
	for _, item := range items {
		if remaining.Sign() <= 0 {
			break
		}
		take := new(big.Rat).Set(mustFundingRat(item.amount))
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		amountText := formatRat(take)
		if _, err = tx.Exec(ctx, `UPDATE funding_promotion_allocation SET reversed_amount=reversed_amount+$2::numeric WHERE id=$1`, item.id, amountText); err != nil {
			return "", err
		}
		if _, err = tx.Exec(ctx, `UPDATE promotion_credit SET amount_remaining=amount_remaining+$2::numeric,
			status=CASE WHEN expires_at IS NOT NULL AND expires_at<=now() THEN 'EXPIRED' ELSE 'ACTIVE' END,updated_at=now() WHERE id=$1`, item.creditID, amountText); err != nil {
			return "", err
		}
		remaining.Sub(remaining, take)
		reversed.Add(reversed, take)
	}
	if remaining.Sign() > 0 {
		return "", ErrFinanceState
	}
	return formatRat(reversed), nil
}

// allocateCashDebitTx consumes refundable cash FIFO and records any remainder
// as credit usage. This evidence is linked to the immutable wallet transaction.
func allocateCashDebitTx(ctx context.Context, tx pgx.Tx, walletID, transactionID, amount string) error {
	remaining := mustFundingRat(amount)
	if remaining.Sign() <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id,remaining_amount::text FROM wallet_cash_lot WHERE wallet_id=$1 AND remaining_amount>0 ORDER BY created_at,id FOR UPDATE`, walletID)
	if err != nil {
		return err
	}
	type lot struct{ id, amount string }
	lots := []lot{}
	for rows.Next() {
		var v lot
		if err = rows.Scan(&v.id, &v.amount); err != nil {
			rows.Close()
			return err
		}
		lots = append(lots, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range lots {
		if remaining.Sign() <= 0 {
			break
		}
		available := mustFundingRat(item.amount)
		take := new(big.Rat).Set(available)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		amountText := formatRat(take)
		_, err = tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-$2::numeric WHERE id=$1`, item.id, amountText)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,cash_lot_id,bucket,direction,amount) VALUES($1,$2,$3,$4,'CASH','DEBIT',$5)`, id.UUID(), walletID, transactionID, item.id, amountText)
		if err != nil {
			return err
		}
		remaining.Sub(remaining, take)
	}
	if remaining.Sign() > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,bucket,direction,amount) VALUES($1,$2,$3,'CREDIT','DEBIT',$4)`, id.UUID(), walletID, transactionID, formatRat(remaining))
	}
	return err
}

func createCashLotTx(ctx context.Context, tx pgx.Tx, walletID, rechargeOrderID, transactionID, sourceKind, sourceReference, amount, currency string, refundable bool) error {
	_, err := tx.Exec(ctx, `INSERT INTO wallet_cash_lot(id,wallet_id,recharge_order_id,source_transaction_id,source_kind,source_reference,original_amount,remaining_amount,currency,refundable) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$8,$9) ON CONFLICT DO NOTHING`, id.UUID(), walletID, nullString(rechargeOrderID), nullString(transactionID), sourceKind, sourceReference, amount, currency, refundable)
	return err
}

func refundCashLotTx(ctx context.Context, tx pgx.Tx, walletID, rechargeOrderID, transactionID, amount string) error {
	var lotID, remaining string
	err := tx.QueryRow(ctx, `SELECT id,remaining_amount::text FROM wallet_cash_lot WHERE wallet_id=$1 AND recharge_order_id=$2 AND refundable FOR UPDATE`, walletID, rechargeOrderID).Scan(&lotID, &remaining)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && mustFundingRat(amount).Cmp(mustFundingRat(remaining)) > 0 {
		return ErrRefundNotEligible
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount-$2::numeric WHERE id=$1`, lotID, amount)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,cash_lot_id,bucket,direction,amount) VALUES($1,$2,$3,$4,'CASH','DEBIT',$5)`, id.UUID(), walletID, transactionID, lotID, amount)
	return err
}

// restoreCashAllocationTx mirrors the original cash/credit attribution for a
// reversal. It never edits the original allocation: RESTORE rows provide the
// append-only evidence and the source lots regain the exact cash portion.
func restoreCashAllocationTx(ctx context.Context, tx pgx.Tx, walletID, sourceTransactionID, reversalTransactionID, amount string) error {
	remaining := mustFundingRat(amount)
	if remaining.Sign() <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT allocation.id,allocation.cash_lot_id,allocation.bucket,
		(allocation.amount-COALESCE((SELECT sum(restored.amount) FROM wallet_cash_allocation restored WHERE restored.source_allocation_id=allocation.id),0))::text
		FROM wallet_cash_allocation allocation WHERE allocation.wallet_id=$1 AND allocation.wallet_transaction_id=$2 AND allocation.direction='DEBIT'
		AND allocation.amount>COALESCE((SELECT sum(restored.amount) FROM wallet_cash_allocation restored WHERE restored.source_allocation_id=allocation.id),0)
		ORDER BY allocation.created_at DESC,allocation.id DESC FOR SHARE`, walletID, sourceTransactionID)
	if err != nil {
		return err
	}
	type allocation struct {
		id             string
		lotID          *string
		bucket, amount string
	}
	items := make([]allocation, 0)
	for rows.Next() {
		var v allocation
		if err = rows.Scan(&v.id, &v.lotID, &v.bucket, &v.amount); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range items {
		if remaining.Sign() <= 0 {
			break
		}
		take := new(big.Rat).Set(mustFundingRat(item.amount))
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		amountText := formatRat(take)
		if item.bucket == "CASH" {
			if item.lotID == nil {
				return ErrFinanceState
			}
			if _, err = tx.Exec(ctx, `UPDATE wallet_cash_lot SET remaining_amount=remaining_amount+$2::numeric
				WHERE id=$1 AND wallet_id=$3 AND remaining_amount+$2::numeric<=original_amount`, *item.lotID, amountText, walletID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO wallet_cash_allocation(id,wallet_id,wallet_transaction_id,cash_lot_id,source_allocation_id,bucket,direction,amount)
			VALUES($1,$2,$3,$4,$5,$6,'RESTORE',$7)`, id.UUID(), walletID, reversalTransactionID, item.lotID, item.id, item.bucket, amountText)
		if err != nil {
			return err
		}
		remaining.Sub(remaining, take)
	}
	if remaining.Sign() > 0 {
		return ErrFinanceState
	}
	return nil
}

func (s *Store) ListFinanceUsage(ctx context.Context, filter FinanceFilter) ([]domain.FinanceUsageDetail, error) {
	return s.listFinanceUsage(ctx, filter, financeInteractiveMaximumRows)
}

func (s *Store) ListFinanceUsageExport(ctx context.Context, filter FinanceFilter) ([]domain.FinanceUsageDetail, error) {
	return s.listFinanceUsage(ctx, filter, 100000)
}

func (s *Store) listFinanceUsage(ctx context.Context, filter FinanceFilter, maximum int) ([]domain.FinanceUsageDetail, error) {
	query := `SELECT usage.request_id,usage.organization_id,usage.project_id,COALESCE(usage.provider_id::text,''),
		COALESCE(provider.name,''),usage.model,usage.input_tokens,usage.cached_input_tokens,usage.output_tokens,
		(COALESCE(usage.final_user_amount,usage.amount,0)+COALESCE(usage.promotion_amount,0))::text,COALESCE(usage.promotion_amount,0)::text,
		COALESCE(usage.final_user_amount,usage.amount,0)::text,
		COALESCE(usage.provider_cost_amount,0)::text,
		(COALESCE(usage.customer_sale_amount,usage.amount,0)-COALESCE(usage.provider_cost_amount,0))::text,
		usage.currency,COALESCE(snapshot.provider_currency,usage.currency),usage.funding_operation_id,
		transaction.id,journal.id,COALESCE(attempt.upstream_request_id,''),COALESCE(attempt.status,''),usage.created_at
		FROM billing_usage_records usage
		LEFT JOIN providers provider ON provider.id=usage.provider_id
		LEFT JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
		LEFT JOIN wallet_transactions transaction ON transaction.funding_operation_id=usage.funding_operation_id
		LEFT JOIN ledger_journal journal ON journal.id=transaction.journal_id
		LEFT JOIN LATERAL (SELECT upstream_request_id,status FROM funding_provider_attempt attempt
			WHERE attempt.operation_id=usage.funding_operation_id ORDER BY attempt.attempt_no DESC LIMIT 1) attempt ON true
		WHERE ($1::text='' OR usage.organization_id=$1::uuid) AND ($2::timestamptz IS NULL OR usage.created_at>=$2)
		AND ($3::timestamptz IS NULL OR usage.created_at<$3) ORDER BY usage.created_at DESC,usage.id DESC LIMIT $4 OFFSET $5`
	rows, err := s.pool.Query(ctx, query, filter.OrganizationID, nullableFinanceTime(filter.From), nullableFinanceTime(filter.To), clampFinanceLimitTo(filter.Limit, maximum), max(filter.Offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.FinanceUsageDetail, 0)
	for rows.Next() {
		var v domain.FinanceUsageDetail
		if err = rows.Scan(&v.RequestID, &v.OrganizationID, &v.ProjectID, &v.ProviderID, &v.ProviderName, &v.Model,
			&v.InputTokens, &v.CachedInputTokens, &v.OutputTokens, &v.CustomerCharge, &v.PromotionAmount,
			&v.CashCharge, &v.ProviderCost, &v.GrossMargin, &v.Currency, &v.ProviderCurrency,
			&v.FundingOperationID, &v.WalletTransactionID, &v.LedgerJournalID, &v.UpstreamRequestID,
			&v.ProviderAttemptStatus, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Finance export readers use the explicit 100,000-row finance ceiling rather
// than the 200-row interactive-list ceiling. Date predicates are applied in
// PostgreSQL so a busy organization's target month cannot be truncated by
// newer activity before filtering.
func (s *Store) ListRechargeOrdersExport(ctx context.Context, organizationID string, from, to time.Time, limit int) ([]domain.RechargeOrder, error) {
	rows, err := s.pool.Query(ctx, rechargeOrderSelect+` WHERE organization_id=$1 AND created_at>=$2 AND created_at<$3
		ORDER BY created_at,id LIMIT $4`, organizationID, from.UTC(), to.UTC(), clampFinanceLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RechargeOrder, 0)
	for rows.Next() {
		item, scanErr := scanRechargeOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListSubscriptionInvoicesExport(ctx context.Context, organizationID string, from, to time.Time, limit int) ([]domain.SubscriptionInvoice, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,invoice_number,organization_id,organization_subscription_id,plan_version_id,coupon_id,
		invoice_type,status,subtotal::text,discount_amount::text,tax_amount::text,total_amount::text,currency,period_start,period_end,
		due_at,paid_at,failed_at,COALESCE(payment_provider,''),COALESCE(provider_payment_reference,''),ledger_journal_id,
		plan_snapshot,created_by,created_at,updated_at FROM subscription_invoice
		WHERE organization_id=$1 AND created_at>=$2 AND created_at<$3 ORDER BY created_at,id LIMIT $4`,
		organizationID, from.UTC(), to.UTC(), clampFinanceLimit(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SubscriptionInvoice, 0)
	for rows.Next() {
		item, scanErr := scanSubscriptionInvoice(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) MonthlyStatements(ctx context.Context, organizationID, month string, limit int) ([]domain.MonthlyStatement, error) {
	if month != "" {
		if _, err := time.Parse("2006-01", month); err != nil {
			return nil, errors.New("month must use YYYY-MM")
		}
	}
	rows, err := s.pool.Query(ctx, `WITH months AS (
		SELECT DISTINCT date_trunc('month',created_at)::date statement_month,currency FROM (
			SELECT created_at,currency FROM recharge_order WHERE organization_id=$1
			UNION ALL SELECT created_at,currency FROM billing_usage_records WHERE organization_id=$1
			UNION ALL SELECT created_at,currency FROM subscription_invoice WHERE organization_id=$1) evidence
		WHERE ($2='' OR to_char(date_trunc('month',created_at),'YYYY-MM')=$2)
	), wallet AS (SELECT id FROM wallets WHERE organization_id=$1)
	SELECT $1,months.statement_month,months.currency,
		COALESCE((SELECT sum(amount) FROM wallet_transactions transaction JOIN wallet ON wallet.id=transaction.wallet_id
		 WHERE transaction.created_at<months.statement_month),0)::text,
		COALESCE((SELECT sum(amount) FROM recharge_order WHERE organization_id=$1 AND status IN ('CREDITED','REFUND_PENDING','REFUNDED','CHARGEBACK')
		 AND currency=months.currency AND credited_at>=months.statement_month AND credited_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(COALESCE(final_user_amount,amount)) FROM billing_usage_records WHERE organization_id=$1
		 AND currency=months.currency AND created_at>=months.statement_month AND created_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(promotion_amount) FROM billing_usage_records WHERE organization_id=$1
		 AND currency=months.currency AND created_at>=months.statement_month AND created_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(total_amount) FROM subscription_invoice WHERE organization_id=$1 AND status='PAID'
		 AND currency=months.currency AND paid_at>=months.statement_month AND paid_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(refund.amount) FROM refund_order refund JOIN recharge_order recharge ON recharge.id=refund.recharge_order_id
		 WHERE recharge.organization_id=$1 AND refund.status='SUCCEEDED' AND refund.currency=months.currency
		 AND refund.completed_at>=months.statement_month AND refund.completed_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(amount) FROM wallet_transactions transaction JOIN wallet ON wallet.id=transaction.wallet_id
		 WHERE transaction.created_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(provider_cost_amount) FROM billing_usage_records WHERE organization_id=$1
		 AND currency=months.currency AND created_at>=months.statement_month AND created_at<months.statement_month+interval '1 month'),0)::text,
		COALESCE((SELECT sum(COALESCE(customer_sale_amount,amount)-COALESCE(provider_cost_amount,0)) FROM billing_usage_records
		 WHERE organization_id=$1 AND currency=months.currency AND created_at>=months.statement_month AND created_at<months.statement_month+interval '1 month'),0)::text,
		(SELECT count(*) FROM billing_usage_records WHERE organization_id=$1 AND currency=months.currency
		 AND created_at>=months.statement_month AND created_at<months.statement_month+interval '1 month')
	FROM months ORDER BY months.statement_month DESC,months.currency LIMIT $3`, organizationID, month, clamp(limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.MonthlyStatement, 0)
	for rows.Next() {
		var v domain.MonthlyStatement
		var monthDate time.Time
		if err = rows.Scan(&v.OrganizationID, &monthDate, &v.Currency, &v.OpeningBalance, &v.RechargeAmount,
			&v.UsageCharge, &v.PromotionAmount, &v.SubscriptionAmount, &v.RefundAmount, &v.ClosingBalance,
			&v.ProviderCost, &v.GrossMargin, &v.RequestCount); err != nil {
			return nil, err
		}
		v.Month = monthDate.Format("2006-01")
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CreateRefundApplication(ctx context.Context, request CreateRefundApplicationRequest) (domain.RefundApplication, bool, error) {
	request.SourceType = strings.ToUpper(strings.TrimSpace(request.SourceType))
	request.Reason = strings.TrimSpace(request.Reason)
	amount, ok := new(big.Rat).SetString(request.Amount)
	if !ok || amount.Sign() <= 0 || request.OrganizationID == "" || request.RequestedBy == "" || request.IdempotencyKey == "" || request.Reason == "" {
		return domain.RefundApplication{}, false, ErrFinanceState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RefundApplication{}, false, err
	}
	defer tx.Rollback(ctx)
	// Serialize refund eligibility with invoice source attribution for the same
	// organization and currency. The concrete currency is resolved below, so
	// recharge/subscription branches acquire this lock immediately after their
	// source row is locked.
	existing, scanErr := scanRefundApplication(tx.QueryRow(ctx, refundApplicationSelect+` WHERE organization_id=$1 AND idempotency_key=$2`, request.OrganizationID, request.IdempotencyKey))
	if scanErr == nil {
		if existing.SourceType != request.SourceType || !decimalEqual(existing.RequestedAmount, request.Amount) || existing.Reason != request.Reason ||
			derefFinanceID(existing.RechargeOrderID) != request.RechargeOrderID || derefFinanceID(existing.SubscriptionInvoiceID) != request.SubscriptionInvoiceID {
			return domain.RefundApplication{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(scanErr, pgx.ErrNoRows) && !errors.Is(scanErr, ErrNotFound) {
		return domain.RefundApplication{}, false, scanErr
	}
	application := domain.RefundApplication{ID: id.UUID(), ApplicationNumber: financeNumber("RFA"), OrganizationID: request.OrganizationID,
		SourceType: request.SourceType, RequestedAmount: request.Amount, Reason: request.Reason,
		IdempotencyKey: request.IdempotencyKey, Status: "SUBMITTED", RequestedBy: request.RequestedBy}
	if request.SourceType == "RECHARGE" {
		var orderAmount, currency, refundable, pending string
		var orderID string
		err = tx.QueryRow(ctx, `SELECT recharge.id,recharge.amount::text,recharge.currency,
			COALESCE(lot.remaining_amount,0)::text FROM recharge_order recharge
			LEFT JOIN wallet_cash_lot lot ON lot.recharge_order_id=recharge.id
			WHERE recharge.id=$1 AND recharge.organization_id=$2 AND recharge.status IN ('CREDITED','REFUND_PENDING') FOR UPDATE OF recharge`,
			request.RechargeOrderID, request.OrganizationID).Scan(&orderID, &orderAmount, &currency, &refundable)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RefundApplication{}, false, ErrRefundNotEligible
		}
		if err != nil {
			return domain.RefundApplication{}, false, err
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "refund-invoice:"+request.OrganizationID+":"+currency); err != nil {
			return domain.RefundApplication{}, false, err
		}
		var invoiced string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(item.amount),0)::text FROM invoice_application_item item
			JOIN invoice_application application ON application.id=item.invoice_application_id
			WHERE item.recharge_order_id=$1 AND application.status NOT IN ('REJECTED','CANCELED')`, orderID).Scan(&invoiced); err != nil {
			return domain.RefundApplication{}, false, err
		}
		refundableAmount := mustFundingRat(refundable)
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(requested_amount),0)::text FROM refund_application
			WHERE recharge_order_id=$1 AND status NOT IN ('REJECTED','CANCELED','FAILED')`, orderID).Scan(&pending); err != nil {
			return domain.RefundApplication{}, false, err
		}
		claimed := new(big.Rat).Add(mustFundingRat(pending), mustFundingRat(invoiced))
		if new(big.Rat).Add(amount, claimed).Cmp(refundableAmount) > 0 {
			return domain.RefundApplication{}, false, ErrRefundNotEligible
		}
		application.RechargeOrderID = &orderID
		application.Currency, application.UnusedCashAmount = currency, request.Amount
		application.UsedServiceAmount = formatRat(new(big.Rat).Sub(mustFundingRat(orderAmount), refundableAmount))
		var providerCost string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(attributed.provider_cost),0)::text FROM (
			SELECT usage.id,usage.provider_cost_amount*
				(sum(allocation.amount) FILTER (WHERE lot.recharge_order_id=$1)/NULLIF(sum(allocation.amount),0)) provider_cost
			FROM billing_usage_records usage
			JOIN wallet_transactions transaction ON transaction.funding_operation_id=usage.funding_operation_id
				AND transaction.transaction_type='CHARGE'
			JOIN wallet_cash_allocation allocation ON allocation.wallet_transaction_id=transaction.id
				AND allocation.bucket='CASH' AND allocation.direction='DEBIT'
			JOIN wallet_cash_lot lot ON lot.id=allocation.cash_lot_id
			GROUP BY usage.id,usage.provider_cost_amount
			HAVING sum(allocation.amount) FILTER (WHERE lot.recharge_order_id=$1)>0
		) attributed`, orderID).Scan(&providerCost); err != nil {
			return domain.RefundApplication{}, false, err
		}
		application.ProviderIrrecoverableCost = zeroIfEmpty(providerCost)
	} else if request.SourceType == "SUBSCRIPTION" {
		var invoiceID, total, currency, alreadyRequested string
		err = tx.QueryRow(ctx, `SELECT id,total_amount::text,currency FROM subscription_invoice
			WHERE id=$1 AND organization_id=$2 AND status='PAID' FOR UPDATE`, request.SubscriptionInvoiceID, request.OrganizationID).
			Scan(&invoiceID, &total, &currency)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RefundApplication{}, false, ErrRefundNotEligible
		}
		if err != nil {
			return domain.RefundApplication{}, false, err
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "refund-invoice:"+request.OrganizationID+":"+currency); err != nil {
			return domain.RefundApplication{}, false, err
		}
		var invoiced string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(item.amount),0)::text FROM invoice_application_item item
			JOIN invoice_application application ON application.id=item.invoice_application_id
			WHERE item.subscription_invoice_id=$1 AND application.status NOT IN ('REJECTED','CANCELED')`, invoiceID).Scan(&invoiced); err != nil {
			return domain.RefundApplication{}, false, err
		}
		// The paid invoice row is locked before the aggregate is read. This
		// serializes concurrent applications for the same subscription payment
		// and prevents their non-rejected total from exceeding the paid amount.
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(requested_amount),0)::text
			FROM refund_application WHERE subscription_invoice_id=$1
			AND status NOT IN ('REJECTED','CANCELED','FAILED')`, invoiceID).Scan(&alreadyRequested); err != nil {
			return domain.RefundApplication{}, false, err
		}
		claimed := new(big.Rat).Add(mustFundingRat(alreadyRequested), mustFundingRat(invoiced))
		if new(big.Rat).Add(amount, claimed).Cmp(mustFundingRat(total)) > 0 {
			return domain.RefundApplication{}, false, ErrRefundNotEligible
		}
		application.SubscriptionInvoiceID, application.Currency, application.SubscriptionFeeAmount = &invoiceID, currency, request.Amount
	} else {
		return domain.RefundApplication{}, false, ErrFinanceState
	}
	if application.UsedServiceAmount == "" {
		application.UsedServiceAmount = "0"
	}
	if application.BonusAmount == "" {
		application.BonusAmount = "0"
	}
	if application.SubscriptionFeeAmount == "" {
		application.SubscriptionFeeAmount = "0"
	}
	if application.ProviderIrrecoverableCost == "" {
		application.ProviderIrrecoverableCost = "0"
	}
	_, err = tx.Exec(ctx, `INSERT INTO refund_application(id,application_number,organization_id,source_type,recharge_order_id,
		subscription_invoice_id,requested_amount,currency,unused_cash_amount,used_service_amount,bonus_amount,
		subscription_fee_amount,provider_irrecoverable_cost,reason,idempotency_key,requested_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, application.ID, application.ApplicationNumber,
		application.OrganizationID, application.SourceType, application.RechargeOrderID, application.SubscriptionInvoiceID,
		application.RequestedAmount, application.Currency, zeroIfEmpty(application.UnusedCashAmount), application.UsedServiceAmount,
		application.BonusAmount, application.SubscriptionFeeAmount, application.ProviderIrrecoverableCost, application.Reason,
		application.IdempotencyKey, application.RequestedBy)
	if err != nil {
		return domain.RefundApplication{}, false, err
	}
	if err = writeAuditTx(ctx, tx, &request.RequestedBy, "finance.refund_application_submitted", "refund_application", application.ID, map[string]any{
		"source_type": application.SourceType, "requested_amount": application.RequestedAmount, "currency": application.Currency,
		"unused_cash_amount": application.UnusedCashAmount, "used_service_amount": application.UsedServiceAmount,
		"bonus_amount": application.BonusAmount, "subscription_fee_amount": application.SubscriptionFeeAmount,
		"provider_irrecoverable_cost": application.ProviderIrrecoverableCost,
	}); err != nil {
		return domain.RefundApplication{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.RefundApplication{}, false, err
	}
	loaded, err := s.RefundApplicationByID(ctx, application.ID)
	return loaded, false, err
}

const refundApplicationSelect = `SELECT id,application_number,organization_id,source_type,recharge_order_id,
	subscription_invoice_id,requested_amount::text,currency,unused_cash_amount::text,used_service_amount::text,
	bonus_amount::text,subscription_fee_amount::text,provider_irrecoverable_cost::text,reason,idempotency_key,status,
	refund_order_id,requested_by,reviewed_by,COALESCE(review_reason,''),reviewed_at,completed_at,created_at,updated_at FROM refund_application`

func scanRefundApplication(row pgx.Row) (domain.RefundApplication, error) {
	var v domain.RefundApplication
	err := row.Scan(&v.ID, &v.ApplicationNumber, &v.OrganizationID, &v.SourceType, &v.RechargeOrderID,
		&v.SubscriptionInvoiceID, &v.RequestedAmount, &v.Currency, &v.UnusedCashAmount, &v.UsedServiceAmount,
		&v.BonusAmount, &v.SubscriptionFeeAmount, &v.ProviderIrrecoverableCost, &v.Reason, &v.IdempotencyKey,
		&v.Status, &v.RefundOrderID, &v.RequestedBy, &v.ReviewedBy, &v.ReviewReason, &v.ReviewedAt,
		&v.CompletedAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func (s *Store) RefundApplicationByID(ctx context.Context, applicationID string) (domain.RefundApplication, error) {
	return scanRefundApplication(s.pool.QueryRow(ctx, refundApplicationSelect+` WHERE id=$1`, applicationID))
}

func (s *Store) ListRefundApplications(ctx context.Context, filter FinanceFilter) ([]domain.RefundApplication, error) {
	rows, err := s.pool.Query(ctx, refundApplicationSelect+` WHERE ($1='' OR organization_id=$1)
		AND ($2='' OR status=$2) ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, filter.OrganizationID,
		strings.ToUpper(filter.Status), clamp(filter.Limit), max(filter.Offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RefundApplication, 0)
	for rows.Next() {
		v, e := scanRefundApplication(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DecideRefundApplication(ctx context.Context, applicationID, decision, reason, idempotencyKey, actor string) (domain.RefundApplication, bool, error) {
	decision, reason = strings.ToUpper(strings.TrimSpace(decision)), strings.TrimSpace(reason)
	if (decision != "APPROVE" && decision != "REJECT") || reason == "" || idempotencyKey == "" || actor == "" {
		return domain.RefundApplication{}, false, ErrFinanceState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RefundApplication{}, false, err
	}
	defer tx.Rollback(ctx)
	v, err := scanRefundApplication(tx.QueryRow(ctx, refundApplicationSelect+` WHERE id=$1 FOR UPDATE`, applicationID))
	if err != nil {
		return v, false, err
	}
	if v.Status == "APPROVED" || v.Status == "REJECTED" {
		expectedStatus := "REJECTED"
		if decision == "APPROVE" {
			expectedStatus = "APPROVED"
		}
		if v.Status != expectedStatus || v.ReviewReason != reason || derefFinanceID(v.ReviewedBy) != actor {
			return v, false, ErrIdempotencyConflict
		}
		return v, true, tx.Commit(ctx)
	}
	if v.Status != "SUBMITTED" && v.Status != "UNDER_REVIEW" {
		return v, false, ErrFinanceState
	}
	status := "REJECTED"
	if decision == "APPROVE" {
		status = "APPROVED"
	}
	_, err = tx.Exec(ctx, `UPDATE refund_application SET status=$2,reviewed_by=$3,review_reason=$4,reviewed_at=now(),updated_at=now() WHERE id=$1`, v.ID, status, actor, reason)
	if err != nil {
		return v, false, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "finance.refund_application_"+strings.ToLower(status), "refund_application", v.ID, map[string]any{"reason": reason, "idempotency_key": idempotencyKey}); err != nil {
		return v, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return v, false, err
	}
	loaded, err := s.RefundApplicationByID(ctx, v.ID)
	return loaded, false, err
}

func (s *Store) LinkRefundApplication(ctx context.Context, applicationID, refundOrderID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE refund_application SET status='PROCESSING',refund_order_id=$2,updated_at=now()
		WHERE id=$1 AND status='APPROVED' AND refund_order_id IS NULL`, applicationID, refundOrderID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrFinanceState
	}
	return err
}

func (s *Store) CompleteRefundApplication(ctx context.Context, applicationID string, succeeded bool, reason string) error {
	status := "COMPLETED"
	if !succeeded {
		status = "FAILED"
	}
	_, err := s.pool.Exec(ctx, `UPDATE refund_application SET status=$2,completed_at=now(),review_reason=CASE WHEN $3='' THEN review_reason ELSE $3 END,updated_at=now()
		WHERE id=$1 AND status='PROCESSING'`, applicationID, status, reason)
	return err
}

func (s *Store) CreateInvoiceApplication(ctx context.Context, request CreateInvoiceApplicationRequest) (domain.InvoiceApplication, bool, error) {
	amount, ok := new(big.Rat).SetString(request.Amount)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	if !ok || amount.Sign() <= 0 || request.OrganizationID == "" || request.RequestedBy == "" || request.IdempotencyKey == "" || strings.TrimSpace(request.InvoiceTitle) == "" || len(request.Currency) != 3 || request.PeriodEnd.Before(request.PeriodStart) {
		return domain.InvoiceApplication{}, false, ErrFinanceState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InvoiceApplication{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "invoice-application:"+request.OrganizationID+":"+request.Currency); err != nil {
		return domain.InvoiceApplication{}, false, err
	}
	// Refund applications use the same organization/currency lock. This keeps
	// net invoiceable payment evidence from changing while source items are
	// attributed, without changing any already-posted ledger row.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "refund-invoice:"+request.OrganizationID+":"+request.Currency); err != nil {
		return domain.InvoiceApplication{}, false, err
	}
	existing, e := scanInvoiceApplication(tx.QueryRow(ctx, invoiceApplicationSelect+` WHERE organization_id=$1 AND idempotency_key=$2`, request.OrganizationID, request.IdempotencyKey))
	if e == nil {
		if !decimalEqual(existing.Amount, request.Amount) || existing.Currency != request.Currency ||
			existing.InvoiceTitle != strings.TrimSpace(request.InvoiceTitle) || existing.TaxIdentifier != strings.TrimSpace(request.TaxIdentifier) ||
			existing.PeriodStart != request.PeriodStart.Format("2006-01-02") || existing.PeriodEnd != request.PeriodEnd.Format("2006-01-02") {
			return domain.InvoiceApplication{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(e, ErrNotFound) {
		return domain.InvoiceApplication{}, false, e
	}
	var eligible, claimed string
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT sum(GREATEST(recharge.amount-COALESCE((SELECT sum(refund.amount) FROM refund_order refund WHERE refund.recharge_order_id=recharge.id AND refund.status='SUCCEEDED'),0),0))
		FROM recharge_order recharge WHERE recharge.organization_id=$1 AND recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED') AND recharge.currency=$2 AND (recharge.credited_at AT TIME ZONE 'UTC')::date BETWEEN $3 AND $4),0)
		+COALESCE((SELECT sum(GREATEST(invoice.total_amount-COALESCE((SELECT sum(application.requested_amount) FROM refund_application application
			WHERE application.subscription_invoice_id=invoice.id AND application.status='COMPLETED'),0),0))
			FROM subscription_invoice invoice WHERE invoice.organization_id=$1 AND invoice.status='PAID' AND invoice.currency=$2
			AND (invoice.paid_at AT TIME ZONE 'UTC')::date BETWEEN $3 AND $4),0),
		COALESCE((SELECT sum(item.amount) FROM invoice_application_item item JOIN invoice_application application ON application.id=item.invoice_application_id
			WHERE application.organization_id=$1 AND application.status NOT IN ('REJECTED','CANCELED') AND item.currency=$2
			AND daterange(application.period_start,application.period_end,'[]') && daterange($3::date,$4::date,'[]')),0)`, request.OrganizationID, request.Currency, request.PeriodStart, request.PeriodEnd).Scan(&eligible, &claimed); err != nil {
		return domain.InvoiceApplication{}, false, err
	}
	if new(big.Rat).Add(amount, mustFundingRat(claimed)).Cmp(mustFundingRat(eligible)) > 0 {
		return domain.InvoiceApplication{}, false, ErrInvoiceAmount
	}
	v := domain.InvoiceApplication{ID: id.UUID(), ApplicationNumber: financeNumber("IVA"), OrganizationID: request.OrganizationID, InvoiceTitle: strings.TrimSpace(request.InvoiceTitle), TaxIdentifier: strings.TrimSpace(request.TaxIdentifier), Amount: request.Amount, Currency: request.Currency, PeriodStart: request.PeriodStart.Format("2006-01-02"), PeriodEnd: request.PeriodEnd.Format("2006-01-02"), Status: "SUBMITTED", IdempotencyKey: request.IdempotencyKey, RequestedBy: request.RequestedBy}
	_, err = tx.Exec(ctx, `INSERT INTO invoice_application(id,application_number,organization_id,invoice_title,tax_identifier,amount,currency,period_start,period_end,idempotency_key,requested_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, v.ID, v.ApplicationNumber, v.OrganizationID, v.InvoiceTitle, nullString(v.TaxIdentifier), v.Amount, v.Currency, v.PeriodStart, v.PeriodEnd, v.IdempotencyKey, v.RequestedBy)
	if err != nil {
		return v, false, err
	}
	// Attribute the exact application amount to eligible settled sources. The
	// source rows are locked so concurrent applications cannot claim the same
	// payment even when their date ranges overlap.
	type invoiceSource struct {
		sourceType, sourceID, gross, claimed string
	}
	sourceRows, err := tx.Query(ctx, `SELECT source_type,source_id,gross::text,claimed::text FROM (
		SELECT 'RECHARGE' source_type,recharge.id source_id,
			GREATEST(recharge.amount-COALESCE((SELECT sum(refund.amount) FROM refund_order refund WHERE refund.recharge_order_id=recharge.id AND refund.status='SUCCEEDED'),0),0) gross,
			COALESCE((SELECT sum(item.amount) FROM invoice_application_item item JOIN invoice_application application ON application.id=item.invoice_application_id
				WHERE item.recharge_order_id=recharge.id AND application.status NOT IN ('REJECTED','CANCELED')),0) claimed,
			COALESCE(recharge.credited_at,recharge.created_at) source_at
		FROM recharge_order recharge WHERE recharge.organization_id=$1 AND recharge.currency=$2
			AND recharge.status IN ('CREDITED','REFUND_PENDING','REFUNDED')
			AND (recharge.credited_at AT TIME ZONE 'UTC')::date BETWEEN $3 AND $4
		UNION ALL
		SELECT 'SUBSCRIPTION',invoice.id,GREATEST(invoice.total_amount-COALESCE((SELECT sum(application.requested_amount)
			FROM refund_application application WHERE application.subscription_invoice_id=invoice.id AND application.status='COMPLETED'),0),0),
			COALESCE((SELECT sum(item.amount) FROM invoice_application_item item JOIN invoice_application application ON application.id=item.invoice_application_id
				WHERE item.subscription_invoice_id=invoice.id AND application.status NOT IN ('REJECTED','CANCELED')),0),invoice.paid_at
		FROM subscription_invoice invoice WHERE invoice.organization_id=$1 AND invoice.currency=$2 AND invoice.status='PAID'
			AND (invoice.paid_at AT TIME ZONE 'UTC')::date BETWEEN $3 AND $4
	) source WHERE gross>claimed ORDER BY source_at,source_id`, request.OrganizationID, request.Currency, request.PeriodStart, request.PeriodEnd)
	if err != nil {
		return v, false, err
	}
	sources := make([]invoiceSource, 0)
	for sourceRows.Next() {
		var source invoiceSource
		if err = sourceRows.Scan(&source.sourceType, &source.sourceID, &source.gross, &source.claimed); err != nil {
			sourceRows.Close()
			return v, false, err
		}
		sources = append(sources, source)
	}
	if err = sourceRows.Err(); err != nil {
		sourceRows.Close()
		return v, false, err
	}
	sourceRows.Close()
	remaining := new(big.Rat).Set(amount)
	for _, source := range sources {
		if remaining.Sign() <= 0 {
			break
		}
		available := new(big.Rat).Sub(mustFundingRat(source.gross), mustFundingRat(source.claimed))
		take := new(big.Rat).Set(available)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		if take.Sign() <= 0 {
			continue
		}
		var rechargeID, subscriptionID any
		if source.sourceType == "RECHARGE" {
			rechargeID = source.sourceID
		} else {
			subscriptionID = source.sourceID
		}
		_, err = tx.Exec(ctx, `INSERT INTO invoice_application_item(id,invoice_application_id,source_type,recharge_order_id,subscription_invoice_id,amount,currency)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), v.ID, source.sourceType, rechargeID, subscriptionID, formatRat(take), v.Currency)
		if err != nil {
			return v, false, err
		}
		remaining.Sub(remaining, take)
	}
	if remaining.Sign() > 0 {
		return v, false, ErrInvoiceAmount
	}
	if err = writeAuditTx(ctx, tx, &request.RequestedBy, "finance.invoice_application_submitted", "invoice_application", v.ID, map[string]any{"amount": v.Amount, "currency": v.Currency, "period_start": v.PeriodStart, "period_end": v.PeriodEnd}); err != nil {
		return v, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return v, false, err
	}
	loaded, err := s.InvoiceApplicationByID(ctx, v.ID)
	return loaded, false, err
}

const invoiceApplicationSelect = `SELECT id,application_number,organization_id,invoice_title,COALESCE(tax_identifier,''),amount::text,currency,period_start::text,period_end::text,status,idempotency_key,requested_by,processed_by,COALESCE(processing_reason,''),processed_at,exported_at,invoice_export_batch_id,created_at,updated_at FROM invoice_application`

func scanInvoiceApplication(row pgx.Row) (domain.InvoiceApplication, error) {
	var v domain.InvoiceApplication
	err := row.Scan(&v.ID, &v.ApplicationNumber, &v.OrganizationID, &v.InvoiceTitle, &v.TaxIdentifier, &v.Amount, &v.Currency, &v.PeriodStart, &v.PeriodEnd, &v.Status, &v.IdempotencyKey, &v.RequestedBy, &v.ProcessedBy, &v.ProcessingReason, &v.ProcessedAt, &v.ExportedAt, &v.ExportBatchID, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *Store) InvoiceApplicationByID(ctx context.Context, id string) (domain.InvoiceApplication, error) {
	return scanInvoiceApplication(s.pool.QueryRow(ctx, invoiceApplicationSelect+` WHERE id=$1`, id))
}
func (s *Store) ListInvoiceApplications(ctx context.Context, filter FinanceFilter) ([]domain.InvoiceApplication, error) {
	rows, err := s.pool.Query(ctx, invoiceApplicationSelect+` WHERE ($1='' OR organization_id=$1) AND ($2='' OR status=$2) ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, filter.OrganizationID, strings.ToUpper(filter.Status), clamp(filter.Limit), max(filter.Offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.InvoiceApplication{}
	for rows.Next() {
		v, e := scanInvoiceApplication(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ExportInvoiceApplicationBatch persists the exact formula-neutralized CSV
// artifact and the EXPORTED transition in one transaction. A retry using the
// same batch key and filter returns the exact bytes even after an interrupted
// HTTP response; a changed filter is an idempotency conflict.
func (s *Store) ExportInvoiceApplicationBatch(ctx context.Context, filter FinanceFilter, batchKey, actor string) (InvoiceExportBatch, bool, error) {
	batchKey, actor = strings.TrimSpace(batchKey), strings.TrimSpace(actor)
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	limit := clampFinanceLimit(filter.Limit)
	if batchKey == "" || actor == "" {
		return InvoiceExportBatch{}, false, ErrFinanceState
	}
	requestHash := sha256.Sum256([]byte("organization_id=" + filter.OrganizationID + "\nlimit=" + strconv.Itoa(limit)))
	requestSHA := hex.EncodeToString(requestHash[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return InvoiceExportBatch{}, false, err
	}
	defer tx.Rollback(ctx)
	var existing InvoiceExportBatch
	err = tx.QueryRow(ctx, `SELECT id,batch_key,organization_id,request_sha256,artifact_sha256,artifact,row_count,created_by,created_at
		FROM invoice_export_batch WHERE batch_key=$1 FOR SHARE`, batchKey).Scan(&existing.ID, &existing.BatchKey, &existing.OrganizationID,
		&existing.RequestSHA256, &existing.ArtifactSHA256, &existing.Artifact, &existing.RowCount, &existing.CreatedBy, &existing.CreatedAt)
	if err == nil {
		if existing.RequestSHA256 != requestSHA || existing.CreatedBy != actor {
			return InvoiceExportBatch{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return InvoiceExportBatch{}, false, err
	}
	rows, err := tx.Query(ctx, invoiceApplicationSelect+` WHERE ($1::text='' OR organization_id=$1::uuid) AND status='APPROVED'
		ORDER BY created_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, filter.OrganizationID, limit)
	if err != nil {
		return InvoiceExportBatch{}, false, err
	}
	items := make([]domain.InvoiceApplication, 0)
	ids := make([]string, 0)
	for rows.Next() {
		item, scanErr := scanInvoiceApplication(rows)
		if scanErr != nil {
			rows.Close()
			return InvoiceExportBatch{}, false, scanErr
		}
		items, ids = append(items, item), append(ids, item.ID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return InvoiceExportBatch{}, false, err
	}
	rows.Close()
	now := time.Now().UTC()
	artifact, err := invoiceApplicationsCSV(items, now)
	if err != nil {
		return InvoiceExportBatch{}, false, err
	}
	artifactHash := sha256.Sum256(artifact)
	batch := InvoiceExportBatch{ID: id.UUID(), BatchKey: batchKey, RequestSHA256: requestSHA,
		ArtifactSHA256: hex.EncodeToString(artifactHash[:]), Artifact: artifact, RowCount: len(items), CreatedBy: actor, CreatedAt: now}
	if filter.OrganizationID != "" {
		batch.OrganizationID = &filter.OrganizationID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO invoice_export_batch(id,batch_key,organization_id,request_sha256,artifact_sha256,artifact,row_count,created_by,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, batch.ID, batch.BatchKey, batch.OrganizationID, batch.RequestSHA256,
		batch.ArtifactSHA256, batch.Artifact, batch.RowCount, batch.CreatedBy, batch.CreatedAt); err != nil {
		return InvoiceExportBatch{}, false, err
	}
	if len(ids) > 0 {
		tag, updateErr := tx.Exec(ctx, `UPDATE invoice_application SET status='EXPORTED',exported_at=$2,invoice_export_batch_id=$3,updated_at=$2
			WHERE id=ANY($1::uuid[]) AND status='APPROVED'`, ids, now, batch.ID)
		if updateErr != nil {
			return InvoiceExportBatch{}, false, updateErr
		}
		if tag.RowsAffected() != int64(len(ids)) {
			return InvoiceExportBatch{}, false, ErrFinanceState
		}
	}
	auditAction := "finance.invoice_export_batch_created"
	if len(ids) > 0 {
		auditAction = "finance.invoice_applications_exported"
	}
	if err = writeAuditTx(ctx, tx, &actor, auditAction, "invoice_export_batch", batch.ID,
		map[string]any{"application_ids": ids, "batch_key": batch.BatchKey, "artifact_sha256": batch.ArtifactSHA256, "row_count": batch.RowCount}); err != nil {
		return InvoiceExportBatch{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return InvoiceExportBatch{}, false, err
	}
	return batch, false, nil
}

// ExportInvoiceApplications preserves the schema-12 store contract for
// internal callers. New HTTP callers use ExportInvoiceApplicationBatch so a
// lost response can be downloaded again byte-for-byte.
func (s *Store) ExportInvoiceApplications(ctx context.Context, filter FinanceFilter, actor string) ([]domain.InvoiceApplication, error) {
	batch, _, err := s.ExportInvoiceApplicationBatch(ctx, filter, "legacy:"+id.UUID(), actor)
	if err != nil || batch.RowCount == 0 {
		return []domain.InvoiceApplication{}, err
	}
	rows, err := s.pool.Query(ctx, invoiceApplicationSelect+` WHERE invoice_export_batch_id=$1 ORDER BY created_at,id`, batch.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.InvoiceApplication, 0, batch.RowCount)
	for rows.Next() {
		item, scanErr := scanInvoiceApplication(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func invoiceApplicationsCSV(items []domain.InvoiceApplication, exportedAt time.Time) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	header := []string{"application_number", "organization_id", "invoice_title", "tax_identifier", "amount", "currency",
		"period_start", "period_end", "status", "requested_by", "processed_by", "processing_reason", "processed_at", "exported_at", "created_at"}
	write := func(record []string) error {
		for index := range record {
			record[index] = CSVSafeCell(record[index])
		}
		return writer.Write(record)
	}
	if err := write(header); err != nil {
		return nil, err
	}
	for _, item := range items {
		if err := write([]string{item.ApplicationNumber, item.OrganizationID, item.InvoiceTitle, item.TaxIdentifier, item.Amount, item.Currency,
			item.PeriodStart, item.PeriodEnd, "EXPORTED", item.RequestedBy, derefFinanceID(item.ProcessedBy), item.ProcessingReason,
			financeStoreTime(item.ProcessedAt), exportedAt.Format(time.RFC3339Nano), item.CreatedAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func financeStoreTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *Store) DecideInvoiceApplication(ctx context.Context, id, decision, reason, idempotencyKey, actor string) (domain.InvoiceApplication, bool, error) {
	decision = strings.ToUpper(strings.TrimSpace(decision))
	reason = strings.TrimSpace(reason)
	if decision != "APPROVE" && decision != "REJECT" || reason == "" || idempotencyKey == "" || actor == "" {
		return domain.InvoiceApplication{}, false, ErrFinanceState
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.InvoiceApplication{}, false, err
	}
	defer tx.Rollback(ctx)
	v, err := scanInvoiceApplication(tx.QueryRow(ctx, invoiceApplicationSelect+` WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return v, false, err
	}
	if v.Status == "APPROVED" || v.Status == "REJECTED" {
		expectedStatus := "REJECTED"
		if decision == "APPROVE" {
			expectedStatus = "APPROVED"
		}
		if v.Status != expectedStatus || v.ProcessingReason != reason || derefFinanceID(v.ProcessedBy) != actor {
			return v, false, ErrIdempotencyConflict
		}
		return v, true, tx.Commit(ctx)
	}
	if v.Status != "SUBMITTED" && v.Status != "UNDER_REVIEW" {
		return v, false, ErrFinanceState
	}
	status := "REJECTED"
	if decision == "APPROVE" {
		status = "APPROVED"
	}
	_, err = tx.Exec(ctx, `UPDATE invoice_application SET status=$2,processed_by=$3,processing_reason=$4,processed_at=now(),updated_at=now() WHERE id=$1`, id, status, actor, reason)
	if err != nil {
		return v, false, err
	}
	if err = writeAuditTx(ctx, tx, &actor, "finance.invoice_application_"+strings.ToLower(status), "invoice_application", id, map[string]any{"reason": reason, "idempotency_key": idempotencyKey}); err != nil {
		return v, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return v, false, err
	}
	v, err = s.InvoiceApplicationByID(ctx, id)
	return v, false, err
}
func (s *Store) MarkInvoiceApplicationsExported(ctx context.Context, ids []string, actor string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE invoice_application SET status='EXPORTED',exported_at=now(),updated_at=now()
		WHERE id=ANY($1::uuid[]) AND status='APPROVED'`, ids)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(ids)) {
		return ErrFinanceState
	}
	if err = writeAuditTx(ctx, tx, &actor, "finance.invoice_applications_exported", "invoice_application", "", map[string]any{"application_ids": ids}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListRechargeOrdersFinance(ctx context.Context, filter FinanceFilter, anomalies bool) ([]domain.RechargeOrder, error) {
	query := rechargeOrderSelect + ` WHERE ($1='' OR organization_id=$1) AND ($2='' OR status=$2) AND ($3='' OR platform_order_no ILIKE '%'||$3||'%' OR COALESCE(provider_order_no,'') ILIKE '%'||$3||'%')`
	if anomalies {
		query += ` AND (status IN ('FAILED','EXPIRED','CHARGEBACK') OR (status='PAID' AND updated_at<now()-interval '15 minutes') OR (status='CREDITED' AND (wallet_transaction_id IS NULL OR ledger_journal_id IS NULL)))`
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`
	rows, err := s.pool.Query(ctx, query, filter.OrganizationID, strings.ToUpper(filter.Status), filter.Query, clamp(filter.Limit), max(filter.Offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.RechargeOrder{}
	for rows.Next() {
		v, e := scanRechargeOrder(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ListAccountingRows(ctx context.Context, filter FinanceFilter) ([]AccountingRow, error) {
	requestedLimit := clampFinanceLimit(filter.Limit)
	rows, err := s.pool.Query(ctx, `SELECT journal.posted_at,journal.id,journal.external_key,journal.journal_type,account.account_key,account.name,
	CASE WHEN entry.entry_side='DEBIT' THEN entry.amount ELSE 0 END::text,CASE WHEN entry.entry_side='CREDIT' THEN entry.amount ELSE 0 END::text,
	entry.currency,COALESCE(journal.reference,''),COALESCE(journal.reconciliation_case_id::text,'') FROM ledger_journal journal JOIN ledger_journal_entry entry ON entry.journal_id=journal.id JOIN ledger_account account ON account.id=entry.account_id
	LEFT JOIN wallets wallet ON wallet.id=journal.wallet_id
	LEFT JOIN subscription_invoice subscription ON subscription.id=journal.subscription_invoice_id
	WHERE journal.status='POSTED' AND ($1::text='' OR COALESCE(wallet.organization_id,subscription.organization_id)=$1::uuid)
	AND ($2::timestamptz IS NULL OR journal.posted_at>=$2) AND ($3::timestamptz IS NULL OR journal.posted_at<$3)
	ORDER BY journal.posted_at,journal.id,entry.id LIMIT $4 OFFSET $5`, filter.OrganizationID, nullableFinanceTime(filter.From), nullableFinanceTime(filter.To), requestedLimit+1, max(filter.Offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountingRow{}
	for rows.Next() {
		var v AccountingRow
		if err = rows.Scan(&v.PostedAt, &v.JournalID, &v.ExternalKey, &v.JournalType, &v.AccountKey, &v.AccountName, &v.Debit, &v.Credit, &v.Currency, &v.Reference, &v.CaseID); err != nil {
			return nil, err
		}
		out = append(out, v)
		if len(out) > requestedLimit && filter.RejectTruncated {
			return nil, ErrFinanceExportLimit
		}
	}
	if len(out) > requestedLimit {
		out = out[:requestedLimit]
	}
	return out, rows.Err()
}

func (s *Store) FinanceReport(ctx context.Context, report string, from, to time.Time) ([]map[string]any, error) {
	switch report {
	case "provider_cost":
		// Statement headers are reconciliation controls. Reports recognize the
		// actual line amount on its usage date so a statement spanning several
		// months is never counted in full in every overlapping period.
		query := `SELECT line.usage_date::text,line.currency,provider.name,sum(line.amount)::text,count(*)
			FROM provider_statement_line line
			JOIN provider_statement statement ON statement.id=line.provider_statement_id
			JOIN providers provider ON provider.id=statement.provider_id
			WHERE ($1::timestamptz IS NULL OR line.usage_date>=($1 AT TIME ZONE 'UTC')::date)
			AND ($2::timestamptz IS NULL OR line.usage_date<($2 AT TIME ZONE 'UTC')::date)
			GROUP BY line.usage_date,line.currency,provider.name
			ORDER BY line.usage_date DESC,line.currency,provider.name`
		rows, err := s.pool.Query(ctx, query, nullableFinanceTime(from), nullableFinanceTime(to))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var date, currency, provider, amount string
			var count int64
			if err = rows.Scan(&date, &currency, &provider, &amount, &count); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"date": date, "currency": currency, "provider": provider, "amount": amount, "requests": count, "report": report, "source": "provider_statement"})
		}
		return out, rows.Err()
	case "user_revenue":
		// Posted income accounts, including their debit corrections, are the
		// accounting authority. This includes usage and subscription revenue and
		// naturally reflects reversals instead of trusting mutable report status.
		query := `SELECT (journal.posted_at AT TIME ZONE 'UTC')::date::text,entry.currency,
			CASE WHEN account.account_key LIKE 'system:subscription-revenue:%' THEN 'Subscription'
				ELSE 'Usage' END source,
			sum(CASE WHEN entry.entry_side='CREDIT' THEN entry.amount ELSE -entry.amount END)::text,
			count(DISTINCT journal.id)
			FROM ledger_journal journal
			JOIN ledger_journal_entry entry ON entry.journal_id=journal.id
			JOIN ledger_account account ON account.id=entry.account_id
			WHERE journal.status='POSTED' AND account.account_key IN
				('system:revenue:'||entry.currency,'system:subscription-revenue:'||entry.currency)
			AND ($1::timestamptz IS NULL OR journal.posted_at>=$1)
			AND ($2::timestamptz IS NULL OR journal.posted_at<$2)
			GROUP BY 1,entry.currency,source ORDER BY 1 DESC,entry.currency,source`
		rows, err := s.pool.Query(ctx, query, nullableFinanceTime(from), nullableFinanceTime(to))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var date, currency, source, amount string
			var count int64
			if err = rows.Scan(&date, &currency, &source, &amount, &count); err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"date": date, "currency": currency, "provider": source,
				"amount": amount, "requests": count, "report": report, "source": "posted_ledger"})
		}
		return out, rows.Err()
	case "gross_margin":
		// Gross margin is reported in the customer currency captured by the
		// immutable usage price snapshot. A Provider statement line contributes
		// cost only when it resolves to exactly one usage record and that usage has
		// coherent pricing evidence. Costs without unique conversion evidence are
		// returned as explicit unallocated_cost rows and are never presented as
		// gross margin.
		query := `WITH revenue AS (
			SELECT (journal.posted_at AT TIME ZONE 'UTC')::date report_day,entry.currency,
				CASE WHEN account.account_key LIKE 'system:subscription-revenue:%' THEN NULL::uuid
					ELSE usage.provider_id END provider_id,
				CASE WHEN account.account_key LIKE 'system:subscription-revenue:%' THEN 'Subscription'
					ELSE COALESCE(provider.name,'Unassigned') END dimension,
				sum(CASE WHEN entry.entry_side='CREDIT' THEN entry.amount ELSE -entry.amount END) amount,
				count(DISTINCT journal.id) requests
			FROM ledger_journal journal
			JOIN ledger_journal_entry entry ON entry.journal_id=journal.id
			JOIN ledger_account account ON account.id=entry.account_id
			LEFT JOIN wallet_transactions transaction ON transaction.journal_id=journal.id
			LEFT JOIN billing_usage_records usage ON usage.funding_operation_id=transaction.funding_operation_id
			LEFT JOIN providers provider ON provider.id=usage.provider_id
			WHERE journal.status='POSTED' AND account.account_key IN
				('system:revenue:'||entry.currency,'system:subscription-revenue:'||entry.currency)
			AND ($1::timestamptz IS NULL OR journal.posted_at>=$1)
			AND ($2::timestamptz IS NULL OR journal.posted_at<$2)
			GROUP BY 1,entry.currency,
				CASE WHEN account.account_key LIKE 'system:subscription-revenue:%' THEN NULL::uuid ELSE usage.provider_id END,
				CASE WHEN account.account_key LIKE 'system:subscription-revenue:%' THEN 'Subscription'
					ELSE COALESCE(provider.name,'Unassigned') END
		), statement_cost_evidence AS (
			SELECT line.id,line.usage_date report_day,statement.provider_id,provider.name dimension,
				line.amount provider_amount,line.currency provider_currency,
				count(matched.usage_id) usage_match_count,count(matched.snapshot_id) snapshot_match_count,
				max(matched.snapshot_provider_id) snapshot_provider_id,
				max(matched.snapshot_provider_currency) snapshot_provider_currency,
				max(matched.customer_currency) customer_currency,max(matched.exchange_rate) exchange_rate
			FROM provider_statement_line line
			JOIN provider_statement statement ON statement.id=line.provider_statement_id
			JOIN providers provider ON provider.id=statement.provider_id
			LEFT JOIN LATERAL (
				SELECT usage.id::text usage_id,snapshot.id::text snapshot_id,
					snapshot.provider_id::text snapshot_provider_id,
					snapshot.provider_currency snapshot_provider_currency,
					snapshot.customer_currency,snapshot.exchange_rate
				FROM billing_usage_records usage
				LEFT JOIN usage_price_snapshot snapshot ON snapshot.id=usage.usage_price_snapshot_id
					AND snapshot.request_id=usage.request_id
				WHERE usage.provider_id=statement.provider_id AND (
					(line.request_id IS NOT NULL AND usage.request_id=line.request_id)
					OR (line.upstream_request_id IS NOT NULL AND EXISTS (
						SELECT 1 FROM funding_provider_attempt attempt
						WHERE attempt.operation_id=usage.funding_operation_id
							AND attempt.status='SUCCEEDED'
							AND attempt.upstream_request_id=line.upstream_request_id
					))
				)
			) matched ON true
			WHERE ($1::timestamptz IS NULL OR line.usage_date>=($1 AT TIME ZONE 'UTC')::date)
				AND ($2::timestamptz IS NULL OR line.usage_date<($2 AT TIME ZONE 'UTC')::date)
			GROUP BY line.id,line.usage_date,statement.provider_id,provider.name,line.amount,line.currency
		), classified_cost AS (
			SELECT evidence.*,
				CASE WHEN usage_match_count=0 THEN 'USAGE_NOT_FOUND'
					WHEN usage_match_count>1 THEN 'USAGE_MATCH_AMBIGUOUS'
					WHEN snapshot_match_count<>1 THEN 'PRICE_SNAPSHOT_MISSING'
					WHEN snapshot_provider_id<>provider_id::text THEN 'SNAPSHOT_PROVIDER_MISMATCH'
					WHEN snapshot_provider_currency<>provider_currency THEN 'PROVIDER_CURRENCY_MISMATCH'
					WHEN customer_currency IS NULL THEN 'CUSTOMER_CURRENCY_MISSING'
					WHEN exchange_rate IS NULL OR exchange_rate<=0 THEN 'EXCHANGE_RATE_MISSING'
					ELSE NULL END unallocated_reason
			FROM statement_cost_evidence evidence
		), actual_cost AS (
			SELECT report_day,customer_currency currency,provider_id,dimension,
				sum(provider_amount*exchange_rate) amount
			FROM classified_cost WHERE unallocated_reason IS NULL
			GROUP BY report_day,customer_currency,provider_id,dimension
		), margin_rows AS (
			SELECT COALESCE(revenue.report_day,actual_cost.report_day) report_day,
				COALESCE(revenue.currency,actual_cost.currency) currency,
				COALESCE(revenue.dimension,actual_cost.dimension,'Unassigned') dimension,
				COALESCE(revenue.amount,0)-COALESCE(actual_cost.amount,0) amount,
				COALESCE(revenue.requests,0) requests
			FROM revenue FULL OUTER JOIN actual_cost
				ON actual_cost.report_day=revenue.report_day AND actual_cost.currency=revenue.currency
				AND actual_cost.provider_id IS NOT DISTINCT FROM revenue.provider_id
		), unallocated_cost AS (
			SELECT report_day,provider_currency currency,dimension,sum(provider_amount) amount,
				count(*) requests,unallocated_reason
			FROM classified_cost WHERE unallocated_reason IS NOT NULL
			GROUP BY report_day,provider_currency,dimension,unallocated_reason
		)
		SELECT report_day::text,currency,dimension,amount::text,requests,
			'gross_margin'::text row_type,NULL::text unallocated_reason FROM margin_rows
		UNION ALL
		SELECT report_day::text,currency,dimension,amount::text,requests,
			'unallocated_cost'::text row_type,unallocated_reason FROM unallocated_cost
		ORDER BY 1 DESC,2,3,6,7 NULLS FIRST`
		rows, err := s.pool.Query(ctx, query, nullableFinanceTime(from), nullableFinanceTime(to))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var date, currency, provider, amount, rowType string
			var unallocatedReason *string
			var count int64
			if err = rows.Scan(&date, &currency, &provider, &amount, &count, &rowType, &unallocatedReason); err != nil {
				return nil, err
			}
			var reason any
			if unallocatedReason != nil {
				reason = *unallocatedReason
			}
			out = append(out, map[string]any{"date": date, "currency": currency, "provider": provider, "amount": amount,
				"requests": count, "report": report, "row_type": rowType, "unallocated_reason": reason,
				"cost_source": "provider_statement", "cost_conversion_source": "usage_price_snapshot"})
		}
		return out, rows.Err()
	default:
		return nil, errors.New("invalid finance report")
	}
}

func (s *Store) ImportProviderStatement(ctx context.Context, input ProviderStatementInput) (string, bool, error) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.StatementReference = strings.TrimSpace(input.StatementReference)
	input.Region = strings.ToUpper(strings.TrimSpace(input.Region))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.TotalAmount = strings.TrimSpace(input.TotalAmount)
	input.SourceSHA256 = strings.ToLower(strings.TrimSpace(input.SourceSHA256))
	if input.ProviderID == "" || input.StatementReference == "" || len(input.Region) != 2 || len(input.Currency) != 3 || len(input.SourceSHA256) != 64 || input.ImportedBy == "" || input.PeriodEnd.Before(input.PeriodStart) {
		return "", false, ErrFinanceState
	}
	total := new(big.Rat)
	for _, line := range input.Lines {
		v, ok := new(big.Rat).SetString(line.Amount)
		if !ok || v.Sign() < 0 || strings.ToUpper(line.Currency) != input.Currency || line.ExternalLineID == "" || line.RequestID == "" && line.UpstreamRequestID == "" {
			return "", false, ErrFinanceState
		}
		total.Add(total, v)
	}
	if !decimalEqual(formatRat(total), input.TotalAmount) {
		return "", false, ErrStatementMismatch
	}
	fingerprintPayload := struct {
		ProviderID         string                       `json:"provider_id"`
		StatementReference string                       `json:"statement_reference"`
		PeriodStart        string                       `json:"period_start"`
		PeriodEnd          string                       `json:"period_end"`
		Region             string                       `json:"region"`
		Currency           string                       `json:"currency"`
		TotalAmount        string                       `json:"total_amount"`
		SourceSHA256       string                       `json:"source_sha256"`
		Lines              []ProviderStatementLineInput `json:"lines"`
	}{input.ProviderID, input.StatementReference, input.PeriodStart.UTC().Format("2006-01-02"), input.PeriodEnd.UTC().Format("2006-01-02"),
		input.Region, input.Currency, formatRat(total), input.SourceSHA256, input.Lines}
	fingerprintBytes, err := json.Marshal(fingerprintPayload)
	if err != nil {
		return "", false, err
	}
	fingerprintHash := sha256.Sum256(fingerprintBytes)
	fingerprint := hex.EncodeToString(fingerprintHash[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var existing, existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,COALESCE(import_fingerprint_sha256,'') FROM provider_statement
		WHERE provider_id=$1 AND (statement_reference=$2 OR source_sha256=$3) FOR SHARE`, input.ProviderID, input.StatementReference, input.SourceSHA256).
		Scan(&existing, &existingFingerprint)
	if err == nil {
		if existingFingerprint == "" || existingFingerprint != fingerprint {
			return "", false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	statementID := id.UUID()
	var providerEnabled bool
	var contractStatus string
	var allowedRegions []byte
	if err = tx.QueryRow(ctx, `SELECT enabled,contract_status,allowed_regions FROM providers WHERE id=$1 FOR SHARE`, input.ProviderID).
		Scan(&providerEnabled, &contractStatus, &allowedRegions); errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	} else if err != nil {
		return "", false, err
	}
	organizationRegionAllowed := true
	for _, line := range input.Lines {
		if line.RequestID == "" && line.UpstreamRequestID == "" {
			continue
		}
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(
			SELECT 1 FROM billing_usage_records usage
			JOIN organizations organization ON organization.id=usage.organization_id
			LEFT JOIN funding_provider_attempt attempt ON attempt.operation_id=usage.funding_operation_id
			WHERE (($1::text<>'' AND usage.request_id=$1::text) OR ($2::text<>'' AND attempt.upstream_request_id=$2::text))
			AND organization.billing_region NOT IN ('*',$3)
		)`, line.RequestID, line.UpstreamRequestID, input.Region).Scan(&allowed); err != nil {
			return "", false, err
		}
		if !allowed {
			organizationRegionAllowed = false
			break
		}
	}
	if !providerEnabled || contractStatus != "ACTIVE" || !regionAllowed(allowedRegions, input.Region) || !organizationRegionAllowed {
		return "", false, ErrProviderNotContracted
	}
	_, err = tx.Exec(ctx, `INSERT INTO provider_statement(id,provider_id,statement_reference,period_start,period_end,region,currency,total_amount,source_sha256,import_fingerprint_sha256,imported_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, statementID, input.ProviderID, input.StatementReference, input.PeriodStart,
		input.PeriodEnd, input.Region, input.Currency, input.TotalAmount, input.SourceSHA256, fingerprint, input.ImportedBy)
	if err != nil {
		return "", false, err
	}
	for _, line := range input.Lines {
		_, err = tx.Exec(ctx, `INSERT INTO provider_statement_line(id,provider_statement_id,external_line_id,request_id,upstream_request_id,usage_date,input_tokens,cached_input_tokens,output_tokens,amount,currency,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id.UUID(), statementID, line.ExternalLineID, nullString(line.RequestID), nullString(line.UpstreamRequestID), line.UsageDate, line.InputTokens, line.CachedInputTokens, line.OutputTokens, line.Amount, input.Currency, jsonBytes(line.Metadata))
		if err != nil {
			return "", false, err
		}
	}
	if decimalPositive(input.TotalAmount) {
		if _, err = tx.Exec(ctx, `INSERT INTO ledger_account(account_key,name,account_type,normal_side,currency) VALUES
			($1,'Provider expense','EXPENSE','DEBIT',$3),($2,'Provider payable','LIABILITY','CREDIT',$3)
			ON CONFLICT(account_key) DO NOTHING`, systemAccountKey("provider-expense", input.Currency),
			systemAccountKey("provider-payable", input.Currency), input.Currency); err != nil {
			return "", false, err
		}
		journalID := id.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO ledger_journal(id,journal_type,external_key,currency,reference,metadata,created_by,provider_statement_id)
			VALUES($1,'PROVIDER_STATEMENT',$2,$3,$4,$5,$6,$7)`, journalID, "provider-statement:"+statementID, input.Currency,
			input.StatementReference, jsonBytes(map[string]any{"provider_id": input.ProviderID, "source_sha256": input.SourceSHA256}), input.ImportedBy, statementID)
		if err != nil {
			return "", false, err
		}
		for _, line := range []journalLine{
			{systemAccountKey("provider-expense", input.Currency), "DEBIT", input.TotalAmount, "Recognize Provider statement expense"},
			{systemAccountKey("provider-payable", input.Currency), "CREDIT", input.TotalAmount, "Recognize Provider statement payable"},
		} {
			var accountID string
			if err = tx.QueryRow(ctx, `SELECT id FROM ledger_account WHERE account_key=$1 AND currency=$2 AND status='ACTIVE'`, line.accountKey, input.Currency).Scan(&accountID); err != nil {
				return "", false, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO ledger_journal_entry(id,journal_id,account_id,currency,entry_side,amount,description)
				VALUES($1,$2,$3,$4,$5,$6,$7)`, id.UUID(), journalID, accountID, input.Currency, line.side, line.amount, line.description); err != nil {
				return "", false, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE ledger_journal SET status='POSTED',posted_at=now() WHERE id=$1`, journalID); err != nil {
			return "", false, err
		}
	}
	if err = writeAuditTx(ctx, tx, &input.ImportedBy, "finance.provider_statement_imported", "provider_statement", statementID, map[string]any{"provider_id": input.ProviderID, "statement_reference": input.StatementReference, "total_amount": input.TotalAmount, "currency": input.Currency, "source_sha256": input.SourceSHA256, "line_count": len(input.Lines)}); err != nil {
		return "", false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", false, err
	}
	return statementID, false, nil
}

func financeNumber(prefix string) string {
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("20060102"), strings.ToUpper(strings.ReplaceAll(id.UUID()[:12], "-", "")))
}
func derefFinanceID(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func nullableFinanceTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}
func clampFinanceLimit(value int) int {
	return clampFinanceLimitTo(value, 100000)
}
func clampFinanceLimitTo(value, maximum int) int {
	if maximum <= 0 || maximum > 100000 {
		maximum = 100000
	}
	if value <= 0 {
		if maximum < 1000 {
			return maximum
		}
		return 1000
	}
	if value > maximum {
		return maximum
	}
	return value
}
func parseFinanceMonth(month string) (time.Time, time.Time, error) {
	if month == "" {
		now := time.Now().UTC()
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		return from, from.AddDate(0, 1, 0), nil
	}
	from, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return from, from.AddDate(0, 1, 0), nil
}
func financeJSON(raw []byte) map[string]any {
	v := map[string]any{}
	_ = json.Unmarshal(raw, &v)
	return v
}
func financeInt(value string) int { n, _ := strconv.Atoi(value); return n }
