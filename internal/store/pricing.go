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
	"github.com/relayedock/relayedock/internal/pricing"
)

var (
	ErrPricingUnavailable        = errors.New("no approved pricing is available for this provider and model")
	ErrProviderNotContracted     = errors.New("provider contract or region policy does not allow pricing")
	ErrNegativeMargin            = errors.New("retail price is below the configured minimum gross margin")
	ErrForceOverrideConfirmation = errors.New("a second confirmation is required for a margin override")
	ErrPricingCurrencyMismatch   = errors.New("provider cost and retail price currencies must match")
)

type PriceQuoteRequest struct {
	OrganizationID    string
	ProviderID        string
	Model             string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	PromotionAmount   string
	TaxRate           string
	ExchangeRate      string
	IdempotencyKey    string
	CreatedBy         *string
}

type costPriceRow struct {
	ID, ProviderID, ModelID                                  string
	Input, Cached, Output, Fixed, Currency, Source, Approval string
	Unit                                                     int64
	EffectiveAt                                              time.Time
	ExpiresAt                                                *time.Time
}

type retailPriceRow struct {
	ID                                                       string
	OrganizationID                                           *string
	ProviderID, ModelID                                      string
	Input, Cached, Output, Fixed, Currency, Source, Approval string
	Unit                                                     int64
	EffectiveAt                                              time.Time
	ExpiresAt                                                *time.Time
	PlanID                                                   *string
	PlanType                                                 string
}

