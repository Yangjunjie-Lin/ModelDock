package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var ErrWalletUnavailable = errors.New("wallet is frozen or has insufficient prepaid balance")

func scanWallet(row pgx.Row) (domain.Wallet, error) {
	var wallet domain.Wallet
	err := row.Scan(&wallet.ID, &wallet.OrganizationID, &wallet.OrganizationName, &wallet.Currency, &wallet.BillingMode,
		&wallet.AvailableBalance, &wallet.ReservedBalance, &wallet.CreditLimit, &wallet.Status, &wallet.Version,
		&wallet.CreatedAt, &wallet.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return wallet, ErrNotFound
	}
	return wallet, err
}

const walletColumns = `w.id,w.organization_id,o.name,w.currency,w.billing_mode,w.available_balance::float8,
	w.reserved_balance::float8,w.credit_limit::float8,w.status,w.version,w.created_at,w.updated_at`

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
	return wallet.BillingMode != "PREPAID" || wallet.AvailableBalance+wallet.CreditLimit > 0, nil
}

func (s *Store) UpdateWallet(ctx context.Context, wallet domain.Wallet) (domain.Wallet, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE wallets SET billing_mode=$2,credit_limit=$3,status=$4,updated_at=now(),version=version+1 WHERE id=$1`,
		wallet.ID, wallet.BillingMode, wallet.CreditLimit, wallet.Status)
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
	if transaction.IdempotencyKey != "" {
		existing, err := scanWalletTransaction(tx.QueryRow(ctx, `SELECT id,wallet_id,usage_record_id,transaction_type,
			amount::float8,balance_after::float8,COALESCE(idempotency_key,''),COALESCE(reference,''),metadata,created_by,created_at
			FROM wallet_transactions WHERE wallet_id=$1 AND idempotency_key=$2`, transaction.WalletID, transaction.IdempotencyKey))
		if err == nil {
			return existing, tx.Commit(ctx)
		}
		if !errors.Is(err, ErrNotFound) {
			return domain.WalletTransaction{}, err
		}
	}
	var balance, credit float64
	var status string
	if err = tx.QueryRow(ctx, `SELECT available_balance::float8,credit_limit::float8,status FROM wallets WHERE id=$1 FOR UPDATE`, transaction.WalletID).Scan(&balance, &credit, &status); errors.Is(err, pgx.ErrNoRows) {
		return domain.WalletTransaction{}, ErrNotFound
	} else if err != nil {
		return domain.WalletTransaction{}, err
	}
	if status != "ACTIVE" {
		return domain.WalletTransaction{}, ErrWalletUnavailable
	}
	newBalance := balance + transaction.Amount
	if transaction.Amount < 0 && newBalance+credit < 0 {
		return domain.WalletTransaction{}, ErrWalletUnavailable
	}
	transaction.ID = id.UUID()
	transaction.BalanceAfter = newBalance
	if transaction.Metadata == nil {
		transaction.Metadata = map[string]any{}
	}
	if _, err = tx.Exec(ctx, `UPDATE wallets SET available_balance=$2,version=version+1,updated_at=now() WHERE id=$1`, transaction.WalletID, newBalance); err != nil {
		return domain.WalletTransaction{}, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO wallet_transactions(id,wallet_id,usage_record_id,transaction_type,amount,
		balance_after,idempotency_key,reference,metadata,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING created_at`, transaction.ID, transaction.WalletID, transaction.UsageRecordID, transaction.TransactionType,
		transaction.Amount, transaction.BalanceAfter, nullString(transaction.IdempotencyKey), nullString(transaction.Reference),
		jsonBytes(transaction.Metadata), transaction.CreatedBy).Scan(&transaction.CreatedAt)
	if err != nil {
		return domain.WalletTransaction{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.WalletTransaction{}, err
	}
	return transaction, nil
}

func scanWalletTransaction(row pgx.Row) (domain.WalletTransaction, error) {
	var transaction domain.WalletTransaction
	var metadata []byte
	err := row.Scan(&transaction.ID, &transaction.WalletID, &transaction.UsageRecordID, &transaction.TransactionType,
		&transaction.Amount, &transaction.BalanceAfter, &transaction.IdempotencyKey, &transaction.Reference, &metadata,
		&transaction.CreatedBy, &transaction.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return transaction, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(metadata, &transaction.Metadata)
	}
	return transaction, err
}

func (s *Store) ListWalletTransactions(ctx context.Context, walletID string, limit, offset int) ([]domain.WalletTransaction, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,wallet_id,usage_record_id,transaction_type,amount::float8,balance_after::float8,
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
	status := "WAIVED"
	if logEntry.StatusCode < 400 && logEntry.EstimatedCost > 0 {
		status = "RECORDED"
	}
	usageID := id.UUID()
	var insertedID string
	err := tx.QueryRow(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,api_key_id,
		provider_id,model,input_tokens,cached_input_tokens,output_tokens,amount,status)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(request_id) DO NOTHING RETURNING id`, usageID,
		logEntry.RequestID, organizationID, projectID, nullString(logEntry.APIKeyID), nullString(logEntry.ProviderID),
		logEntry.ResolvedModel, logEntry.InputTokens, logEntry.CachedInputTokens, logEntry.OutputTokens,
		logEntry.EstimatedCost, status).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || status != "RECORDED" {
		return err
	}
	var walletID string
	var balance float64
	err = tx.QueryRow(ctx, `SELECT id,available_balance::float8 FROM wallets WHERE organization_id=$1 AND status='ACTIVE' FOR UPDATE`, organizationID).Scan(&walletID, &balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	newBalance := balance - logEntry.EstimatedCost
	transactionID := id.UUID()
	_, err = tx.Exec(ctx, `UPDATE wallets SET available_balance=$2,version=version+1,updated_at=now() WHERE id=$1`, walletID, newBalance)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,usage_record_id,transaction_type,amount,
		balance_after,idempotency_key,reference,metadata) VALUES($1,$2,$3,'CHARGE',$4,$5,$6,$6,$7)`, transactionID,
		walletID, insertedID, -logEntry.EstimatedCost, newBalance, "usage:"+logEntry.RequestID,
		jsonBytes(map[string]any{"request_id": logEntry.RequestID, "model": logEntry.ResolvedModel}))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE billing_usage_records SET status='CHARGED' WHERE id=$1`, insertedID)
	return err
}
