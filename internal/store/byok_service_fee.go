package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/domain"
	"github.com/relayedock/relayedock/internal/id"
)

func (s *Store) CreateBYOKServiceFeePolicy(ctx context.Context, policy domain.BYOKServiceFeePolicy, actor *string) (domain.BYOKServiceFeePolicy, error) {
	entry := domain.ProviderCostPriceBook{InputTokenCost: policy.InputTokenFee, CachedInputTokenCost: policy.CachedInputTokenFee,
		OutputTokenCost: policy.OutputTokenFee, RequestFixedCost: policy.FixedFee, Currency: policy.Currency, Unit: policy.Unit,
		EffectiveAt: policy.EffectiveAt, ExpiresAt: policy.ExpiresAt, ProviderID: "byok", ModelID: "service", Source: "policy"}
	if err := validateCostEntry(entry); err != nil {
		return policy, err
	}
	if policy.EffectiveAt.IsZero() {
		return policy, errors.New("effective_at is required")
	}
	if policy.ServiceFeeBPS < 0 || policy.ServiceFeeBPS > 10000 {
		return policy, errors.New("service_fee_bps must be between 0 and 10000")
	}
	if strings.TrimSpace(policy.MonthlyFreeAllowance) == "" {
		policy.MonthlyFreeAllowance = "0"
	}
	allowance, allowanceErr := domain.ParseDecimal(policy.MonthlyFreeAllowance)
	if allowanceErr != nil {
		return policy, allowanceErr
	}
	if negative, negativeErr := allowance.IsNegative(); negativeErr != nil || negative {
		return policy, errors.New("monthly_free_allowance must be non-negative")
	}
	policy.ID = id.UUID()
	policy.Currency = strings.ToUpper(strings.TrimSpace(policy.Currency))
	policy.Enabled = true
	policy.CreatedBy = actor
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return policy, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO byok_service_fee_policies(id,organization_id,provider_id,fixed_fee,input_token_fee,
		cached_input_token_fee,output_token_fee,currency,unit,effective_at,expires_at,enabled,created_by,service_fee_bps,monthly_free_allowance)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,true,$12,$13,$14)
		RETURNING created_at`, policy.ID, policy.OrganizationID, policy.ProviderID, policy.FixedFee, policy.InputTokenFee,
		policy.CachedInputTokenFee, policy.OutputTokenFee, policy.Currency, policy.Unit, policy.EffectiveAt, policy.ExpiresAt, actor,
		policy.ServiceFeeBPS, policy.MonthlyFreeAllowance).
		Scan(&policy.CreatedAt)
	if err != nil {
		return policy, err
	}
	if err = writeAuditTx(ctx, tx, actor, "pricing.byok_service_fee_policy_created", "byok_service_fee_policy", policy.ID,
		map[string]any{"organization_id": policy.OrganizationID, "provider_id": policy.ProviderID, "currency": policy.Currency,
			"effective_at": policy.EffectiveAt, "expires_at": policy.ExpiresAt}); err != nil {
		return policy, err
	}
	if err = tx.Commit(ctx); err != nil {
		return policy, err
	}
	return policy, nil
}

func (s *Store) ListBYOKServiceFeePolicies(ctx context.Context) ([]domain.BYOKServiceFeePolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,organization_id,provider_id,fixed_fee::text,input_token_fee::text,
		cached_input_token_fee::text,output_token_fee::text,currency,unit,effective_at,expires_at,enabled,created_by,created_at,
		service_fee_bps,monthly_free_allowance::text
		FROM byok_service_fee_policies ORDER BY effective_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]domain.BYOKServiceFeePolicy, 0)
	for rows.Next() {
		var policy domain.BYOKServiceFeePolicy
		if err = scanBYOKServiceFeePolicy(rows, &policy); err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func (s *Store) DisableBYOKServiceFeePolicy(ctx context.Context, policyID string, actor *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE byok_service_fee_policies SET enabled=false WHERE id=$1 AND enabled=true`, policyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = writeAuditTx(ctx, tx, actor, "pricing.byok_service_fee_policy_disabled", "byok_service_fee_policy", policyID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanBYOKServiceFeePolicy(row interface{ Scan(...any) error }, policy *domain.BYOKServiceFeePolicy) error {
	return row.Scan(&policy.ID, &policy.OrganizationID, &policy.ProviderID, &policy.FixedFee, &policy.InputTokenFee,
		&policy.CachedInputTokenFee, &policy.OutputTokenFee, &policy.Currency, &policy.Unit, &policy.EffectiveAt,
		&policy.ExpiresAt, &policy.Enabled, &policy.CreatedBy, &policy.CreatedAt, &policy.ServiceFeeBPS, &policy.MonthlyFreeAllowance)
}

func (s *Store) activeBYOKServiceFeePolicy(ctx context.Context, organizationID, providerID string, at time.Time) (domain.BYOKServiceFeePolicy, error) {
	var policy domain.BYOKServiceFeePolicy
	err := scanBYOKServiceFeePolicy(s.pool.QueryRow(ctx, `SELECT id,organization_id,provider_id,fixed_fee::text,input_token_fee::text,
		cached_input_token_fee::text,output_token_fee::text,currency,unit,effective_at,expires_at,enabled,created_by,created_at,
		service_fee_bps,monthly_free_allowance::text
		FROM byok_service_fee_policies WHERE enabled=true AND (organization_id=$1 OR organization_id IS NULL)
		AND (provider_id=$2 OR provider_id IS NULL) AND effective_at<=$3 AND (expires_at IS NULL OR expires_at>$3)
		ORDER BY (organization_id IS NOT NULL) DESC,(provider_id IS NOT NULL) DESC,effective_at DESC,id DESC LIMIT 1`,
		organizationID, providerID, at), &policy)
	if errors.Is(err, pgx.ErrNoRows) {
		return policy, ErrPricingUnavailable
	}
	return policy, err
}
