package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

var ErrPriceChangeState = errors.New("provider cost change does not allow this transition")
var ErrPriceChangeSelfReview = errors.New("provider cost change requester cannot review the same request")

func (s *Store) CreateProviderCostChange(ctx context.Context, change domain.ProviderCostChangeRequest, actor *string) (domain.ProviderCostChangeRequest, bool, error) {
	change, err := normalizeProviderCostChange(change)
	if err != nil {
		return change, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return change, false, err
	}
	defer tx.Rollback(ctx)
	change, replayed, err := createProviderCostChangeTx(ctx, tx, change, actor)
	if err != nil {
		return change, false, err
	}
	return change, replayed, tx.Commit(ctx)
}

func normalizeProviderCostChange(change domain.ProviderCostChangeRequest) (domain.ProviderCostChangeRequest, error) {
	entry := domain.ProviderCostPriceBook{ProviderID: change.ProviderID, ModelID: change.ModelID, InputTokenCost: change.InputTokenCost,
		CachedInputTokenCost: change.CachedInputTokenCost, OutputTokenCost: change.OutputTokenCost,
		RequestFixedCost: change.RequestFixedCost, Currency: change.Currency, Unit: change.Unit,
		EffectiveAt: change.EffectiveAt, ExpiresAt: change.ExpiresAt, Source: change.SourceType}
	if err := validateCostEntry(entry); err != nil {
		return change, err
	}
	change.SourceType = strings.ToUpper(strings.TrimSpace(change.SourceType))
	if change.SourceType != "MANUAL" && change.SourceType != "API" && change.SourceType != "CSV" {
		return change, errors.New("source_type must be MANUAL, API, or CSV")
	}
	if strings.TrimSpace(change.IdempotencyKey) == "" || len(change.IdempotencyKey) > 200 {
		return change, errors.New("idempotency_key is required and must not exceed 200 characters")
	}
	if change.EffectiveAt.IsZero() {
		change.EffectiveAt = time.Now().UTC()
	}
	return change, nil
}

func createProviderCostChangeTx(ctx context.Context, tx pgx.Tx, change domain.ProviderCostChangeRequest, actor *string) (domain.ProviderCostChangeRequest, bool, error) {
	fingerprint := providerCostChangeFingerprint(change)
	// Serialize the check/insert pair without exposing the idempotency key or
	// relying on a unique-violation retry that would abort the transaction.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, change.IdempotencyKey); err != nil {
		return change, false, err
	}
	var existingFingerprint string
	err := tx.QueryRow(ctx, `SELECT request_fingerprint FROM provider_cost_change_requests WHERE idempotency_key=$1 FOR UPDATE`, change.IdempotencyKey).Scan(&existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return change, false, ErrIdempotencyConflict
		}
		existing, readErr := providerCostChangeByKeyTx(ctx, tx, change.IdempotencyKey)
		if readErr != nil {
			return change, false, readErr
		}
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return change, false, err
	}
	change.ID, change.Status, change.RequestedBy = id.UUID(), "PENDING", actor
	var previousID *string
	_ = tx.QueryRow(ctx, `SELECT id FROM provider_cost_price_book WHERE provider_id=$1 AND model_id=$2
		AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=$3
		AND (expires_at IS NULL OR expires_at>$3) ORDER BY effective_at DESC,id DESC LIMIT 1`,
		change.ProviderID, change.ModelID, change.EffectiveAt).Scan(&previousID)
	change.PreviousPriceBookID = previousID
	change.ChangeSummary = map[string]any{"previous_price_book_id": previousID, "effective_at": change.EffectiveAt, "source_type": change.SourceType}
	err = tx.QueryRow(ctx, `INSERT INTO provider_cost_change_requests(id,idempotency_key,provider_id,model_id,source_type,source_reference,
		input_token_cost,cached_input_token_cost,output_token_cost,request_fixed_cost,currency,unit,effective_at,expires_at,status,
		request_fingerprint,previous_price_book_id,change_summary,requested_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'PENDING',$15,$16,$17,$18) RETURNING created_at,updated_at`,
		change.ID, change.IdempotencyKey, change.ProviderID, change.ModelID, change.SourceType, change.SourceReference,
		change.InputTokenCost, change.CachedInputTokenCost, change.OutputTokenCost, change.RequestFixedCost,
		strings.ToUpper(change.Currency), change.Unit, change.EffectiveAt, change.ExpiresAt, fingerprint, previousID,
		jsonBytes(change.ChangeSummary), actor).Scan(&change.CreatedAt, &change.UpdatedAt)
	if err != nil {
		return change, false, err
	}
	if err = writeAuditTx(ctx, tx, actor, "pricing.provider_cost_change_requested", "provider_cost_change_request", change.ID,
		map[string]any{"provider_id": change.ProviderID, "model_id": change.ModelID, "source_type": change.SourceType, "effective_at": change.EffectiveAt}); err != nil {
		return change, false, err
	}
	return change, false, nil
}

