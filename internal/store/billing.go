package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var (
	ErrWalletUnavailable   = errors.New("wallet is frozen or has insufficient prepaid balance")
	ErrIdempotencyConflict = errors.New("idempotency key was already used with different operation data")
)

func scanWallet(row pgx.Row) (domain.Wallet, error) {
	var wallet domain.Wallet
	var available, reserved, credit, riskLimit, riskExposure string
	err := row.Scan(&wallet.ID, &wallet.OrganizationID, &wallet.OrganizationName, &wallet.Currency, &wallet.BillingMode,
		&available, &reserved, &credit, &riskLimit, &riskExposure, &wallet.CreditEnforced, &wallet.Status, &wallet.Version,
		&wallet.CreatedAt, &wallet.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet, ErrNotFound
	}
	if err != nil {
		return wallet, err
	}
	values := []*domain.Decimal{&wallet.AvailableBalance, &wallet.ReservedBalance, &wallet.CreditLimit, &wallet.RiskLimit, &wallet.RiskExposure}
	for index, raw := range []string{available, reserved, credit, riskLimit, riskExposure} {
		parsed, parseErr := domain.ParseDecimal(raw)
		if parseErr != nil {
			return wallet, parseErr
		}
		*values[index] = parsed
	}
	return wallet, nil
}

const walletColumns = `w.id,w.organization_id,o.name,w.currency,w.billing_mode,w.available_balance::text,
	w.reserved_balance::text,w.credit_limit::text,w.risk_limit::text,w.risk_exposure::text,w.credit_enforced,w.status,w.version,w.created_at,w.updated_at`

func (s *Store) ListWallets(ctx context.Context) ([]domain.Wallet, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+walletColumns+` FROM wallets w JOIN organizations o ON o.id=w.organization_id ORDER BY o.name,w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Wallet
	for rows.Next() {
		wallet, err := scanWallet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wallet)
	}
	return out, rows.Err()
}

func (s *Store) WalletByOrganization(ctx context.Context, organizationID string) (domain.Wallet, error) {
	return scanWallet(s.pool.QueryRow(ctx, `SELECT `+walletColumns+` FROM wallets w JOIN organizations o ON o.id=w.organization_id WHERE w.organization_id=$1`, organizationID))
}

func (s *Store) WalletByID(ctx context.Context, walletID string) (domain.Wallet, error) {
	return scanWallet(s.pool.QueryRow(ctx, `SELECT `+walletColumns+` FROM wallets w JOIN organizations o ON o.id=w.organization_id WHERE w.id=$1`, walletID))
}

// WalletAllowsRequest is deliberately fail-closed for PREPAID wallets and
// frozen accounts. POSTPAID is the migration default, preserving V2 traffic.
func (s *Store) WalletAllowsRequest(ctx context.Context, organizationID string) (bool, error) {
	wallet, err := s.WalletByOrganization(ctx, organizationID)
	if err != nil {
		return false, err
	}
	if wallet.Status != "ACTIVE" {
		return false, nil
	}
	if wallet.BillingMode != "PREPAID" {
		return true, nil
	}
	available, err := wallet.AvailableBalance.Add(wallet.CreditLimit)
	if err != nil {
		return false, err
	}
	positive, err := available.IsPositive()
	return positive, err
}

func (s *Store) UpdateWallet(ctx context.Context, wallet domain.Wallet) (domain.Wallet, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE wallets SET billing_mode=$2,credit_limit=$3,risk_limit=$4,credit_enforced=$5,status=$6,updated_at=now(),version=version+1 WHERE id=$1`,
		wallet.ID, wallet.BillingMode, wallet.CreditLimit.String(), wallet.RiskLimit.String(), wallet.CreditEnforced, wallet.Status)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.Wallet{}, ErrNotFound
	}
	if err != nil {
		return domain.Wallet{}, err
	}
	return scanWallet(s.pool.QueryRow(ctx, `SELECT `+walletColumns+` FROM wallets w JOIN organizations o ON o.id=w.organization_id WHERE w.id=$1`, wallet.ID))
}

