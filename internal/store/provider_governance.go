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
	"github.com/relayedock/relayedock/internal/pricing"
)

var (
	ErrProviderCommercialUnavailable = errors.New("provider is not commercially available")
	ErrProviderRegionDenied          = errors.New("provider is unavailable for the customer region")
	ErrProviderPolicyDenied          = errors.New("organization provider policy denied the route")
	ErrProviderDataResidencyDenied   = errors.New("provider cannot satisfy data residency policy")
	ErrProviderBudgetExceeded        = errors.New("provider commercial budget is exhausted")
	ErrProviderRateExceeded          = errors.New("provider commercial rate limit is exhausted")
	ErrProviderMarginInsufficient    = errors.New("provider route does not meet the current margin policy")
	ErrBYOKOrganizationMismatch      = errors.New("customer credential belongs to another organization")
)

type ProviderAdmission struct {
	ProviderID         string
	ModelID            string
	CustomerRegion     string
	RateLimit          *int
	CostLimit          string
	CurrentCost        string
	ReservedCost       string
	SettlementCurrency string
}

type providerAdmissionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// CheckProviderAdmission is the authoritative pre-route commercial gate. It
// intentionally does not use technical health as evidence of authorization.
func (s *Store) CheckProviderAdmission(ctx context.Context, organizationID, userID, providerID, model string) (ProviderAdmission, error) {
	return checkProviderAdmission(ctx, s.pool, organizationID, userID, providerID, model)
}