// CreateProviderCostChanges atomically accepts a validated import batch. If
// any row is invalid or conflicts, no new request from the batch is retained.
func (s *Store) CreateProviderCostChanges(ctx context.Context, changes []domain.ProviderCostChangeRequest, actor *string) ([]domain.ProviderCostChangeRequest, int, error) {
	if len(changes) == 0 || len(changes) > 500 {
		return nil, 0, errors.New("provider cost change batch must contain 1 to 500 rows")
	}
	normalized := make([]domain.ProviderCostChangeRequest, len(changes))
	for index, change := range changes {
		var err error
		normalized[index], err = normalizeProviderCostChange(change)
		if err != nil {
			return nil, 0, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	result := make([]domain.ProviderCostChangeRequest, 0, len(normalized))
	replayed := 0
	for _, change := range normalized {
		created, wasReplayed, createErr := createProviderCostChangeTx(ctx, tx, change, actor)
		if createErr != nil {
			return nil, 0, createErr
		}
		if wasReplayed {
			replayed++
		}
		result = append(result, created)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, err
	}
	return result, replayed, nil
}

func (s *Store) ReviewProviderCostChange(ctx context.Context, changeID, decision, reason string, actor *string) (domain.ProviderCostChangeRequest, error) {
	decision = strings.ToUpper(strings.TrimSpace(decision))
	if decision != "APPROVE" && decision != "REJECT" {
		return domain.ProviderCostChangeRequest{}, errors.New("decision must be APPROVE or REJECT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProviderCostChangeRequest{}, err
	}
	defer tx.Rollback(ctx)
	change, err := providerCostChangeByIDTx(ctx, tx, changeID, true)
	if err != nil {
		return change, err
	}
	if change.Status != "PENDING" {
		return change, ErrPriceChangeState
	}
	if actor != nil && change.RequestedBy != nil && *actor == *change.RequestedBy {
		return change, ErrPriceChangeSelfReview
	}
	status := "REJECTED"
	var publishedID *string
	if decision == "APPROVE" {
		status = "APPROVED"
		priceID := id.UUID()
		_, err = tx.Exec(ctx, `INSERT INTO provider_cost_price_book(id,provider_id,model_id,input_token_cost,cached_input_token_cost,
			output_token_cost,request_fixed_cost,currency,unit,effective_at,expires_at,source,created_by,approval_status)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'APPROVED')`, priceID, change.ProviderID, change.ModelID,
			change.InputTokenCost, change.CachedInputTokenCost, change.OutputTokenCost, change.RequestFixedCost,
			change.Currency, change.Unit, change.EffectiveAt, change.ExpiresAt, strings.ToLower(change.SourceType)+":"+change.ID, actor)
		if err != nil {
			return change, err
		}
		publishedID = &priceID
		_, err = tx.Exec(ctx, `INSERT INTO alerts(id,kind,severity,message,resource_type,resource_id)
			VALUES($1,'provider_cost_changed','warning','An approved Provider cost change was published.','provider_cost_change_request',$2)`, id.UUID(), change.ID)
		if err != nil {
			return change, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE provider_cost_change_requests SET status=$2,published_price_book_id=$3,reviewed_by=$4,
		review_reason=$5,reviewed_at=now(),updated_at=now() WHERE id=$1`, change.ID, status, publishedID, actor, reason)
	if err != nil {
		return change, err
	}
	if err = writeAuditTx(ctx, tx, actor, "pricing.provider_cost_change_"+strings.ToLower(status), "provider_cost_change_request", change.ID,
		map[string]any{"reason": reason, "published_price_book_id": publishedID}); err != nil {
		return change, err
	}
	if err = tx.Commit(ctx); err != nil {
		return change, err
	}
	return s.ProviderCostChangeByID(ctx, change.ID)
}

func (s *Store) ListProviderCostChanges(ctx context.Context, status string) ([]domain.ProviderCostChangeRequest, error) {
	rows, err := s.pool.Query(ctx, providerCostChangeSelect+` WHERE ($1='' OR status=$1) ORDER BY created_at DESC,id DESC`, strings.ToUpper(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProviderCostChangeRequest, 0)
	for rows.Next() {
		var change domain.ProviderCostChangeRequest
		if err = scanProviderCostChange(rows, &change); err != nil {
			return nil, err
		}
		out = append(out, change)
	}
	return out, rows.Err()
}

func (s *Store) ProviderCostChangeByID(ctx context.Context, changeID string) (domain.ProviderCostChangeRequest, error) {
	return providerCostChangeByIDTx(ctx, s.pool, changeID, false)
}

type providerCostChangeQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const providerCostChangeSelect = `SELECT id,idempotency_key,provider_id,model_id,source_type,source_reference,input_token_cost::text,
	cached_input_token_cost::text,output_token_cost::text,request_fixed_cost::text,currency,unit,effective_at,expires_at,status,
	previous_price_book_id,published_price_book_id,change_summary,requested_by,reviewed_by,review_reason,reviewed_at,created_at,updated_at
	FROM provider_cost_change_requests`

func providerCostChangeByIDTx(ctx context.Context, q providerCostChangeQuerier, id string, lock bool) (domain.ProviderCostChangeRequest, error) {
	suffix := " WHERE id=$1"
	if lock {
		suffix += " FOR UPDATE"
	}
	var change domain.ProviderCostChangeRequest
	err := scanProviderCostChange(q.QueryRow(ctx, providerCostChangeSelect+suffix, id), &change)
	if errors.Is(err, pgx.ErrNoRows) {
		return change, ErrNotFound
	}
	return change, err
}
func providerCostChangeByKeyTx(ctx context.Context, q providerCostChangeQuerier, key string) (domain.ProviderCostChangeRequest, error) {
	var change domain.ProviderCostChangeRequest
	err := scanProviderCostChange(q.QueryRow(ctx, providerCostChangeSelect+` WHERE idempotency_key=$1`, key), &change)
	return change, err
}
func scanProviderCostChange(row interface{ Scan(...any) error }, change *domain.ProviderCostChangeRequest) error {
	var summary []byte
	err := row.Scan(&change.ID, &change.IdempotencyKey, &change.ProviderID, &change.ModelID, &change.SourceType, &change.SourceReference,
		&change.InputTokenCost, &change.CachedInputTokenCost, &change.OutputTokenCost, &change.RequestFixedCost, &change.Currency, &change.Unit,
		&change.EffectiveAt, &change.ExpiresAt, &change.Status, &change.PreviousPriceBookID, &change.PublishedPriceBookID, &summary,
		&change.RequestedBy, &change.ReviewedBy, &change.ReviewReason, &change.ReviewedAt, &change.CreatedAt, &change.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal(summary, &change.ChangeSummary)
		if change.ChangeSummary == nil {
			change.ChangeSummary = map[string]any{}
		}
	}
	return err
}
func providerCostChangeFingerprint(change domain.ProviderCostChangeRequest) string {
	raw := strings.Join([]string{change.ProviderID, change.ModelID, strings.ToUpper(change.SourceType), change.SourceReference,
		change.InputTokenCost, change.CachedInputTokenCost, change.OutputTokenCost, change.RequestFixedCost, strings.ToUpper(change.Currency),
		strconv.FormatInt(change.Unit, 10), change.EffectiveAt.UTC().Format(time.RFC3339Nano)}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