func (s *Store) CheckOrganizationPricingAccess(ctx context.Context, userID, organizationID string) error {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_memberships om JOIN organizations o ON o.id=om.organization_id WHERE om.user_id=$1 AND om.organization_id=$2 AND om.status='ACTIVE' AND o.status='ACTIVE')`, userID, organizationID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateProviderCostPriceBook(ctx context.Context, entry domain.ProviderCostPriceBook, actorID *string) (domain.ProviderCostPriceBook, error) {
	// Internal compatibility helper for migrations/tests. Public control-plane
	// writes use provider_cost_change_requests and approval before calling this
	// append-only primitive.
	if err := validateCostEntry(entry); err != nil {
		return domain.ProviderCostPriceBook{}, err
	}
	entry.ID = id.UUID()
	entry.CreatedBy = actorID
	if entry.EffectiveAt.IsZero() {
		entry.EffectiveAt = time.Now().UTC()
	}
	if entry.ApprovalStatus == "" {
		entry.ApprovalStatus = "APPROVED"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO provider_cost_price_book(id,provider_id,model_id,input_token_cost,cached_input_token_cost,output_token_cost,request_fixed_cost,currency,unit,effective_at,expires_at,source,created_by,approval_status)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, entry.ID, entry.ProviderID, entry.ModelID, entry.InputTokenCost, entry.CachedInputTokenCost, entry.OutputTokenCost, entry.RequestFixedCost, strings.ToUpper(entry.Currency), entry.Unit, entry.EffectiveAt, entry.ExpiresAt, entry.Source, entry.CreatedBy, entry.ApprovalStatus)
	if err != nil {
		return domain.ProviderCostPriceBook{}, err
	}
	return s.providerCostPriceBookByID(ctx, entry.ID)
}

func (s *Store) ListProviderCostPriceBooks(ctx context.Context, providerID, modelID string) ([]domain.ProviderCostPriceBook, error) {
	q := `SELECT id,provider_id,model_id,input_token_cost::text,cached_input_token_cost::text,output_token_cost::text,request_fixed_cost::text,currency,unit,effective_at,expires_at,source,created_by,approval_status,created_at FROM provider_cost_price_book WHERE ($1='' OR provider_id=$1) AND ($2='' OR model_id=$2) ORDER BY effective_at DESC,id DESC`
	rows, err := s.pool.Query(ctx, q, providerID, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ProviderCostPriceBook, 0)
	for rows.Next() {
		var v domain.ProviderCostPriceBook
		if err := rows.Scan(&v.ID, &v.ProviderID, &v.ModelID, &v.InputTokenCost, &v.CachedInputTokenCost, &v.OutputTokenCost, &v.RequestFixedCost, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.CreatedBy, &v.ApprovalStatus, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) providerCostPriceBookByID(ctx context.Context, priceID string) (domain.ProviderCostPriceBook, error) {
	var v domain.ProviderCostPriceBook
	err := s.pool.QueryRow(ctx, `SELECT id,provider_id,model_id,input_token_cost::text,cached_input_token_cost::text,output_token_cost::text,request_fixed_cost::text,currency,unit,effective_at,expires_at,source,created_by,approval_status,created_at FROM provider_cost_price_book WHERE id=$1`, priceID).Scan(&v.ID, &v.ProviderID, &v.ModelID, &v.InputTokenCost, &v.CachedInputTokenCost, &v.OutputTokenCost, &v.RequestFixedCost, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.CreatedBy, &v.ApprovalStatus, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func (s *Store) CreateCustomerRetailPriceBook(ctx context.Context, entry domain.CustomerRetailPriceBook, actorID *string, forceOverride bool, confirmation string) (domain.CustomerRetailPriceBook, error) {
	if err := validateRetailEntry(entry); err != nil {
		return domain.CustomerRetailPriceBook{}, err
	}
	if entry.EffectiveAt.IsZero() {
		entry.EffectiveAt = time.Now().UTC()
	}
	overridden, err := s.ensureRetailMargin(ctx, entry.OrganizationID, entry.ProviderID, entry.ModelID, entry.EffectiveAt, pricing.Rate{Input: entry.InputTokenPrice, Cached: entry.CachedInputTokenPrice, Output: entry.OutputTokenPrice, Fixed: entry.RequestFixedPrice, Unit: entry.Unit, Currency: entry.Currency}, forceOverride, confirmation)
	if err != nil {
		return domain.CustomerRetailPriceBook{}, err
	}
	entry.ID = id.UUID()
	entry.CreatedBy = actorID
	entry.ApprovalStatus = "APPROVED"
	if overridden {
		entry.ApprovalStatus = "FORCED_APPROVED"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CustomerRetailPriceBook{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO customer_retail_price_book(id,organization_id,provider_id,model_id,input_token_price,cached_input_token_price,output_token_price,request_fixed_price,currency,unit,effective_at,expires_at,source,created_by,approval_status)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, entry.ID, entry.OrganizationID, entry.ProviderID, entry.ModelID, entry.InputTokenPrice, entry.CachedInputTokenPrice, entry.OutputTokenPrice, entry.RequestFixedPrice, strings.ToUpper(entry.Currency), entry.Unit, entry.EffectiveAt, entry.ExpiresAt, entry.Source, entry.CreatedBy, entry.ApprovalStatus)
	if err != nil {
		return domain.CustomerRetailPriceBook{}, err
	}
	if overridden {
		if err = writeAuditTx(ctx, tx, actorID, "pricing.negative_margin_override", "customer_retail_price_book", entry.ID, map[string]any{"price": entry, "second_confirmation": true}); err != nil {
			return domain.CustomerRetailPriceBook{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.CustomerRetailPriceBook{}, err
	}
	return s.customerRetailPriceBookByID(ctx, entry.ID)
}

func (s *Store) ListCustomerRetailPriceBooks(ctx context.Context, organizationID, providerID, modelID string) ([]domain.CustomerRetailPriceBook, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,organization_id,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,created_by,approval_status,created_at FROM customer_retail_price_book WHERE ($1='' OR organization_id::text=$1 OR organization_id IS NULL) AND ($2='' OR provider_id=$2) AND ($3='' OR model_id=$3) ORDER BY organization_id NULLS LAST,effective_at DESC,id DESC`, organizationID, providerID, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.CustomerRetailPriceBook, 0)
	for rows.Next() {
		var v domain.CustomerRetailPriceBook
		if err := rows.Scan(&v.ID, &v.OrganizationID, &v.ProviderID, &v.ModelID, &v.InputTokenPrice, &v.CachedInputTokenPrice, &v.OutputTokenPrice, &v.RequestFixedPrice, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.CreatedBy, &v.ApprovalStatus, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) customerRetailPriceBookByID(ctx context.Context, priceID string) (domain.CustomerRetailPriceBook, error) {
	var v domain.CustomerRetailPriceBook
	err := s.pool.QueryRow(ctx, `SELECT id,organization_id,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,created_by,approval_status,created_at FROM customer_retail_price_book WHERE id=$1`, priceID).Scan(&v.ID, &v.OrganizationID, &v.ProviderID, &v.ModelID, &v.InputTokenPrice, &v.CachedInputTokenPrice, &v.OutputTokenPrice, &v.RequestFixedPrice, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.CreatedBy, &v.ApprovalStatus, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func (s *Store) CreateOrganizationPricePlan(ctx context.Context, entry domain.OrganizationPricePlan, actorID *string, forceOverride bool, confirmation string) (domain.OrganizationPricePlan, error) {
	if entry.PlanType != "SUBSCRIPTION" && entry.PlanType != "ORGANIZATION_OVERRIDE" {
		return domain.OrganizationPricePlan{}, errors.New("invalid price plan type")
	}
	if err := validateRetailEntry(domain.CustomerRetailPriceBook{ProviderID: entry.ProviderID, ModelID: entry.ModelID, InputTokenPrice: entry.InputTokenPrice, CachedInputTokenPrice: entry.CachedInputTokenPrice, OutputTokenPrice: entry.OutputTokenPrice, RequestFixedPrice: entry.RequestFixedPrice, Currency: entry.Currency, Unit: entry.Unit, Source: entry.Source}); err != nil {
		return domain.OrganizationPricePlan{}, err
	}
	if entry.EffectiveAt.IsZero() {
		entry.EffectiveAt = time.Now().UTC()
	}
	overridden, err := s.ensureRetailMargin(ctx, &entry.OrganizationID, entry.ProviderID, entry.ModelID, entry.EffectiveAt, pricing.Rate{Input: entry.InputTokenPrice, Cached: entry.CachedInputTokenPrice, Output: entry.OutputTokenPrice, Fixed: entry.RequestFixedPrice, Unit: entry.Unit, Currency: entry.Currency}, forceOverride, confirmation)
	if err != nil {
		return domain.OrganizationPricePlan{}, err
	}
	entry.ID = id.UUID()
	entry.CreatedBy = actorID
	entry.ApprovalStatus = "APPROVED"
	if overridden {
		entry.ApprovalStatus = "FORCED_APPROVED"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.OrganizationPricePlan{}, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO organization_price_plan(id,organization_id,name,plan_type,provider_id,model_id,input_token_price,cached_input_token_price,output_token_price,request_fixed_price,currency,unit,effective_at,expires_at,source,created_by,approval_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, entry.ID, entry.OrganizationID, entry.Name, entry.PlanType, entry.ProviderID, entry.ModelID, entry.InputTokenPrice, entry.CachedInputTokenPrice, entry.OutputTokenPrice, entry.RequestFixedPrice, strings.ToUpper(entry.Currency), entry.Unit, entry.EffectiveAt, entry.ExpiresAt, entry.Source, entry.CreatedBy, entry.ApprovalStatus)
	if err != nil {
		return domain.OrganizationPricePlan{}, err
	}
	if overridden {
		if err = writeAuditTx(ctx, tx, actorID, "pricing.negative_margin_override", "organization_price_plan", entry.ID, map[string]any{"price": entry, "second_confirmation": true}); err != nil {
			return domain.OrganizationPricePlan{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.OrganizationPricePlan{}, err
	}
	return s.organizationPricePlanByID(ctx, entry.ID)
}

func (s *Store) ListOrganizationPricePlans(ctx context.Context, organizationID string) ([]domain.OrganizationPricePlan, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,organization_id,name,plan_type,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,created_by,approval_status,created_at FROM organization_price_plan WHERE ($1='' OR organization_id=$1) ORDER BY effective_at DESC,id DESC`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OrganizationPricePlan, 0)
	for rows.Next() {
		var v domain.OrganizationPricePlan
		if err := rows.Scan(&v.ID, &v.OrganizationID, &v.Name, &v.PlanType, &v.ProviderID, &v.ModelID, &v.InputTokenPrice, &v.CachedInputTokenPrice, &v.OutputTokenPrice, &v.RequestFixedPrice, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.CreatedBy, &v.ApprovalStatus, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) organizationPricePlanByID(ctx context.Context, priceID string) (domain.OrganizationPricePlan, error) {
	var v domain.OrganizationPricePlan
	err := s.pool.QueryRow(ctx, `SELECT id,organization_id,name,plan_type,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,created_by,approval_status,created_at FROM organization_price_plan WHERE id=$1`, priceID).Scan(&v.ID, &v.OrganizationID, &v.Name, &v.PlanType, &v.ProviderID, &v.ModelID, &v.InputTokenPrice, &v.CachedInputTokenPrice, &v.OutputTokenPrice, &v.RequestFixedPrice, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.CreatedBy, &v.ApprovalStatus, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

func (s *Store) CreatePricingMarginPolicy(ctx context.Context, policy domain.PricingMarginPolicy, actorID *string) (domain.PricingMarginPolicy, error) {
	if policy.MinimumMarginBPS < 0 || policy.MinimumMarginBPS > 100000 {
		return domain.PricingMarginPolicy{}, errors.New("minimum_margin_bps must be between 0 and 100000")
	}
	if policy.OrganizationID == nil && policy.ProviderID == nil && policy.ModelID == nil {
		return domain.PricingMarginPolicy{}, errors.New("a margin policy must target an organization, provider, or model")
	}
	policy.ID = id.UUID()
	policy.CreatedBy = actorID
	_, err := s.pool.Exec(ctx, `INSERT INTO pricing_margin_policies(id,organization_id,provider_id,model_id,minimum_margin_amount,minimum_margin_bps,enabled,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, policy.ID, policy.OrganizationID, policy.ProviderID, policy.ModelID, zeroIfEmpty(policy.MinimumMarginAmount), policy.MinimumMarginBPS, policy.Enabled, policy.CreatedBy)
	if err != nil {
		return domain.PricingMarginPolicy{}, err
	}
	return policy, nil
}
func (s *Store) ListPricingMarginPolicies(ctx context.Context) ([]domain.PricingMarginPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,organization_id,provider_id,model_id,minimum_margin_amount::text,minimum_margin_bps,enabled,created_by FROM pricing_margin_policies ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PricingMarginPolicy, 0)
	for rows.Next() {
		var v domain.PricingMarginPolicy
		if err := rows.Scan(&v.ID, &v.OrganizationID, &v.ProviderID, &v.ModelID, &v.MinimumMarginAmount, &v.MinimumMarginBPS, &v.Enabled, &v.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CreatePromotionCredit(ctx context.Context, credit domain.PromotionCredit, actorID *string) (domain.PromotionCredit, error) {
	if credit.OrganizationID == "" || credit.Currency == "" || credit.Source == "" || !decimalPositive(credit.AmountGranted) || credit.IdempotencyKey == "" || len(credit.IdempotencyKey) > 200 {
		return domain.PromotionCredit{}, errors.New("organization_id, positive amount_granted, currency, source, and idempotency_key are required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.PromotionCredit{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "promotion:"+credit.OrganizationID+":"+credit.IdempotencyKey); err != nil {
		return domain.PromotionCredit{}, err
	}
	var existing domain.PromotionCredit
	err = tx.QueryRow(ctx, `SELECT id,organization_id,currency,amount_granted::text,amount_remaining::text,idempotency_key,source,non_refundable,status,expires_at,created_by,created_at FROM promotion_credit WHERE organization_id=$1 AND idempotency_key=$2`, credit.OrganizationID, credit.IdempotencyKey).Scan(&existing.ID, &existing.OrganizationID, &existing.Currency, &existing.AmountGranted, &existing.AmountRemaining, &existing.IdempotencyKey, &existing.Source, &existing.NonRefundable, &existing.Status, &existing.ExpiresAt, &existing.CreatedBy, &existing.CreatedAt)
	if err == nil {
		if existing.Currency != strings.ToUpper(credit.Currency) || !decimalEqual(existing.AmountGranted, credit.AmountGranted) || existing.Source != credit.Source {
			return domain.PromotionCredit{}, ErrIdempotencyConflict
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.PromotionCredit{}, err
	}
	credit.ID = id.UUID()
	credit.AmountRemaining = credit.AmountGranted
	credit.NonRefundable = true
	credit.Status = "ACTIVE"
	credit.CreatedBy = actorID
	err = tx.QueryRow(ctx, `INSERT INTO promotion_credit(id,organization_id,currency,amount_granted,amount_remaining,idempotency_key,source,non_refundable,status,expires_at,created_by) VALUES($1,$2,$3,$4,$4,$5,$6,true,'ACTIVE',$7,$8) RETURNING created_at`, credit.ID, credit.OrganizationID, strings.ToUpper(credit.Currency), credit.AmountGranted, credit.IdempotencyKey, credit.Source, credit.ExpiresAt, credit.CreatedBy).Scan(&credit.CreatedAt)
	if err != nil {
		return domain.PromotionCredit{}, err
	}
	credit.Currency = strings.ToUpper(credit.Currency)
	if err = writeAuditTx(ctx, tx, actorID, "promotion_credit.granted", "promotion_credit", credit.ID, credit); err != nil {
		return domain.PromotionCredit{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.PromotionCredit{}, err
	}
	return credit, nil
}

func (s *Store) ListPromotionCredits(ctx context.Context, organizationID string) ([]domain.PromotionCredit, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,organization_id,currency,amount_granted::text,amount_remaining::text,idempotency_key,source,non_refundable,status,expires_at,created_by,created_at FROM promotion_credit WHERE ($1='' OR organization_id=$1) ORDER BY created_at DESC,id DESC`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PromotionCredit, 0)
	for rows.Next() {
		var v domain.PromotionCredit
		if err := rows.Scan(&v.ID, &v.OrganizationID, &v.Currency, &v.AmountGranted, &v.AmountRemaining, &v.IdempotencyKey, &v.Source, &v.NonRefundable, &v.Status, &v.ExpiresAt, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) QuotePricing(ctx context.Context, request PriceQuoteRequest) (domain.PricingQuote, error) {
	if request.InputTokens < 0 || request.CachedInputTokens < 0 || request.OutputTokens < 0 || request.CachedInputTokens > request.InputTokens {
		return domain.PricingQuote{}, errors.New("invalid token estimates")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return domain.PricingQuote{}, err
	}
	defer tx.Rollback(ctx)
	if request.IdempotencyKey != "" {
		var existing domain.PricingQuote
		err = scanPricingQuote(tx.QueryRow(ctx, `SELECT id,organization_id,provider_id,model_id,model,estimated_input_tokens,estimated_cached_input_tokens,estimated_output_tokens,pricing_version_id,provider_cost_amount::text,retail_amount::text,promotion_amount::text,currency,exchange_rate::text,gross_margin::text,pre_tax_amount::text,tax_rate::text,tax_amount::text,final_amount::text,expires_at,created_at FROM pricing_quote WHERE organization_id=$1 AND idempotency_key=$2`, request.OrganizationID, request.IdempotencyKey), &existing)
		if err == nil {
			return existing, tx.Commit(ctx)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.PricingQuote{}, err
		}
	}
	if strings.TrimSpace(request.ProviderID) == "" {
		if err = tx.QueryRow(ctx, `SELECT p.id FROM models m JOIN providers p ON p.id=m.provider_id WHERE (m.id::text=$1 OR m.provider_model_id=$1) AND m.enabled=true AND p.enabled=true ORDER BY p.id LIMIT 1`, request.Model).Scan(&request.ProviderID); err != nil {
			return domain.PricingQuote{}, ErrPricingUnavailable
		}
	}
	if _, admissionErr := checkProviderAdmission(ctx, tx, request.OrganizationID, derefString(request.CreatedBy), request.ProviderID, request.Model); admissionErr != nil {
		if isProviderAdmissionError(admissionErr) {
			return domain.PricingQuote{}, ErrProviderNotContracted
		}
		return domain.PricingQuote{}, admissionErr
	}
	var modelID, modelName string
	err = tx.QueryRow(ctx, `SELECT id,provider_model_id FROM models WHERE provider_id=$1 AND (id::text=$2 OR provider_model_id=$2) AND enabled=true LIMIT 1`, request.ProviderID, request.Model).Scan(&modelID, &modelName)
	if err != nil {
		return domain.PricingQuote{}, ErrPricingUnavailable
	}
	cost, err := selectCostPrice(ctx, tx, request.ProviderID, modelID)
	if err != nil {
		return domain.PricingQuote{}, err
	}
	retail, err := selectRetailPrice(ctx, tx, request.OrganizationID, request.ProviderID, modelID)
	if err != nil {
		return domain.PricingQuote{}, err
	}
	promo := request.PromotionAmount
	if strings.TrimSpace(promo) == "" {
		_ = tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_remaining),0)::text FROM promotion_credit WHERE organization_id=$1 AND currency=$2 AND status='ACTIVE' AND (expires_at IS NULL OR expires_at>now())`, request.OrganizationID, retail.Currency).Scan(&promo)
	}
	result, err := pricing.Calculate(pricing.Rate{Input: cost.Input, Cached: cost.Cached, Output: cost.Output, Fixed: cost.Fixed, Unit: cost.Unit, Currency: cost.Currency}, pricing.Rate{Input: retail.Input, Cached: retail.Cached, Output: retail.Output, Fixed: retail.Fixed, Unit: retail.Unit, Currency: retail.Currency}, pricing.Tokens{Input: request.InputTokens, Cached: request.CachedInputTokens, Output: request.OutputTokens}, promo, request.TaxRate, request.ExchangeRate)
	if err != nil {
		return domain.PricingQuote{}, err
	}
	var minimumMargin string
	if err = tx.QueryRow(ctx, `SELECT minimum_gross_margin::text FROM organizations WHERE id=$1`, request.OrganizationID).Scan(&minimumMargin); err != nil {
		return domain.PricingQuote{}, err
	}
	margin, marginOK := new(big.Rat).SetString(result.GrossMargin)
	minimum, minimumOK := new(big.Rat).SetString(zeroIfEmpty(minimumMargin))
	if !marginOK || !minimumOK || margin.Cmp(minimum) < 0 {
		return domain.PricingQuote{}, ErrProviderMarginInsufficient
	}
	versionID := id.UUID()
	var version int64
	if err = tx.QueryRow(ctx, `SELECT nextval('model_price_version_sequence')`).Scan(&version); err != nil {
		return domain.PricingQuote{}, err
	}
	var customerBookID, planID *string
	if retail.PlanID == nil {
		customerBookID = &retail.ID
	} else {
		planID = retail.PlanID
	}
	_, err = tx.Exec(ctx, `INSERT INTO model_price_version(id,provider_id,model_id,organization_id,provider_cost_price_book_id,customer_retail_price_book_id,organization_price_plan_id,version,provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,provider_request_fixed_cost,retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,retail_request_fixed_price,provider_currency,retail_currency,provider_unit,retail_unit,effective_at,expires_at,source,created_by,approval_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,'APPROVED')`, versionID, request.ProviderID, modelID, request.OrganizationID, cost.ID, customerBookID, planID, version, cost.Input, cost.Cached, cost.Output, cost.Fixed, retail.Input, retail.Cached, retail.Output, retail.Fixed, cost.Currency, retail.Currency, cost.Unit, retail.Unit, maxTime(cost.EffectiveAt, retail.EffectiveAt), minTime(cost.ExpiresAt, retail.ExpiresAt), "resolved:"+retail.Source, request.CreatedBy)
	if err != nil {
		return domain.PricingQuote{}, err
	}
	quote := domain.PricingQuote{ID: id.UUID(), OrganizationID: request.OrganizationID, ProviderID: request.ProviderID, ModelID: modelID, Model: modelName, EstimatedInputTokens: request.InputTokens, EstimatedCachedInputTokens: request.CachedInputTokens, EstimatedOutputTokens: request.OutputTokens, PricingVersionID: versionID, ProviderCostAmount: result.ProviderCost, RetailAmount: result.RetailAmount, PromotionAmount: result.PromotionAmount, Currency: retail.Currency, ExchangeRate: result.ExchangeRate, GrossMargin: result.GrossMargin, PreTaxAmount: result.PreTaxAmount, TaxRate: result.TaxRate, TaxAmount: result.TaxAmount, FinalAmount: result.FinalAmount, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	err = tx.QueryRow(ctx, `INSERT INTO pricing_quote(id,idempotency_key,organization_id,provider_id,model_id,model,estimated_input_tokens,estimated_cached_input_tokens,estimated_output_tokens,pricing_version_id,provider_cost_amount,retail_amount,promotion_amount,currency,exchange_rate,gross_margin,pre_tax_amount,tax_rate,tax_amount,final_amount,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22) RETURNING created_at`, quote.ID, nullString(request.IdempotencyKey), quote.OrganizationID, quote.ProviderID, quote.ModelID, quote.Model, quote.EstimatedInputTokens, quote.EstimatedCachedInputTokens, quote.EstimatedOutputTokens, quote.PricingVersionID, quote.ProviderCostAmount, quote.RetailAmount, quote.PromotionAmount, quote.Currency, quote.ExchangeRate, quote.GrossMargin, quote.PreTaxAmount, quote.TaxRate, quote.TaxAmount, quote.FinalAmount, request.CreatedBy, quote.ExpiresAt).Scan(&quote.CreatedAt)
	if err != nil {
		return domain.PricingQuote{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.PricingQuote{}, err
	}
	return quote, nil
}

func isProviderAdmissionError(err error) bool {
	return errors.Is(err, ErrProviderCommercialUnavailable) || errors.Is(err, ErrProviderRegionDenied) ||
		errors.Is(err, ErrProviderPolicyDenied) || errors.Is(err, ErrProviderDataResidencyDenied) ||
		errors.Is(err, ErrProviderBudgetExceeded) || errors.Is(err, ErrProviderRateExceeded) ||
		errors.Is(err, ErrProviderMarginInsufficient)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func selectCostPrice(ctx context.Context, tx pgx.Tx, providerID, modelID string) (costPriceRow, error) {
	var v costPriceRow
	err := tx.QueryRow(ctx, `SELECT id,provider_id,model_id,input_token_cost::text,cached_input_token_cost::text,output_token_cost::text,request_fixed_cost::text,currency,unit,effective_at,expires_at,source,approval_status FROM provider_cost_price_book WHERE provider_id=$1 AND model_id=$2 AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now()) ORDER BY effective_at DESC,id DESC LIMIT 1 FOR SHARE`, providerID, modelID).Scan(&v.ID, &v.ProviderID, &v.ModelID, &v.Input, &v.Cached, &v.Output, &v.Fixed, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &v.Approval)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrPricingUnavailable
	}
	return v, err
}
func selectRetailPrice(ctx context.Context, tx pgx.Tx, orgID, providerID, modelID string) (retailPriceRow, error) {
	var v retailPriceRow
	queries := []struct {
		sql  string
		args []any
	}{
		{`SELECT id,organization_id,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,approval_status FROM organization_price_plan WHERE organization_id=$1 AND plan_type='ORGANIZATION_OVERRIDE' AND provider_id=$2 AND model_id=$3 AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now()) ORDER BY effective_at DESC,id DESC LIMIT 1 FOR SHARE`, []any{orgID, providerID, modelID}},
		{`SELECT id,organization_id,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,approval_status FROM organization_price_plan WHERE organization_id=$1 AND plan_type='SUBSCRIPTION' AND provider_id=$2 AND model_id=$3 AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now()) ORDER BY effective_at DESC,id DESC LIMIT 1 FOR SHARE`, []any{orgID, providerID, modelID}},
		{`SELECT id,organization_id,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,approval_status FROM customer_retail_price_book WHERE organization_id=$1 AND provider_id=$2 AND model_id=$3 AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now()) ORDER BY effective_at DESC,id DESC LIMIT 1 FOR SHARE`, []any{orgID, providerID, modelID}},
		{`SELECT id,organization_id,provider_id,model_id,input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,currency,unit,effective_at,expires_at,source,approval_status FROM customer_retail_price_book WHERE organization_id IS NULL AND provider_id=$1 AND model_id=$2 AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now()) ORDER BY effective_at DESC,id DESC LIMIT 1 FOR SHARE`, []any{providerID, modelID}},
	}
	for index, q := range queries {
		var approval string
		err := tx.QueryRow(ctx, q.sql, q.args...).Scan(&v.ID, &v.OrganizationID, &v.ProviderID, &v.ModelID, &v.Input, &v.Cached, &v.Output, &v.Fixed, &v.Currency, &v.Unit, &v.EffectiveAt, &v.ExpiresAt, &v.Source, &approval)
		if err == nil {
			if index == 0 || index == 1 {
				v.PlanID = &v.ID
				v.PlanType = map[int]string{0: "ORGANIZATION_OVERRIDE", 1: "SUBSCRIPTION"}[index]
			}
			return v, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return v, err
		}
	}
	return v, ErrPricingUnavailable
}

func (s *Store) ensureRetailMargin(ctx context.Context, organizationID *string, providerID, modelID string, effectiveAt time.Time, retail pricing.Rate, force bool, confirmation string) (bool, error) {
	var cost costPriceRow
	var err error
	err = s.pool.QueryRow(ctx, `SELECT id,provider_id,model_id,input_token_cost::text,cached_input_token_cost::text,output_token_cost::text,request_fixed_cost::text,currency,unit,effective_at,expires_at,source,approval_status FROM provider_cost_price_book WHERE provider_id=$1 AND model_id=$2 AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=$3 AND (expires_at IS NULL OR expires_at>$3) ORDER BY effective_at DESC,id DESC LIMIT 1`, providerID, modelID, effectiveAt).Scan(&cost.ID, &cost.ProviderID, &cost.ModelID, &cost.Input, &cost.Cached, &cost.Output, &cost.Fixed, &cost.Currency, &cost.Unit, &cost.EffectiveAt, &cost.ExpiresAt, &cost.Source, &cost.Approval)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrPricingUnavailable
	}
	if err != nil {
		return false, err
	}
	if !strings.EqualFold(cost.Currency, retail.Currency) {
		return false, ErrPricingCurrencyMismatch
	}
	var amount string
	var bps int64
	_ = s.pool.QueryRow(ctx, `SELECT minimum_margin_amount::text,minimum_margin_bps FROM pricing_margin_policies WHERE enabled
		AND (organization_id IS NULL OR organization_id=$3) AND (provider_id IS NULL OR provider_id=$2) AND (model_id IS NULL OR model_id=$1)
		ORDER BY ((model_id IS NOT NULL)::int+(organization_id IS NOT NULL)::int+(provider_id IS NOT NULL)::int) DESC,created_at DESC LIMIT 1`, modelID, providerID, organizationID).Scan(&amount, &bps)
	ok, _, err := pricing.MeetsMinimumMargin(pricing.Rate{Input: cost.Input, Cached: cost.Cached, Output: cost.Output, Fixed: cost.Fixed, Unit: cost.Unit, Currency: cost.Currency}, retail, amount, bps, "1")
	if err != nil {
		return false, err
	}
	if !ok && !force {
		return false, ErrNegativeMargin
	}
	if !ok && force && confirmation != "CONFIRM_NEGATIVE_MARGIN_OVERRIDE" {
		return false, ErrForceOverrideConfirmation
	}
	return !ok, nil
}

func validateCostEntry(v domain.ProviderCostPriceBook) error {
	if v.ProviderID == "" || v.ModelID == "" || v.Unit <= 0 || strings.TrimSpace(v.Currency) == "" || v.Source == "" {
		return errors.New("provider_id, model_id, currency, unit, and source are required")
	}
	if len(strings.TrimSpace(v.Currency)) != 3 {
		return errors.New("currency must be a three-letter code")
	}
	for _, amount := range []string{v.InputTokenCost, v.CachedInputTokenCost, v.OutputTokenCost, v.RequestFixedCost} {
		if err := pricing.ValidateStoredDecimal(zeroIfEmpty(amount)); err != nil {
			return err
		}
	}
	if v.ExpiresAt != nil && !v.EffectiveAt.IsZero() && !v.ExpiresAt.After(v.EffectiveAt) {
		return errors.New("expires_at must be after effective_at")
	}
	_, err := pricing.Calculate(pricing.Rate{Input: v.InputTokenCost, Cached: v.CachedInputTokenCost, Output: v.OutputTokenCost, Fixed: v.RequestFixedCost, Unit: v.Unit, Currency: v.Currency}, pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: v.Unit, Currency: v.Currency}, pricing.Tokens{}, "0", "0", "1")
	return err
}
func validateRetailEntry(v domain.CustomerRetailPriceBook) error {
	if v.ProviderID == "" || v.ModelID == "" || v.Unit <= 0 || strings.TrimSpace(v.Currency) == "" || v.Source == "" {
		return errors.New("provider_id, model_id, currency, unit, and source are required")
	}
	_, err := pricing.Calculate(pricing.Rate{Input: v.InputTokenPrice, Cached: v.CachedInputTokenPrice, Output: v.OutputTokenPrice, Fixed: v.RequestFixedPrice, Unit: v.Unit, Currency: v.Currency}, pricing.Rate{Input: "0", Cached: "0", Output: "0", Fixed: "0", Unit: v.Unit, Currency: v.Currency}, pricing.Tokens{}, "0", "0", "1")
	return err
}
func regionAllowed(raw []byte, region string) bool {
	var regions []string
	_ = jsonUnmarshal(raw, &regions)
	for _, candidate := range regions {
		if candidate == "*" || strings.EqualFold(candidate, region) {
			return true
		}
	}
	return false
}
func jsonUnmarshal(raw []byte, out *[]string) error { return json.Unmarshal(raw, out) }
func minTime(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.Before(*b) {
		return a
	}
	return b
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func zeroIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "0"
	}
	return value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func scanPricingQuote(row interface{ Scan(...any) error }, out *domain.PricingQuote) error {
	return row.Scan(&out.ID, &out.OrganizationID, &out.ProviderID, &out.ModelID, &out.Model, &out.EstimatedInputTokens, &out.EstimatedCachedInputTokens, &out.EstimatedOutputTokens, &out.PricingVersionID, &out.ProviderCostAmount, &out.RetailAmount, &out.PromotionAmount, &out.Currency, &out.ExchangeRate, &out.GrossMargin, &out.PreTaxAmount, &out.TaxRate, &out.TaxAmount, &out.FinalAmount, &out.ExpiresAt, &out.CreatedAt)
}

type settledPriceVersion struct {
	ID, ProviderID, ModelID                              string
	Version                                              int64
	CostInput, CostCached, CostOutput, CostFixed         string
	RetailInput, RetailCached, RetailOutput, RetailFixed string
	ProviderCurrency, RetailCurrency                     string
	CostUnit, RetailUnit                                 int64
}

func settlePricingUsage(ctx context.Context, tx pgx.Tx, logEntry domain.RequestLog, organizationID, projectID string) error {
	if logEntry.StatusCode >= 400 || strings.TrimSpace(logEntry.PricingVersionID) == "" {
		_, err := tx.Exec(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,api_key_id,provider_id,model,input_tokens,cached_input_tokens,output_tokens,amount,currency,status,promotion_amount,tax_amount,final_user_amount,pricing_rule_version)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,'USD','WAIVED',0,0,0,'unpriced-compat') ON CONFLICT(request_id) DO NOTHING`, id.UUID(), logEntry.RequestID, organizationID, projectID, nullString(logEntry.APIKeyID), nullString(logEntry.ProviderID), logEntry.ResolvedModel, logEntry.InputTokens, logEntry.CachedInputTokens, logEntry.OutputTokens)
		return err
	}
	var version settledPriceVersion
	err := tx.QueryRow(ctx, `SELECT id,provider_id,model_id,version,provider_input_token_cost::text,provider_cached_input_token_cost::text,provider_output_token_cost::text,provider_request_fixed_cost::text,retail_input_token_price::text,retail_cached_input_token_price::text,retail_output_token_price::text,retail_request_fixed_price::text,provider_currency,retail_currency,provider_unit,retail_unit FROM model_price_version WHERE id=$1 FOR SHARE`, logEntry.PricingVersionID).Scan(&version.ID, &version.ProviderID, &version.ModelID, &version.Version, &version.CostInput, &version.CostCached, &version.CostOutput, &version.CostFixed, &version.RetailInput, &version.RetailCached, &version.RetailOutput, &version.RetailFixed, &version.ProviderCurrency, &version.RetailCurrency, &version.CostUnit, &version.RetailUnit)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPricingUnavailable
	}
	if err != nil {
		return err
	}
	snapshotID := id.UUID()
	promotion := "0"
	if logEntry.FundingOperationID != "" {
		if err = tx.QueryRow(ctx, `SELECT consumed_promotion_amount::text FROM funding_operation WHERE id=$1`, logEntry.FundingOperationID).Scan(&promotion); err != nil {
			return err
		}
	} else if decimalPositive(zeroIfEmpty(logEntry.PromotionAmount)) {
		undiscounted, calcErr := pricing.Calculate(
			pricing.Rate{Input: version.CostInput, Cached: version.CostCached, Output: version.CostOutput, Fixed: version.CostFixed, Unit: version.CostUnit, Currency: version.ProviderCurrency},
			pricing.Rate{Input: version.RetailInput, Cached: version.RetailCached, Output: version.RetailOutput, Fixed: version.RetailFixed, Unit: version.RetailUnit, Currency: version.RetailCurrency},
			pricing.Tokens{Input: logEntry.InputTokens, Cached: logEntry.CachedInputTokens, Output: logEntry.OutputTokens}, "0", zeroIfEmpty(logEntry.TaxRate), oneIfEmpty(logEntry.ExchangeRate))
		if calcErr != nil {
			return calcErr
		}
		promotion, err = redeemPromotionCredits(ctx, tx, organizationID, version.RetailCurrency, decimalMin(logEntry.PromotionAmount, undiscounted.RetailAmount), logEntry.RequestID, snapshotID)
		if err != nil {
			return err
		}
	}
	result, err := pricing.Calculate(
		pricing.Rate{Input: version.CostInput, Cached: version.CostCached, Output: version.CostOutput, Fixed: version.CostFixed, Unit: version.CostUnit, Currency: version.ProviderCurrency},
		pricing.Rate{Input: version.RetailInput, Cached: version.RetailCached, Output: version.RetailOutput, Fixed: version.RetailFixed, Unit: version.RetailUnit, Currency: version.RetailCurrency},
		pricing.Tokens{Input: logEntry.InputTokens, Cached: logEntry.CachedInputTokens, Output: logEntry.OutputTokens}, promotion, zeroIfEmpty(logEntry.TaxRate), oneIfEmpty(logEntry.ExchangeRate))
	if err != nil {
		return err
	}
	ruleVersion := fmt.Sprintf("%s:%d", version.ID, version.Version)
	credentialOwner := domain.CredentialOwnerPlatform
	platformServiceFee := "0"
	providerCostAmount := result.ProviderCost
	customerSaleAmount := result.RetailAmount
	promotionAmount := result.PromotionAmount
	preTaxAmount := result.PreTaxAmount
	taxRate := result.TaxRate
	taxAmount := result.TaxAmount
	finalUserAmount := result.FinalAmount
	exchangeRate := result.ExchangeRate
	retailInput, retailCached, retailOutput, retailFixed, retailUnit := version.RetailInput, version.RetailCached, version.RetailOutput, version.RetailFixed, version.RetailUnit
	var byokServiceFeePolicyID *string
	if logEntry.FundingOperationID != "" {
		var byokInput, byokCached, byokOutput, byokFixed string
		var byokUnit int64
		if err = tx.QueryRow(ctx, `SELECT credential_owner,platform_service_fee::text,byok_service_fee_policy_id,
			byok_input_token_fee::text,byok_cached_input_token_fee::text,byok_output_token_fee::text,byok_fixed_fee::text,byok_fee_unit
			FROM funding_operation WHERE id=$1`, logEntry.FundingOperationID).
			Scan(&credentialOwner, &platformServiceFee, &byokServiceFeePolicyID, &byokInput, &byokCached, &byokOutput, &byokFixed, &byokUnit); err != nil {
			return err
		}
		if credentialOwner == domain.CredentialOwnerCustomer {
			providerCostAmount = "0"
			customerSaleAmount = platformServiceFee
			promotionAmount = "0"
			preTaxAmount = platformServiceFee
			taxRate, taxAmount = "0", "0"
			finalUserAmount = platformServiceFee
			exchangeRate = "1"
			retailInput, retailCached, retailOutput, retailFixed, retailUnit = byokInput, byokCached, byokOutput, byokFixed, byokUnit
			ruleVersion = "byok:" + valueOrEmpty(byokServiceFeePolicyID)
		}
	}
	marginRat, _ := new(big.Rat).SetString(customerSaleAmount)
	costRat, _ := new(big.Rat).SetString(providerCostAmount)
	marginRat.Sub(marginRat, costRat)
	_, err = tx.Exec(ctx, `INSERT INTO usage_price_snapshot(id,request_id,pricing_version_id,provider_id,model_id,input_tokens,cached_input_tokens,output_tokens,provider_input_token_cost,provider_cached_input_token_cost,provider_output_token_cost,provider_request_fixed_cost,retail_input_token_price,retail_cached_input_token_price,retail_output_token_price,retail_request_fixed_price,provider_unit,retail_unit,provider_cost_amount,provider_currency,customer_sale_amount,customer_currency,exchange_rate,platform_gross_margin,promotion_amount,pre_tax_amount,tax_rate,tax_amount,final_user_amount,pricing_rule_version,credential_owner,platform_service_fee,byok_service_fee_policy_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33)`, snapshotID, logEntry.RequestID, version.ID, version.ProviderID, version.ModelID, logEntry.InputTokens, logEntry.CachedInputTokens, logEntry.OutputTokens, version.CostInput, version.CostCached, version.CostOutput, version.CostFixed, retailInput, retailCached, retailOutput, retailFixed, version.CostUnit, retailUnit, providerCostAmount, version.ProviderCurrency, customerSaleAmount, version.RetailCurrency, exchangeRate, formatRat(marginRat), promotionAmount, preTaxAmount, taxRate, taxAmount, finalUserAmount, ruleVersion, credentialOwner, platformServiceFee, byokServiceFeePolicyID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE request_logs SET pricing_version_id=$2,usage_price_snapshot_id=$3 WHERE request_id=$1`, logEntry.RequestID, version.ID, snapshotID); err != nil {
		return err
	}
	status := "WAIVED"
	if decimalPositive(finalUserAmount) {
		status = "RECORDED"
	}
	usageID := id.UUID()
	err = tx.QueryRow(ctx, `INSERT INTO billing_usage_records(id,request_id,organization_id,project_id,api_key_id,provider_id,model,input_tokens,cached_input_tokens,output_tokens,amount,currency,status,usage_price_snapshot_id,provider_cost_amount,customer_sale_amount,promotion_amount,tax_amount,final_user_amount,pricing_rule_version,funding_operation_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21) ON CONFLICT(request_id) DO NOTHING RETURNING id`, usageID, logEntry.RequestID, organizationID, projectID, nullString(logEntry.APIKeyID), version.ProviderID, logEntry.ResolvedModel, logEntry.InputTokens, logEntry.CachedInputTokens, logEntry.OutputTokens, finalUserAmount, version.RetailCurrency, status, snapshotID, providerCostAmount, customerSaleAmount, promotionAmount, taxAmount, finalUserAmount, ruleVersion, nullString(logEntry.FundingOperationID)).Scan(&usageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || status == "WAIVED" {
		return err
	}
	if logEntry.FundingOperationID != "" {
		_, err = tx.Exec(ctx, `UPDATE billing_usage_records SET status=CASE
			WHEN EXISTS(SELECT 1 FROM funding_operation WHERE id=$2 AND status IN ('SETTLED','PARTIALLY_SETTLED')) THEN 'CHARGED'
			WHEN EXISTS(SELECT 1 FROM funding_operation WHERE id=$2 AND status IN ('RELEASED','FAILED','REVERSED')) THEN 'WAIVED'
			ELSE status END WHERE id=$1`, usageID, logEntry.FundingOperationID)
		return err
	}
	var walletID, walletCurrency string
	err = tx.QueryRow(ctx, `SELECT id,currency FROM wallets WHERE organization_id=$1 AND status='ACTIVE' FOR UPDATE`, organizationID).Scan(&walletID, &walletCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if walletCurrency != version.RetailCurrency {
		return fmt.Errorf("wallet currency %s does not match retail currency %s", walletCurrency, version.RetailCurrency)
	}
	var balanceAfter string
	journalID, err := postJournalTx(ctx, tx, walletID, "SETTLEMENT", "legacy-usage:"+logEntry.RequestID,
		version.RetailCurrency, logEntry.RequestID, map[string]any{"request_id": logEntry.RequestID, "pricing_version_id": version.ID, "compatibility_path": true}, nil, []journalLine{
			{walletAccountKey(walletID, "available"), "DEBIT", result.FinalAmount, "Settle legacy usage charge"},
			{systemAccountKey("revenue", version.RetailCurrency), "CREDIT", result.FinalAmount, "Recognize usage revenue"},
		})
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `UPDATE wallets SET available_balance=available_balance-$2::numeric,version=version+1,updated_at=now() WHERE id=$1 RETURNING available_balance::text`, walletID, result.FinalAmount).Scan(&balanceAfter)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wallet_transactions(id,wallet_id,usage_record_id,transaction_type,amount,balance_after,idempotency_key,reference,metadata,journal_id) VALUES($1,$2,$3,'CHARGE',-$4::numeric,$5,$6,$6,$7,$8) ON CONFLICT(wallet_id,idempotency_key) DO NOTHING`, id.UUID(), walletID, usageID, result.FinalAmount, balanceAfter, "usage:"+logEntry.RequestID, jsonBytes(map[string]any{"request_id": logEntry.RequestID, "model": logEntry.ResolvedModel, "pricing_version_id": version.ID, "usage_price_snapshot_id": snapshotID}), journalID)
	if err != nil {
		return err
	}
	var actor *string
	if logEntry.UserID != "" {
		actor = &logEntry.UserID
	}
	if err = writeAuditTx(ctx, tx, actor, "wallet.usage_charge", "wallet", walletID, map[string]any{"request_id": logEntry.RequestID, "amount": result.FinalAmount, "currency": version.RetailCurrency, "pricing_version_id": version.ID, "usage_price_snapshot_id": snapshotID, "balance_after": balanceAfter}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE billing_usage_records SET status='CHARGED' WHERE id=$1`, usageID)
	return err
}

func redeemPromotionCredits(ctx context.Context, tx pgx.Tx, organizationID, currency, requested, requestID, snapshotID string) (string, error) {
	remaining, ok := new(big.Rat).SetString(zeroIfEmpty(requested))
	if !ok || remaining.Sign() < 0 {
		return "", pricing.ErrInvalidAmount
	}
	used := new(big.Rat)
	rows, err := tx.Query(ctx, `SELECT id,amount_remaining::text FROM promotion_credit WHERE organization_id=$1 AND currency=$2 AND status='ACTIVE' AND amount_remaining>0 AND (expires_at IS NULL OR expires_at>now()) ORDER BY expires_at NULLS LAST,created_at,id FOR UPDATE`, organizationID, currency)
	if err != nil {
		return "", err
	}
	type credit struct{ id, amount string }
	credits := make([]credit, 0)
	for rows.Next() {
		var v credit
		if err = rows.Scan(&v.id, &v.amount); err != nil {
			rows.Close()
			return "", err
		}
		credits = append(credits, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	for _, credit := range credits {
		if remaining.Sign() <= 0 {
			break
		}
		available, _ := new(big.Rat).SetString(credit.amount)
		take := new(big.Rat).Set(available)
		if take.Cmp(remaining) > 0 {
			take.Set(remaining)
		}
		remaining.Sub(remaining, take)
		used.Add(used, take)
		takeText := formatRat(take)
		_, err = tx.Exec(ctx, `UPDATE promotion_credit SET amount_remaining=amount_remaining-$2::numeric,status=CASE WHEN amount_remaining-$2::numeric=0 THEN 'EXHAUSTED' ELSE status END,updated_at=now() WHERE id=$1`, credit.id, takeText)
		if err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, `INSERT INTO promotion_credit_redemptions(id,promotion_credit_id,usage_snapshot_id,request_id,amount) VALUES($1,$2,$3,$4,$5)`, id.UUID(), credit.id, snapshotID, requestID, takeText)
		if err != nil {
			return "", err
		}
	}
	return formatRat(used), nil
}

func decimalPositive(value string) bool {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	return ok && r.Sign() > 0
}
func decimalEqual(a, b string) bool {
	left, ok := new(big.Rat).SetString(zeroIfEmpty(a))
	if !ok {
		return false
	}
	right, ok := new(big.Rat).SetString(zeroIfEmpty(b))
	return ok && left.Cmp(right) == 0
}
func decimalMin(a, b string) string {
	left, ok := new(big.Rat).SetString(zeroIfEmpty(a))
	if !ok {
		return "0"
	}
	right, ok := new(big.Rat).SetString(zeroIfEmpty(b))
	if !ok {
		return "0"
	}
	if left.Cmp(right) < 0 {
		return formatRat(left)
	}
	return formatRat(right)
}
func oneIfEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "1"
	}
	return value
}
func formatRat(r *big.Rat) string {
	if r == nil {
		return "0"
	}
	value := r.FloatString(12)
	value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	if value == "" || value == "-0" {
		return "0"
	}
	return value
}