func (s *Store) CreateWalletTransaction(ctx context.Context, transaction domain.WalletTransaction) (domain.WalletTransaction, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.WalletTransaction{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM wallets WHERE id=$1 FOR UPDATE`, transaction.WalletID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return domain.WalletTransaction{}, ErrNotFound
	} else if err != nil {
		return domain.WalletTransaction{}, err
	}
	if status != "ACTIVE" {
		return domain.WalletTransaction{}, ErrWalletUnavailable
	}
	if transaction.IdempotencyKey != "" {
		existing, err := scanWalletTransaction(tx.QueryRow(ctx, `SELECT id,wallet_id,usage_record_id,transaction_type,
			amount::text,balance_after::text,COALESCE(idempotency_key,''),COALESCE(reference,''),metadata,created_by,created_at
			FROM wallet_transactions WHERE wallet_id=$1 AND idempotency_key=$2`, transaction.WalletID, transaction.IdempotencyKey))
		if err == nil {
			if existing.TransactionType != transaction.TransactionType ||
				!decimalEqual(existing.Amount.String(), transaction.Amount.String()) ||
				existing.Reference != transaction.Reference {
				return domain.WalletTransaction{}, ErrIdempotencyConflict
			}
			return existing, tx.Commit(ctx)
		}
		if !errors.Is(err, ErrNotFound) {
			return domain.WalletTransaction{}, err
		}
	}
	transaction.ID = id.UUID()
	if transaction.Metadata == nil {
		transaction.Metadata = map[string]any{}
	}
	var balanceAfter string
	err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance+$2::numeric,version=version+1,updated_at=now()
		WHERE id=$1 AND ($2::numeric>=0 OR available_balance+$2::numeric+credit_limit>=0) RETURNING available_balance::text`, transaction.WalletID, transaction.Amount.String()).Scan(&balanceAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.WalletTransaction{}, ErrWalletUnavailable
	}
	if err != nil {
		return domain.WalletTransaction{}, err
	}
	transaction.BalanceAfter, err = domain.ParseDecimal(balanceAfter)
	if err != nil {
		return domain.WalletTransaction{}, err
	}
	journalType := transaction.TransactionType
	if journalType != "TOPUP" {
		journalType = "ADJUSTMENT"
	}
	amount := mustFundingRat(transaction.Amount.String())
	absAmount := new(big.Rat).Abs(new(big.Rat).Set(amount))
	counterpartKind := "adjustment-equity"
	if transaction.TransactionType == "TOPUP" {
		counterpartKind = "cash"
	}
	var currency string
	if err = tx.QueryRow(ctx, `SELECT currency FROM wallets WHERE id=$1`, transaction.WalletID).Scan(&currency); err != nil {
		return domain.WalletTransaction{}, err
	}
	lines := []journalLine{}
	if amount.Sign() > 0 {
		lines = append(lines,
			journalLine{systemAccountKey(counterpartKind, currency), "DEBIT", formatRat(absAmount), "Wallet funding counterpart"},
			journalLine{walletAccountKey(transaction.WalletID, "available"), "CREDIT", formatRat(absAmount), "Increase wallet balance"})
	} else {
		lines = append(lines,
			journalLine{walletAccountKey(transaction.WalletID, "available"), "DEBIT", formatRat(absAmount), "Decrease wallet balance"},
			journalLine{systemAccountKey(counterpartKind, currency), "CREDIT", formatRat(absAmount), "Wallet adjustment counterpart"})
	}
	journalID, err := postJournalTx(ctx, tx, transaction.WalletID, journalType,
		"wallet-transaction:"+transaction.WalletID+":"+transaction.IdempotencyKey, currency, transaction.Reference,
		map[string]any{"transaction_type": transaction.TransactionType, "idempotency_key": transaction.IdempotencyKey}, transaction.CreatedBy, lines)
	if err != nil {
		return domain.WalletTransaction{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO wallet_transactions(id,wallet_id,usage_record_id,transaction_type,amount,
		balance_after,idempotency_key,reference,metadata,created_by,journal_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING created_at`, transaction.ID, transaction.WalletID, transaction.UsageRecordID, transaction.TransactionType,
		transaction.Amount.String(), transaction.BalanceAfter.String(), nullString(transaction.IdempotencyKey), nullString(transaction.Reference),
		jsonBytes(transaction.Metadata), transaction.CreatedBy, journalID).Scan(&transaction.CreatedAt)
	if err != nil {
		return domain.WalletTransaction{}, err
	}
	amountPositive := amount.Sign() > 0
	amountNegative := amount.Sign() < 0
	if amountPositive {
		sourceKind := "ADJUSTMENT"
		if transaction.TransactionType == "TOPUP" {
			sourceKind = "TOPUP"
		}
		if err = createCashLotTx(ctx, tx, transaction.WalletID, "", transaction.ID, sourceKind,
			"wallet-transaction:"+transaction.ID, transaction.Amount.String(), currency, false); err != nil {
			return domain.WalletTransaction{}, err
		}
	} else if amountNegative {
		if err = allocateCashDebitTx(ctx, tx, transaction.WalletID, transaction.ID, formatRat(absAmount)); err != nil {
			return domain.WalletTransaction{}, err
		}
	}
	if err = writeAuditTx(ctx, tx, transaction.CreatedBy, "wallet.transaction.committed", "wallet", transaction.WalletID, transaction); err != nil {
		return domain.WalletTransaction{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WalletTransaction{}, err
	}
	return transaction, nil
}

func writeAuditTx(ctx context.Context, tx pgx.Tx, actor *string, action, resourceType, resourceID string, after any) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,action,resource_type,resource_id,after_state) VALUES($1,$2,$3,$4,$5,$6)`, id.UUID(), actor, action, resourceType, nullString(resourceID), jsonBytes(after))
	return err
}

func scanWalletTransaction(row pgx.Row) (domain.WalletTransaction, error) {
	var transaction domain.WalletTransaction
	var metadata []byte
	var amount, balanceAfter string
	err := row.Scan(&transaction.ID, &transaction.WalletID, &transaction.UsageRecordID, &transaction.TransactionType,
		&amount, &balanceAfter, &transaction.IdempotencyKey, &transaction.Reference, &metadata,
		&transaction.CreatedBy, &transaction.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return transaction, ErrNotFound
	}
	if err != nil {
		return transaction, err
	}
	transaction.Amount, err = domain.ParseDecimal(amount)
	if err != nil {
		return transaction, err
	}
	transaction.BalanceAfter, err = domain.ParseDecimal(balanceAfter)
	if err != nil {
		return transaction, err
	}
	_ = json.Unmarshal(metadata, &transaction.Metadata)
	return transaction, nil
}

func (s *Store) ListWalletTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.WalletTransaction, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,wallet_id,usage_record_id,transaction_type,amount::text,balance_after::text,
		COALESCE(idempotency_key,''),COALESCE(reference,''),metadata,created_by,created_at FROM wallet_transactions
		WHERE wallet_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, walletID, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.WalletTransaction
	for rows.Next() {
		transaction, err := scanWalletTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, transaction)
	}
	return out, rows.Err()
}

func settleBillingUsage(ctx context.Context, tx pgx.Tx, logEntry domain.RequestLog, organizationID, projectID string) error {
	// The legacy function name is retained for callers, but all new charges are
	// settled by the immutable pricing snapshot path. EstimatedCost is never
	// used as a wallet input.
	return settlePricingUsage(ctx, tx, logEntry, organizationID, projectID)
}