func checkProviderAdmission(ctx context.Context, q providerAdmissionQuerier, organizationID, userID, providerID, model string) (ProviderAdmission, error) {
	var result ProviderAdmission
	var enabled, pricingDisabled, killSwitch, qualityPolicyEnabled, marketplaceAllowed bool
	var qualityCircuitState string
	var commercialStatus, resaleStatus, billingRegion, userRegion, settlementCurrency, costLimit, currentCost, reservedCost string
	var contractStart, contractEnd *time.Time
	var allowedCustomer, prohibited, processing, allowedProviders, prohibitedProviders, requiredData, modelRegions []byte
	var rateLimit *int
	err := q.QueryRow(ctx, `SELECT p.id,m.id,p.enabled,p.pricing_disabled,p.emergency_kill_switch,p.commercial_status,
		p.commercial_resale_status,p.contract_start_at,p.contract_end_at,p.allowed_customer_regions,p.prohibited_regions,
		p.data_processing_regions,COALESCE(p.cost_limit,0)::text,p.rate_limit,p.settlement_currency,o.billing_region,
		COALESCE((SELECT u.region FROM users u WHERE u.id=NULLIF($2,'')::uuid),''),o.allowed_provider_ids,o.prohibited_provider_ids,o.required_data_regions,m.allowed_regions,
		COALESCE(b.provider_cost,0)::text,COALESCE(b.reserved_cost,0)::text,
		COALESCE(qp.enabled,false),COALESCE(qs.circuit_state,'CLOSED'),
		NOT EXISTS(SELECT 1 FROM supplier_provider_links any_link WHERE any_link.provider_id=p.id AND any_link.status<>'ENDED')
		OR EXISTS(SELECT 1 FROM supplier_provider_links link
			JOIN provider_marketplace_listings listing ON listing.provider_id=link.provider_id
			JOIN marketplace_launch_review review ON review.listing_id=listing.id AND review.provider_id=link.provider_id AND review.supplier_id=link.supplier_id
			WHERE link.provider_id=p.id AND link.status='ACTIVE' AND (
				(listing.status='ACTIVE' AND review.status='APPROVED' AND NOT EXISTS(
					SELECT 1 FROM marketplace_launch_gate gate WHERE gate.review_id=review.id AND gate.status<>'PASSED'))
				OR (listing.status='CANARY' AND review.status='IN_REVIEW' AND NOT EXISTS(
					SELECT 1 FROM marketplace_launch_gate gate WHERE gate.review_id=review.id
					AND gate.gate_code IN ('SUPPLIER_REGISTRATION','QUALIFICATION_REVIEW','ENDPOINT_VERIFICATION','MODEL_PUBLICATION','PRICE_APPROVAL','HEALTH_TEST')
					AND gate.status<>'PASSED'))))
		FROM providers p JOIN organizations o ON o.id=$1
		JOIN models m ON m.provider_id=p.id AND (m.id::text=$4 OR m.provider_model_id=$4) AND m.enabled=true
		LEFT JOIN provider_usage_budget b ON b.provider_id=p.id AND b.period_month=date_trunc('month',now())::date
		LEFT JOIN provider_quality_policies qp ON qp.provider_id=p.id
		LEFT JOIN provider_quality_states qs ON qs.provider_id=p.id
		WHERE p.id=$3`, organizationID, userID, providerID, model).Scan(&result.ProviderID, &result.ModelID, &enabled,
		&pricingDisabled, &killSwitch, &commercialStatus, &resaleStatus, &contractStart, &contractEnd, &allowedCustomer,
		&prohibited, &processing, &costLimit, &rateLimit, &settlementCurrency, &billingRegion, &userRegion,
		&allowedProviders, &prohibitedProviders, &requiredData, &modelRegions, &currentCost, &reservedCost,
		&qualityPolicyEnabled, &qualityCircuitState, &marketplaceAllowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	if !enabled || pricingDisabled || killSwitch || commercialStatus != domain.ProviderStatusCommercialApproved ||
		resaleStatus != "APPROVED" || !marketplaceAllowed || (contractStart != nil && now.Before(*contractStart)) || (contractEnd != nil && !now.Before(*contractEnd)) {
		return result, ErrProviderCommercialUnavailable
	}
	if qualityPolicyEnabled && qualityCircuitState != "CLOSED" {
		return result, ErrProviderQualityCircuitOpen
	}
	region := strings.ToUpper(strings.TrimSpace(userRegion))
	if region == "" {
		region = strings.ToUpper(strings.TrimSpace(billingRegion))
	}
	if !jsonStringSetAllows(allowedCustomer, region) || jsonStringSetContains(prohibited, region) || !jsonStringSetAllows(modelRegions, region) {
		return result, ErrProviderRegionDenied
	}
	if (jsonStringSetNonEmpty(allowedProviders) && !jsonStringSetContains(allowedProviders, providerID)) || jsonStringSetContains(prohibitedProviders, providerID) {
		return result, ErrProviderPolicyDenied
	}
	for _, required := range decodeStringSet(requiredData) {
		if !jsonStringSetAllows(processing, required) {
			return result, ErrProviderDataResidencyDenied
		}
	}
	if allowed, marginErr := providerCurrentMarginAllowed(ctx, q, organizationID, providerID, result.ModelID); marginErr != nil {
		return result, marginErr
	} else if !allowed {
		return result, ErrProviderMarginInsufficient
	}
	if limit, ok := new(big.Rat).SetString(costLimit); ok && limit.Sign() > 0 {
		used, usedOK := new(big.Rat).SetString(currentCost)
		reserved, reservedOK := new(big.Rat).SetString(reservedCost)
		if !usedOK || !reservedOK || new(big.Rat).Add(used, reserved).Cmp(limit) >= 0 {
			return result, ErrProviderBudgetExceeded
		}
	}
	result.CustomerRegion, result.RateLimit, result.CostLimit = region, rateLimit, costLimit
	result.CurrentCost, result.ReservedCost, result.SettlementCurrency = currentCost, reservedCost, settlementCurrency
	return result, nil
}

func providerCurrentMarginAllowed(ctx context.Context, q providerAdmissionQuerier, organizationID, providerID, modelID string) (bool, error) {
	var cost pricing.Rate
	err := q.QueryRow(ctx, `SELECT input_token_cost::text,cached_input_token_cost::text,output_token_cost::text,
		request_fixed_cost::text,unit,currency FROM provider_cost_price_book WHERE provider_id=$1 AND model_id=$2
		AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
		ORDER BY effective_at DESC,id DESC LIMIT 1`, providerID, modelID).
		Scan(&cost.Input, &cost.Cached, &cost.Output, &cost.Fixed, &cost.Unit, &cost.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var retail pricing.Rate
	retailQueries := []struct {
		sql  string
		args []any
	}{
		{`SELECT input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,unit,currency
			FROM organization_price_plan WHERE organization_id=$1 AND plan_type='ORGANIZATION_OVERRIDE' AND provider_id=$2 AND model_id=$3
			AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
			ORDER BY effective_at DESC,id DESC LIMIT 1`, []any{organizationID, providerID, modelID}},
		{`SELECT input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,unit,currency
			FROM organization_price_plan WHERE organization_id=$1 AND plan_type='SUBSCRIPTION' AND provider_id=$2 AND model_id=$3
			AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
			ORDER BY effective_at DESC,id DESC LIMIT 1`, []any{organizationID, providerID, modelID}},
		{`SELECT input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,unit,currency
			FROM customer_retail_price_book WHERE organization_id=$1 AND provider_id=$2 AND model_id=$3
			AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
			ORDER BY effective_at DESC,id DESC LIMIT 1`, []any{organizationID, providerID, modelID}},
		{`SELECT input_token_price::text,cached_input_token_price::text,output_token_price::text,request_fixed_price::text,unit,currency
			FROM customer_retail_price_book WHERE organization_id IS NULL AND provider_id=$1 AND model_id=$2
			AND approval_status IN ('APPROVED','FORCED_APPROVED') AND effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
			ORDER BY effective_at DESC,id DESC LIMIT 1`, []any{providerID, modelID}},
	}
	retailFound := false
	for _, query := range retailQueries {
		err = q.QueryRow(ctx, query.sql, query.args...).Scan(&retail.Input, &retail.Cached, &retail.Output, &retail.Fixed, &retail.Unit, &retail.Currency)
		if err == nil {
			retailFound = true
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	if !retailFound || !strings.EqualFold(cost.Currency, retail.Currency) {
		return false, nil
	}
	var organizationMinimum string
	if err = q.QueryRow(ctx, `SELECT minimum_gross_margin::text FROM organizations WHERE id=$1`, organizationID).Scan(&organizationMinimum); err != nil {
		return false, err
	}
	policyMinimum, minimumBPS := "0", int64(0)
	err = q.QueryRow(ctx, `SELECT minimum_margin_amount::text,minimum_margin_bps FROM pricing_margin_policies WHERE enabled
		AND (organization_id IS NULL OR organization_id=$1) AND (provider_id IS NULL OR provider_id=$2) AND (model_id IS NULL OR model_id=$3)
		ORDER BY ((model_id IS NOT NULL)::int+(organization_id IS NOT NULL)::int+(provider_id IS NOT NULL)::int) DESC,created_at DESC LIMIT 1`,
		organizationID, providerID, modelID).Scan(&policyMinimum, &minimumBPS)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	minimum := maxDecimal(organizationMinimum, policyMinimum)
	allowed, _, err := pricing.MeetsMinimumMargin(cost, retail, minimum, minimumBPS, "1")
	return allowed, err
}

func maxDecimal(left, right string) string {
	l, leftOK := new(big.Rat).SetString(zeroIfEmpty(left))
	r, rightOK := new(big.Rat).SetString(zeroIfEmpty(right))
	if !leftOK || !rightOK {
		return "invalid"
	}
	if l.Cmp(r) >= 0 {
		return zeroIfEmpty(left)
	}
	return zeroIfEmpty(right)
}

// RecheckProviderDispatch closes the route-selection/dispatch window. It is
// called in the same transaction that records a provider attempt.
func recheckProviderDispatchTx(ctx context.Context, tx pgx.Tx, organizationID, providerID, credentialID string) error {
	var allowed, qualityAllowed, marketplaceAllowed bool
	err := tx.QueryRow(ctx, `SELECT p.enabled AND NOT p.pricing_disabled AND NOT p.emergency_kill_switch
		AND p.commercial_status='COMMERCIAL_APPROVED' AND p.commercial_resale_status='APPROVED'
		AND (p.contract_start_at IS NULL OR p.contract_start_at<=now())
		AND (p.contract_end_at IS NULL OR p.contract_end_at>now())
		AND (c.credential_owner='PLATFORM' OR c.owner_organization_id=$1),
		NOT COALESCE(qp.enabled,false) OR COALESCE(qs.circuit_state,'CLOSED')='CLOSED',
		NOT EXISTS(SELECT 1 FROM supplier_provider_links any_link WHERE any_link.provider_id=p.id AND any_link.status<>'ENDED')
		OR EXISTS(SELECT 1 FROM supplier_provider_links link
			JOIN provider_marketplace_listings listing ON listing.provider_id=link.provider_id
			JOIN marketplace_launch_review review ON review.listing_id=listing.id AND review.provider_id=link.provider_id AND review.supplier_id=link.supplier_id
			WHERE link.provider_id=p.id AND link.status='ACTIVE' AND (
				(listing.status='ACTIVE' AND review.status='APPROVED' AND NOT EXISTS(
					SELECT 1 FROM marketplace_launch_gate gate WHERE gate.review_id=review.id AND gate.status<>'PASSED'))
				OR (listing.status='CANARY' AND review.status='IN_REVIEW' AND NOT EXISTS(
					SELECT 1 FROM marketplace_launch_gate gate WHERE gate.review_id=review.id
					AND gate.gate_code IN ('SUPPLIER_REGISTRATION','QUALIFICATION_REVIEW','ENDPOINT_VERIFICATION','MODEL_PUBLICATION','PRICE_APPROVAL','HEALTH_TEST')
					AND gate.status<>'PASSED'))))
		FROM providers p JOIN provider_credentials c ON c.provider_id=p.id
		LEFT JOIN provider_quality_policies qp ON qp.provider_id=p.id
		LEFT JOIN provider_quality_states qs ON qs.provider_id=p.id
		WHERE p.id=$2 AND c.id=$3 FOR SHARE OF p,c`, organizationID, providerID, credentialID).Scan(&allowed, &qualityAllowed, &marketplaceAllowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrProviderCommercialUnavailable
	}
	if err != nil {
		return err
	}
	if !allowed || !marketplaceAllowed {
		return ErrProviderCommercialUnavailable
	}
	if !qualityAllowed {
		return ErrProviderQualityCircuitOpen
	}
	return nil
}

func decodeStringSet(raw []byte) []string {
	var values []string
	_ = json.Unmarshal(raw, &values)
	return values
}

func jsonStringSetNonEmpty(raw []byte) bool { return len(decodeStringSet(raw)) > 0 }

func jsonStringSetContains(raw []byte, value string) bool {
	for _, candidate := range decodeStringSet(raw) {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func jsonStringSetAllows(raw []byte, value string) bool {
	return jsonStringSetContains(raw, "*") || (strings.TrimSpace(value) != "" && jsonStringSetContains(raw, value))
}

func (s *Store) ProviderCommerciallyAvailableForModel(ctx context.Context, organizationID, userID, providerID, model string) bool {
	_, err := s.CheckProviderAdmission(ctx, organizationID, userID, providerID, model)
	return err == nil
}

func (s *Store) SettleProviderBudgetReservation(ctx context.Context, operationID, actualCost string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var providerID, period, reserved, status string
	err = tx.QueryRow(ctx, `SELECT provider_id,period_month::text,maximum_cost::text,status FROM provider_budget_reservations WHERE operation_id=$1 FOR UPDATE`, operationID).Scan(&providerID, &period, &reserved, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "RESERVED" {
		return tx.Commit(ctx)
	}
	actual, ok := new(big.Rat).SetString(actualCost)
	if !ok || actual.Sign() < 0 {
		return fmt.Errorf("invalid provider cost")
	}
	maximum, maximumOK := new(big.Rat).SetString(reserved)
	if !maximumOK || actual.Cmp(maximum) > 0 {
		return ErrProviderBudgetExceeded
	}
	newStatus := "RELEASED"
	if actual.Sign() > 0 {
		newStatus = "SETTLED"
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_usage_budget SET reserved_cost=GREATEST(0,reserved_cost-$3::numeric),provider_cost=provider_cost+$4::numeric,updated_at=now() WHERE provider_id=$1 AND period_month=$2::date`, providerID, period, reserved, actualCost); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE provider_budget_reservations SET settled_cost=$2,status=$3,settled_at=now() WHERE operation_id=$1`, operationID, actualCost, newStatus); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
