package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/relayedock/relayedock/internal/id"
)

const (
	FunnelHomepageVisited   = "HOMEPAGE_VISITED"
	FunnelRegistered        = "REGISTERED"
	FunnelEmailVerified     = "EMAIL_VERIFIED"
	FunnelAPIKeyCreated     = "API_KEY_CREATED"
	FunnelFirstRecharge     = "FIRST_RECHARGE"
	FunnelFirstAPICall      = "FIRST_API_CALL"
	FunnelSecondAPICall     = "SECOND_API_CALL"
	FunnelFirstSubscription = "FIRST_SUBSCRIPTION"
)

var FunnelOrder = []string{
	FunnelHomepageVisited,
	FunnelRegistered,
	FunnelEmailVerified,
	FunnelAPIKeyCreated,
	FunnelFirstRecharge,
	FunnelFirstAPICall,
	FunnelSecondAPICall,
	FunnelFirstSubscription,
}

type PublicAvailability struct {
	Available  bool   `json:"available"`
	Region     string `json:"region"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

type PublicProvider struct {
	ID                      string             `json:"id"`
	Name                    string             `json:"name"`
	Slug                    string             `json:"slug"`
	ProviderType            string             `json:"provider_type"`
	Enabled                 bool               `json:"enabled"`
	CommercialStatus        string             `json:"commercial_status"`
	CommercialResaleStatus  string             `json:"commercial_resale_status"`
	LegalEntity             string             `json:"legal_entity,omitempty"`
	AllowedCustomerRegions  []string           `json:"allowed_customer_regions"`
	ProhibitedRegions       []string           `json:"prohibited_regions"`
	DataProcessingRegions   []string           `json:"data_processing_regions"`
	DataRetentionPolicy     string             `json:"data_retention_policy,omitempty"`
	TermsVersion            string             `json:"terms_version,omitempty"`
	PricingDisabled         bool               `json:"pricing_disabled"`
	EmergencyKillSwitch     bool               `json:"emergency_kill_switch"`
	TechnicalStatus         string             `json:"technical_status"`
	PublishedUptime         string             `json:"published_uptime,omitempty"`
	DeclaredUptime          string             `json:"declared_uptime,omitempty"`
	QualityGrade            string             `json:"quality_grade"`
	QualitySource           string             `json:"quality_source"`
	QualityMeasurementCount int64              `json:"quality_measurement_count"`
	EnabledModelCount       int64              `json:"enabled_model_count"`
	Availability            PublicAvailability `json:"availability"`
	UpdatedAt               time.Time          `json:"updated_at"`

	contractStartAt       *time.Time
	contractEndAt         *time.Time
	listingActive         bool
	qualityEnabled        bool
	qualityMinimumSamples int64
	qualityCircuit        string
}

type PublicTokenPrice struct {
	PriceBookID           string             `json:"price_book_id"`
	ProviderID            string             `json:"provider_id"`
	ProviderName          string             `json:"provider_name"`
	ProviderSlug          string             `json:"provider_slug"`
	ModelID               string             `json:"model_id"`
	ModelName             string             `json:"model_name"`
	ProviderModelID       string             `json:"provider_model_id"`
	InputTokenPrice       string             `json:"input_token_price"`
	CachedInputTokenPrice string             `json:"cached_input_token_price"`
	OutputTokenPrice      string             `json:"output_token_price"`
	RequestFixedPrice     string             `json:"request_fixed_price"`
	Currency              string             `json:"currency"`
	Unit                  int64              `json:"unit"`
	EffectiveAt           time.Time          `json:"effective_at"`
	ExpiresAt             *time.Time         `json:"expires_at,omitempty"`
	Availability          PublicAvailability `json:"availability"`
}

type PublicModel struct {
	ID                              string             `json:"id"`
	ProviderID                      string             `json:"provider_id"`
	ProviderName                    string             `json:"provider_name"`
	ProviderSlug                    string             `json:"provider_slug"`
	ProviderModelID                 string             `json:"provider_model_id"`
	DisplayName                     string             `json:"display_name"`
	ModelType                       string             `json:"model_type"`
	Capabilities                    []string           `json:"capabilities"`
	ContextWindow                   *int               `json:"context_window,omitempty"`
	AllowedRegions                  []string           `json:"allowed_regions"`
	ServiceSubject                  string             `json:"service_subject,omitempty"`
	FilingInfo                      string             `json:"filing_info,omitempty"`
	GeneratedContentLabelCapability string             `json:"generated_content_label_capability"`
	UserDisclosure                  string             `json:"user_disclosure,omitempty"`
	Availability                    PublicAvailability `json:"availability"`
	Pricing                         *PublicTokenPrice  `json:"pricing"`
	UpdatedAt                       time.Time          `json:"updated_at"`
}

type PublicSubscriptionPlan struct {
	PlanID             string         `json:"plan_id"`
	Slug               string         `json:"slug"`
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	PlanKind           string         `json:"plan_kind"`
	PlanVersionID      string         `json:"plan_version_id"`
	Version            int            `json:"version"`
	SubscriptionFee    string         `json:"subscription_fee"`
	Currency           string         `json:"currency"`
	BillingInterval    string         `json:"billing_interval"`
	TrialDays          int            `json:"trial_days"`
	TokenBillingMode   string         `json:"token_billing_mode"`
	EnterpriseContract bool           `json:"enterprise_contract"`
	ContactSales       bool           `json:"contact_sales"`
	Entitlements       map[string]any `json:"entitlements"`
	EffectiveAt        time.Time      `json:"effective_at"`
}

type PublicCommercialTerms struct {
	ID                      string     `json:"id"`
	Region                  string     `json:"region"`
	Currency                string     `json:"currency"`
	SubscriptionTaxIncluded *bool      `json:"subscription_tax_included"`
	TokenTaxIncluded        *bool      `json:"token_tax_included"`
	TaxDisclosure           string     `json:"tax_disclosure"`
	RefundSummary           string     `json:"refund_summary"`
	RefundPolicyURL         string     `json:"refund_policy_url"`
	BonusCreditAmount       string     `json:"bonus_credit_amount"`
	BonusNonRefundable      bool       `json:"bonus_non_refundable"`
	LegalReviewRequired     bool       `json:"legal_review_required"`
	LegalReviewStatus       string     `json:"legal_review_status"`
	EffectiveAt             time.Time  `json:"effective_at"`
	ExpiresAt               *time.Time `json:"expires_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
}

type PublicPaymentFee struct {
	ID                  string     `json:"id"`
	FeeCategory         string     `json:"fee_category"`
	PaymentProvider     string     `json:"payment_provider"`
	Region              string     `json:"region"`
	Currency            string     `json:"currency"`
	FeeKind             string     `json:"fee_kind"`
	FixedAmount         string     `json:"fixed_amount"`
	RateBPS             int        `json:"rate_bps"`
	ChargedToCustomer   bool       `json:"charged_to_customer"`
	Description         string     `json:"description"`
	LegalReviewRequired bool       `json:"legal_review_required"`
	LegalReviewStatus   string     `json:"legal_review_status"`
	EffectiveAt         time.Time  `json:"effective_at"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type PublishCommercialTermsRequest struct {
	Region                  string
	Currency                string
	SubscriptionTaxIncluded *bool
	TokenTaxIncluded        *bool
	TaxDisclosure           string
	RefundSummary           string
	RefundPolicyURL         string
	BonusCreditAmount       string
	BonusNonRefundable      bool
	EffectiveAt             time.Time
	ExpiresAt               *time.Time
	LegalReviewStatus       string
	ReviewedAt              *time.Time
	IdempotencyKey          string
	CreatedBy               string
}

type PublishPaymentFeeRequest struct {
	FeeCategory       string
	PaymentProvider   string
	Region            string
	Currency          string
	FeeKind           string
	FixedAmount       string
	RateBPS           int
	ChargedToCustomer bool
	Description       string
	EffectiveAt       time.Time
	ExpiresAt         *time.Time
	LegalReviewStatus string
	ReviewedAt        *time.Time
	IdempotencyKey    string
	CreatedBy         string
}

type PublicPricingCatalog struct {
	Region                 string                   `json:"region"`
	Currency               string                   `json:"currency"`
	SubscriptionPlans      []PublicSubscriptionPlan `json:"subscription_plans"`
	TokenPrices            []PublicTokenPrice       `json:"token_prices"`
	PaymentFees            []PublicPaymentFee       `json:"payment_fees"`
	CommercialTerms        *PublicCommercialTerms   `json:"commercial_terms"`
	TermsConfigured        bool                     `json:"terms_configured"`
	PaymentFeesConfigured  bool                     `json:"payment_fees_configured"`
	PaymentRegionSupported bool                     `json:"payment_region_supported"`
	UpdatedAt              time.Time                `json:"updated_at"`
}

type PublicCatalogDimensions struct {
	Regions    []string `json:"supported_regions"`
	Currencies []string `json:"supported_currencies"`
}

type FunnelEvent struct {
	ID                 string         `json:"id"`
	EventType          string         `json:"event_type"`
	UserID             *string        `json:"user_id,omitempty"`
	OrganizationID     *string        `json:"organization_id,omitempty"`
	SourceResourceType string         `json:"source_resource_type"`
	SourceResourceID   string         `json:"source_resource_id"`
	IdempotencyKey     string         `json:"-"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	OccurredAt         time.Time      `json:"occurred_at"`
	CreatedAt          time.Time      `json:"created_at"`
}

type FunnelSummary struct {
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Counts map[string]int64 `json:"counts"`
}

type OnboardingStep struct {
	Key         string     `json:"key"`
	Completed   bool       `json:"completed"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ResourceID  string     `json:"resource_id,omitempty"`
	Required    bool       `json:"required"`
}

type OnboardingStatus struct {
	UserID         string           `json:"user_id"`
	OrganizationID string           `json:"organization_id,omitempty"`
	ProjectID      string           `json:"project_id,omitempty"`
	Steps          []OnboardingStep `json:"steps"`
	NextStep       string           `json:"next_step,omitempty"`
	Complete       bool             `json:"complete"`
	Milestones     map[string]bool  `json:"milestones"`
}

func (s *Store) ListPublicProviders(ctx context.Context, region string) ([]PublicProvider, error) {
	region = normalizePublicRegion(region)
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.name,p.slug,p.provider_type,p.enabled,p.commercial_status,
		p.commercial_resale_status,p.legal_entity,p.allowed_customer_regions,p.prohibited_regions,
		p.data_processing_regions,p.data_retention_policy,p.terms_version,p.pricing_disabled,
		p.emergency_kill_switch,p.contract_start_at,p.contract_end_at,p.updated_at,
		COALESCE(listing.active,false),COALESCE(listing.uptime,''),
		COALESCE(qp.enabled,false),COALESCE(qp.minimum_samples,0),COALESCE(qs.grade,'UNKNOWN'),
		COALESCE(qs.availability_pct::text,''),COALESCE(qs.measurement_count,0),COALESCE(qs.circuit_state,'CLOSED'),
		(SELECT count(*) FROM models m WHERE m.provider_id=p.id AND m.enabled)
		FROM providers p
		LEFT JOIN provider_quality_policies qp ON qp.provider_id=p.id
		LEFT JOIN provider_quality_states qs ON qs.provider_id=p.id
		LEFT JOIN LATERAL (
			SELECT bool_or(l.status='ACTIVE' AND l.verified) active,
				CASE WHEN count(*)=0 THEN '' ELSE avg(l.uptime)::text END uptime
			FROM provider_marketplace_listings l WHERE l.provider_id=p.id
		) listing ON true
		WHERE EXISTS (SELECT 1 FROM models published_model WHERE published_model.provider_id=p.id AND published_model.enabled)
			OR COALESCE(listing.active,false)
		ORDER BY p.name,p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicProvider, 0)
	for rows.Next() {
		var item PublicProvider
		var allowed, prohibited, processing []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.Slug, &item.ProviderType, &item.Enabled,
			&item.CommercialStatus, &item.CommercialResaleStatus, &item.LegalEntity, &allowed,
			&prohibited, &processing, &item.DataRetentionPolicy, &item.TermsVersion,
			&item.PricingDisabled, &item.EmergencyKillSwitch, &item.contractStartAt,
			&item.contractEndAt, &item.UpdatedAt, &item.listingActive, &item.DeclaredUptime,
			&item.qualityEnabled, &item.qualityMinimumSamples, &item.QualityGrade, &item.PublishedUptime,
			&item.QualityMeasurementCount, &item.qualityCircuit,
			&item.EnabledModelCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(allowed, &item.AllowedCustomerRegions)
		_ = json.Unmarshal(prohibited, &item.ProhibitedRegions)
		_ = json.Unmarshal(processing, &item.DataProcessingRegions)
		item.Availability = providerPublicAvailability(item, region, time.Now().UTC())
		item.QualitySource = "PLATFORM_MEASURED"
		item.TechnicalStatus = "UNKNOWN"
		if !item.Enabled || item.EmergencyKillSwitch {
			item.TechnicalStatus = "DISABLED"
		} else if item.qualityEnabled && item.QualityMeasurementCount >= item.qualityMinimumSamples && item.qualityCircuit == "CLOSED" {
			item.TechnicalStatus = "OPERATIONAL"
		}
		if !item.qualityEnabled || item.QualityMeasurementCount < item.qualityMinimumSamples {
			item.PublishedUptime = ""
			item.QualityGrade = "UNKNOWN"
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) PublicCatalogDimensions(ctx context.Context) (PublicCatalogDimensions, error) {
	out := PublicCatalogDimensions{Regions: []string{}, Currencies: []string{}}
	regionRows, err := s.pool.Query(ctx, `SELECT DISTINCT upper(value) FROM (
		SELECT jsonb_array_elements_text(p.allowed_customer_regions) value
		FROM providers p WHERE p.enabled AND NOT p.pricing_disabled AND NOT p.emergency_kill_switch
			AND p.commercial_status='COMMERCIAL_APPROVED' AND p.commercial_resale_status='APPROVED'
		UNION ALL
		SELECT jsonb_array_elements_text(m.allowed_regions) value FROM models m JOIN providers p ON p.id=m.provider_id
		WHERE m.enabled AND p.enabled AND p.commercial_status='COMMERCIAL_APPROVED'
		UNION ALL SELECT region FROM public_commercial_terms WHERE effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
		UNION ALL SELECT region FROM public_payment_fee_schedule WHERE effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
	) region_values WHERE value ~ '^[A-Za-z]{2}$' ORDER BY upper(value)`)
	if err != nil {
		return out, err
	}
	for regionRows.Next() {
		var value string
		if err = regionRows.Scan(&value); err != nil {
			regionRows.Close()
			return out, err
		}
		out.Regions = append(out.Regions, value)
	}
	if err = regionRows.Err(); err != nil {
		regionRows.Close()
		return out, err
	}
	regionRows.Close()

	currencyRows, err := s.pool.Query(ctx, `SELECT DISTINCT currency FROM (
		SELECT r.currency FROM customer_retail_price_book r
		WHERE r.organization_id IS NULL AND r.approval_status IN ('APPROVED','FORCED_APPROVED')
			AND r.effective_at<=now() AND (r.expires_at IS NULL OR r.expires_at>now())
		UNION ALL SELECT v.currency FROM subscription_plan p JOIN plan_version v ON v.id=p.current_version_id
		WHERE p.enabled AND v.status='FROZEN' AND v.effective_at<=now()
		UNION ALL SELECT currency FROM public_commercial_terms WHERE effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
		UNION ALL SELECT currency FROM public_payment_fee_schedule WHERE effective_at<=now() AND (expires_at IS NULL OR expires_at>now())
	) currency_values ORDER BY currency`)
	if err != nil {
		return out, err
	}
	defer currencyRows.Close()
	for currencyRows.Next() {
		var value string
		if err = currencyRows.Scan(&value); err != nil {
			return out, err
		}
		out.Currencies = append(out.Currencies, value)
	}
	return out, currencyRows.Err()
}

func (s *Store) ListPublicModels(ctx context.Context, region, currency string) ([]PublicModel, error) {
	region = normalizePublicRegion(region)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	rows, err := s.pool.Query(ctx, `SELECT m.id,m.provider_id,p.name,p.slug,m.provider_model_id,m.display_name,
		m.model_type,m.enabled,m.capabilities,m.context_window,m.allowed_regions,COALESCE(m.service_subject,''),
		COALESCE(m.filing_info,''),m.generated_content_label_capability,COALESCE(m.user_disclosure,''),m.updated_at,
		p.enabled,p.commercial_status,p.commercial_resale_status,p.allowed_customer_regions,p.prohibited_regions,
		p.data_processing_regions,p.pricing_disabled,p.emergency_kill_switch,p.contract_start_at,p.contract_end_at,p.updated_at,
		COALESCE(qp.enabled,false),COALESCE(qs.circuit_state,'CLOSED'),
		price.id,price.input_token_price::text,price.cached_input_token_price::text,
		price.output_token_price::text,price.request_fixed_price::text,price.currency,price.unit,
		price.effective_at,price.expires_at,cost.currency
		FROM models m JOIN providers p ON p.id=m.provider_id
		LEFT JOIN provider_quality_policies qp ON qp.provider_id=p.id
		LEFT JOIN provider_quality_states qs ON qs.provider_id=p.id
		LEFT JOIN LATERAL (
			SELECT r.id,r.input_token_price,r.cached_input_token_price,r.output_token_price,
				r.request_fixed_price,r.currency,r.unit,r.effective_at,r.expires_at
			FROM customer_retail_price_book r
			WHERE r.organization_id IS NULL AND r.provider_id=m.provider_id AND r.model_id=m.id
				AND r.approval_status IN ('APPROVED','FORCED_APPROVED')
				AND r.effective_at<=now() AND (r.expires_at IS NULL OR r.expires_at>now())
				AND ($1='' OR r.currency=$1)
			ORDER BY r.effective_at DESC,r.id DESC LIMIT 1
		) price ON true
		LEFT JOIN LATERAL (
			SELECT c.currency FROM provider_cost_price_book c
			WHERE c.provider_id=m.provider_id AND c.model_id=m.id
				AND c.approval_status IN ('APPROVED','FORCED_APPROVED')
				AND c.effective_at<=now() AND (c.expires_at IS NULL OR c.expires_at>now())
			ORDER BY c.effective_at DESC,c.id DESC LIMIT 1
		) cost ON true
		WHERE m.enabled
		ORDER BY p.name,m.display_name,m.id`, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicModel, 0)
	for rows.Next() {
		var item PublicModel
		var modelEnabled bool
		var capabilities, modelRegions, providerAllowed, providerProhibited, providerProcessing []byte
		var provider PublicProvider
		var priceID, input, cached, output, fixed, priceCurrency, costCurrency *string
		var unit *int64
		var effective *time.Time
		var expires *time.Time
		if err = rows.Scan(&item.ID, &item.ProviderID, &item.ProviderName, &item.ProviderSlug,
			&item.ProviderModelID, &item.DisplayName, &item.ModelType, &modelEnabled, &capabilities,
			&item.ContextWindow, &modelRegions, &item.ServiceSubject, &item.FilingInfo,
			&item.GeneratedContentLabelCapability, &item.UserDisclosure, &item.UpdatedAt,
			&provider.Enabled, &provider.CommercialStatus, &provider.CommercialResaleStatus,
			&providerAllowed, &providerProhibited, &providerProcessing, &provider.PricingDisabled,
			&provider.EmergencyKillSwitch, &provider.contractStartAt, &provider.contractEndAt,
			&provider.UpdatedAt, &provider.qualityEnabled, &provider.qualityCircuit,
			&priceID, &input, &cached, &output, &fixed, &priceCurrency, &unit,
			&effective, &expires, &costCurrency); err != nil {
			return nil, err
		}
		provider.ID = item.ProviderID
		_ = json.Unmarshal(providerAllowed, &provider.AllowedCustomerRegions)
		_ = json.Unmarshal(providerProhibited, &provider.ProhibitedRegions)
		_ = json.Unmarshal(providerProcessing, &provider.DataProcessingRegions)
		_ = json.Unmarshal(capabilities, &item.Capabilities)
		_ = json.Unmarshal(modelRegions, &item.AllowedRegions)
		availability := providerPublicAvailability(provider, region, time.Now().UTC())
		if availability.Available && !modelEnabled {
			availability = unavailable(region, "MODEL_DISABLED")
		}
		if availability.Available && !stringSetAllows(item.AllowedRegions, region) {
			availability = unavailable(region, "MODEL_REGION_NOT_ALLOWED")
		}
		if priceID != nil && unit != nil && effective != nil {
			item.Pricing = &PublicTokenPrice{
				PriceBookID: *priceID, ProviderID: item.ProviderID, ModelID: item.ID,
				ProviderName: item.ProviderName, ProviderSlug: item.ProviderSlug,
				ModelName: item.DisplayName, ProviderModelID: item.ProviderModelID,
				InputTokenPrice: valueOrZero(input), CachedInputTokenPrice: valueOrZero(cached),
				OutputTokenPrice: valueOrZero(output), RequestFixedPrice: valueOrZero(fixed),
				Currency: valueOrEmpty(priceCurrency), Unit: *unit, EffectiveAt: *effective, ExpiresAt: expires,
			}
		}
		if availability.Available && item.Pricing == nil {
			availability = unavailable(region, "PUBLIC_PRICE_UNAVAILABLE")
		}
		if availability.Available && costCurrency == nil {
			availability = unavailable(region, "PROVIDER_COST_UNAVAILABLE")
		}
		if availability.Available && priceCurrency != nil && costCurrency != nil && !strings.EqualFold(*priceCurrency, *costCurrency) {
			availability = unavailable(region, "PRICING_CURRENCY_MISMATCH")
		}
		item.Availability = availability
		if item.Pricing != nil {
			item.Pricing.Availability = availability
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) PublicPricing(ctx context.Context, region, currency string) (PublicPricingCatalog, error) {
	region = normalizePublicRegion(region)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	result := PublicPricingCatalog{Region: region, Currency: currency, UpdatedAt: time.Now().UTC()}
	var err error
	result.SubscriptionPlans, err = s.listPublicSubscriptionPlans(ctx, currency)
	if err != nil {
		return result, err
	}
	models, err := s.ListPublicModels(ctx, region, currency)
	if err != nil {
		return result, err
	}
	result.TokenPrices = make([]PublicTokenPrice, 0)
	for _, model := range models {
		if model.Pricing != nil {
			result.TokenPrices = append(result.TokenPrices, *model.Pricing)
		}
	}
	result.CommercialTerms, err = s.publicCommercialTerms(ctx, region, currency)
	if err != nil {
		return result, err
	}
	result.TermsConfigured = result.CommercialTerms != nil
	result.PaymentFees, err = s.publicPaymentFees(ctx, region, currency)
	if err != nil {
		return result, err
	}
	result.PaymentFeesConfigured = len(result.PaymentFees) > 0
	return result, nil
}

func (s *Store) listPublicSubscriptionPlans(ctx context.Context, currency string) ([]PublicSubscriptionPlan, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id,p.slug,p.name,p.description,p.plan_kind,v.id,v.version,
		v.subscription_fee::text,v.currency,v.billing_interval,v.trial_days,v.token_billing_mode,
		v.enterprise_contract,v.effective_at,
		COALESCE((SELECT jsonb_object_agg(e.entitlement_key,
			CASE e.value_type WHEN 'INTEGER' THEN to_jsonb(e.integer_value)
				WHEN 'BOOLEAN' THEN to_jsonb(e.boolean_value) ELSE to_jsonb(e.string_value) END)
			FROM plan_entitlement e WHERE e.plan_version_id=v.id),'{}'::jsonb)
		FROM subscription_plan p JOIN plan_version v ON v.id=p.current_version_id
		WHERE p.enabled AND v.status='FROZEN' AND v.effective_at<=now() AND ($1='' OR v.currency=$1)
		ORDER BY CASE p.slug WHEN 'free' THEN 0 WHEN 'developer' THEN 1 WHEN 'team' THEN 2
			WHEN 'enterprise' THEN 3 ELSE 4 END,p.name,p.id`, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicSubscriptionPlan, 0)
	for rows.Next() {
		var item PublicSubscriptionPlan
		var entitlements []byte
		if err = rows.Scan(&item.PlanID, &item.Slug, &item.Name, &item.Description, &item.PlanKind,
			&item.PlanVersionID, &item.Version, &item.SubscriptionFee, &item.Currency,
			&item.BillingInterval, &item.TrialDays, &item.TokenBillingMode,
			&item.EnterpriseContract, &item.EffectiveAt, &entitlements); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(entitlements, &item.Entitlements)
		if item.Entitlements == nil {
			item.Entitlements = map[string]any{}
		}
		item.ContactSales = item.EnterpriseContract
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) publicCommercialTerms(ctx context.Context, region, currency string) (*PublicCommercialTerms, error) {
	var out PublicCommercialTerms
	err := s.pool.QueryRow(ctx, `SELECT id,region,currency,subscription_tax_included,token_tax_included,
		tax_disclosure,refund_summary,refund_policy_url,bonus_credit_amount::text,bonus_non_refundable,
		legal_review_required,legal_review_status,effective_at,expires_at,created_at
		FROM public_commercial_terms
		WHERE region IN ($1,'*') AND currency=$2 AND effective_at<=now()
			AND (expires_at IS NULL OR expires_at>now())
		ORDER BY (region=$1) DESC,effective_at DESC,id DESC LIMIT 1`, region, currency).
		Scan(&out.ID, &out.Region, &out.Currency, &out.SubscriptionTaxIncluded, &out.TokenTaxIncluded,
			&out.TaxDisclosure, &out.RefundSummary, &out.RefundPolicyURL, &out.BonusCreditAmount,
			&out.BonusNonRefundable, &out.LegalReviewRequired, &out.LegalReviewStatus,
			&out.EffectiveAt, &out.ExpiresAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &out, err
}

func (s *Store) publicPaymentFees(ctx context.Context, region, currency string) ([]PublicPaymentFee, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT ON (fee_category,payment_provider)
		id,fee_category,payment_provider,region,currency,fee_kind,fixed_amount::text,rate_bps,
		charged_to_customer,description,legal_review_required,legal_review_status,effective_at,expires_at,created_at
		FROM public_payment_fee_schedule
		WHERE region IN ($1,'*') AND currency=$2 AND effective_at<=now()
			AND (expires_at IS NULL OR expires_at>now())
		ORDER BY fee_category,payment_provider,(region=$1) DESC,effective_at DESC,id DESC`, region, currency)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicPaymentFee, 0)
	for rows.Next() {
		var item PublicPaymentFee
		if err = rows.Scan(&item.ID, &item.FeeCategory, &item.PaymentProvider, &item.Region,
			&item.Currency, &item.FeeKind, &item.FixedAmount, &item.RateBPS,
			&item.ChargedToCustomer, &item.Description, &item.LegalReviewRequired,
			&item.LegalReviewStatus, &item.EffectiveAt, &item.ExpiresAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) PublishCommercialTerms(ctx context.Context, request PublishCommercialTermsRequest) (PublicCommercialTerms, bool, error) {
	request.Region = normalizePublicRegion(request.Region)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.LegalReviewStatus = strings.ToUpper(strings.TrimSpace(request.LegalReviewStatus))
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.RefundPolicyURL = strings.TrimSpace(request.RefundPolicyURL)
	request.BonusCreditAmount = zeroIfEmpty(request.BonusCreditAmount)
	if request.EffectiveAt.IsZero() {
		return PublicCommercialTerms{}, false, errors.New("effective_at is required for idempotent commercial evidence")
	}
	fingerprint, err := publicCommercialTermsFingerprint(request)
	if err != nil {
		return PublicCommercialTerms{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublicCommercialTerms{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "public-terms:"+request.IdempotencyKey); err != nil {
		return PublicCommercialTerms{}, false, err
	}
	var existingID, existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,request_fingerprint FROM public_commercial_terms WHERE idempotency_key=$1`, request.IdempotencyKey).
		Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return PublicCommercialTerms{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return PublicCommercialTerms{}, false, err
		}
		out, loadErr := s.publicCommercialTermsByID(ctx, existingID)
		return out, true, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PublicCommercialTerms{}, false, err
	}
	termsID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO public_commercial_terms(
		id,idempotency_key,request_fingerprint,region,currency,subscription_tax_included,
		token_tax_included,tax_disclosure,refund_summary,refund_policy_url,bonus_credit_amount,
		bonus_non_refundable,effective_at,expires_at,legal_review_status,reviewed_by,reviewed_at,created_by
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		NULLIF($16,'')::uuid,$17,NULLIF($18,'')::uuid)`, termsID, request.IdempotencyKey, fingerprint,
		request.Region, request.Currency, request.SubscriptionTaxIncluded, request.TokenTaxIncluded,
		request.TaxDisclosure, request.RefundSummary, request.RefundPolicyURL,
		zeroIfEmpty(request.BonusCreditAmount), request.BonusNonRefundable, request.EffectiveAt.UTC(),
		request.ExpiresAt, request.LegalReviewStatus, request.CreatedBy, request.ReviewedAt, request.CreatedBy)
	if err != nil {
		return PublicCommercialTerms{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,action,resource_type,resource_id,after_state)
		VALUES($1,NULLIF($2,'')::uuid,'commercial.public_terms.publish','public_commercial_terms',$3,$4)`,
		id.UUID(), request.CreatedBy, termsID, jsonBytes(map[string]any{
			"region": request.Region, "currency": request.Currency, "effective_at": request.EffectiveAt,
			"legal_review_status": request.LegalReviewStatus,
		})); err != nil {
		return PublicCommercialTerms{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PublicCommercialTerms{}, false, err
	}
	out, err := s.publicCommercialTermsByID(ctx, termsID)
	return out, false, err
}

func (s *Store) PublishPaymentFee(ctx context.Context, request PublishPaymentFeeRequest) (PublicPaymentFee, bool, error) {
	request.FeeCategory = strings.ToUpper(strings.TrimSpace(request.FeeCategory))
	request.PaymentProvider = strings.ToLower(strings.TrimSpace(request.PaymentProvider))
	request.Region = normalizePublicRegion(request.Region)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.FeeKind = strings.ToUpper(strings.TrimSpace(request.FeeKind))
	request.LegalReviewStatus = strings.ToUpper(strings.TrimSpace(request.LegalReviewStatus))
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.FixedAmount = zeroIfEmpty(request.FixedAmount)
	if request.EffectiveAt.IsZero() {
		return PublicPaymentFee{}, false, errors.New("effective_at is required for idempotent payment fee evidence")
	}
	fingerprint, err := publicPaymentFeeFingerprint(request)
	if err != nil {
		return PublicPaymentFee{}, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublicPaymentFee{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "public-fee:"+request.IdempotencyKey); err != nil {
		return PublicPaymentFee{}, false, err
	}
	var existingID, existingFingerprint string
	err = tx.QueryRow(ctx, `SELECT id,request_fingerprint FROM public_payment_fee_schedule WHERE idempotency_key=$1`, request.IdempotencyKey).
		Scan(&existingID, &existingFingerprint)
	if err == nil {
		if existingFingerprint != fingerprint {
			return PublicPaymentFee{}, false, ErrIdempotencyConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return PublicPaymentFee{}, false, err
		}
		out, loadErr := s.publicPaymentFeeByID(ctx, existingID)
		return out, true, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PublicPaymentFee{}, false, err
	}
	feeID := id.UUID()
	_, err = tx.Exec(ctx, `INSERT INTO public_payment_fee_schedule(
		id,idempotency_key,request_fingerprint,fee_category,payment_provider,region,currency,
		fee_kind,fixed_amount,rate_bps,charged_to_customer,description,effective_at,expires_at,
		legal_review_status,reviewed_by,reviewed_at,created_by
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		NULLIF($16,'')::uuid,$17,NULLIF($18,'')::uuid)`, feeID, request.IdempotencyKey, fingerprint,
		request.FeeCategory, request.PaymentProvider, request.Region, request.Currency, request.FeeKind,
		zeroIfEmpty(request.FixedAmount), request.RateBPS, request.ChargedToCustomer, request.Description,
		request.EffectiveAt.UTC(), request.ExpiresAt, request.LegalReviewStatus, request.CreatedBy,
		request.ReviewedAt, request.CreatedBy)
	if err != nil {
		return PublicPaymentFee{}, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_id,action,resource_type,resource_id,after_state)
		VALUES($1,NULLIF($2,'')::uuid,'commercial.payment_fee.publish','public_payment_fee_schedule',$3,$4)`,
		id.UUID(), request.CreatedBy, feeID, jsonBytes(map[string]any{
			"fee_category": request.FeeCategory, "payment_provider": request.PaymentProvider,
			"region": request.Region, "currency": request.Currency, "effective_at": request.EffectiveAt,
			"legal_review_status": request.LegalReviewStatus,
		})); err != nil {
		return PublicPaymentFee{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PublicPaymentFee{}, false, err
	}
	out, err := s.publicPaymentFeeByID(ctx, feeID)
	return out, false, err
}

func (s *Store) ListCommercialTermsEvidence(ctx context.Context, limit, offset int) ([]PublicCommercialTerms, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,region,currency,subscription_tax_included,token_tax_included,
		tax_disclosure,refund_summary,refund_policy_url,bonus_credit_amount::text,bonus_non_refundable,
		legal_review_required,legal_review_status,effective_at,expires_at,created_at
		FROM public_commercial_terms ORDER BY effective_at DESC,id DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicCommercialTerms, 0)
	for rows.Next() {
		item, scanErr := scanPublicCommercialTerms(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListPaymentFeeEvidence(ctx context.Context, limit, offset int) ([]PublicPaymentFee, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,fee_category,payment_provider,region,currency,fee_kind,
		fixed_amount::text,rate_bps,charged_to_customer,description,legal_review_required,
		legal_review_status,effective_at,expires_at,created_at
		FROM public_payment_fee_schedule ORDER BY effective_at DESC,id DESC LIMIT $1 OFFSET $2`, clamp(limit), max(offset, 0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PublicPaymentFee, 0)
	for rows.Next() {
		item, scanErr := scanPublicPaymentFee(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) publicCommercialTermsByID(ctx context.Context, termsID string) (PublicCommercialTerms, error) {
	return scanPublicCommercialTerms(s.pool.QueryRow(ctx, `SELECT id,region,currency,subscription_tax_included,
		token_tax_included,tax_disclosure,refund_summary,refund_policy_url,bonus_credit_amount::text,
		bonus_non_refundable,legal_review_required,legal_review_status,effective_at,expires_at,created_at
		FROM public_commercial_terms WHERE id=$1`, termsID))
}

func scanPublicCommercialTerms(row pgx.Row) (PublicCommercialTerms, error) {
	var out PublicCommercialTerms
	err := row.Scan(&out.ID, &out.Region, &out.Currency, &out.SubscriptionTaxIncluded,
		&out.TokenTaxIncluded, &out.TaxDisclosure, &out.RefundSummary, &out.RefundPolicyURL,
		&out.BonusCreditAmount, &out.BonusNonRefundable, &out.LegalReviewRequired,
		&out.LegalReviewStatus, &out.EffectiveAt, &out.ExpiresAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func (s *Store) publicPaymentFeeByID(ctx context.Context, feeID string) (PublicPaymentFee, error) {
	return scanPublicPaymentFee(s.pool.QueryRow(ctx, `SELECT id,fee_category,payment_provider,region,currency,
		fee_kind,fixed_amount::text,rate_bps,charged_to_customer,description,legal_review_required,
		legal_review_status,effective_at,expires_at,created_at FROM public_payment_fee_schedule WHERE id=$1`, feeID))
}

func scanPublicPaymentFee(row pgx.Row) (PublicPaymentFee, error) {
	var out PublicPaymentFee
	err := row.Scan(&out.ID, &out.FeeCategory, &out.PaymentProvider, &out.Region, &out.Currency,
		&out.FeeKind, &out.FixedAmount, &out.RateBPS, &out.ChargedToCustomer, &out.Description,
		&out.LegalReviewRequired, &out.LegalReviewStatus, &out.EffectiveAt, &out.ExpiresAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

func publicEvidenceFingerprint(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func publicCommercialTermsFingerprint(request PublishCommercialTermsRequest) (string, error) {
	// ReviewedAt is server-generated confirmation evidence. It is stored on the
	// first insert but cannot be part of the caller's logical idempotency input,
	// otherwise an identical approved retry receives a new timestamp and
	// spuriously conflicts.
	request.ReviewedAt = nil
	return publicEvidenceFingerprint(request)
}

func publicPaymentFeeFingerprint(request PublishPaymentFeeRequest) (string, error) {
	request.ReviewedAt = nil
	return publicEvidenceFingerprint(request)
}

func (s *Store) RecordAnonymousHomepageVisit(ctx context.Context, anonymousHash []byte, idempotencyKey string) (FunnelEvent, bool, error) {
	if len(anonymousHash) != 32 || len(idempotencyKey) < 1 || len(idempotencyKey) > 200 {
		return FunnelEvent{}, false, errors.New("valid anonymous hash and idempotency key are required")
	}
	var insertedID *string
	err := s.pool.QueryRow(ctx, `SELECT record_commercial_funnel_event(
		'HOMEPAGE_VISITED',NULL,NULL,$1,'public_page','/', $2,now(),
		'{"acquisition_source":"PUBLIC_WEB"}'::jsonb)::text`,
		anonymousHash, idempotencyKey).Scan(&insertedID)
	if err != nil {
		return FunnelEvent{}, false, err
	}
	replayed := insertedID == nil
	event, err := s.funnelEventByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return event, replayed, err
	}
	if event.EventType != FunnelHomepageVisited || event.SourceResourceType != "public_page" || event.SourceResourceID != "/" {
		return FunnelEvent{}, false, ErrIdempotencyConflict
	}
	var storedHash []byte
	if err = s.pool.QueryRow(ctx, `SELECT anonymous_id_hash FROM commercial_funnel_events
		WHERE idempotency_key=$1`, idempotencyKey).Scan(&storedHash); err != nil {
		return event, replayed, err
	}
	if len(storedHash) != len(anonymousHash) || subtle.ConstantTimeCompare(storedHash, anonymousHash) != 1 {
		return FunnelEvent{}, false, ErrIdempotencyConflict
	}
	return event, replayed, err
}

func (s *Store) funnelEventByIdempotencyKey(ctx context.Context, idempotencyKey string) (FunnelEvent, error) {
	var out FunnelEvent
	var metadata []byte
	err := s.pool.QueryRow(ctx, `SELECT id,event_type,user_id,organization_id,source_resource_type,
		source_resource_id,idempotency_key,metadata,occurred_at,created_at
		FROM commercial_funnel_events WHERE idempotency_key=$1`, idempotencyKey).
		Scan(&out.ID, &out.EventType, &out.UserID, &out.OrganizationID, &out.SourceResourceType,
			&out.SourceResourceID, &out.IdempotencyKey, &metadata, &out.OccurredAt, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal(metadata, &out.Metadata)
	}
	return out, err
}

func (s *Store) CommercialFunnelSummary(ctx context.Context, from, to time.Time) (FunnelSummary, error) {
	out := FunnelSummary{From: from.UTC(), To: to.UTC(), Counts: make(map[string]int64, len(FunnelOrder))}
	for _, eventType := range FunnelOrder {
		out.Counts[eventType] = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT event_type,count(*) FROM commercial_funnel_events
		WHERE occurred_at >= $1 AND occurred_at < $2 GROUP BY event_type`, from.UTC(), to.UTC())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var eventType string
		var count int64
		if err = rows.Scan(&eventType, &count); err != nil {
			return out, err
		}
		out.Counts[eventType] = count
	}
	return out, rows.Err()
}

func (s *Store) UserOnboardingStatus(ctx context.Context, userID, organizationID, projectID string) (OnboardingStatus, error) {
	out := OnboardingStatus{UserID: userID, Milestones: map[string]bool{}}
	organizationID = strings.TrimSpace(organizationID)
	projectID = strings.TrimSpace(projectID)
	var registeredAt time.Time
	var verifiedAt *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT created_at,email_verified_at FROM users WHERE id=$1`, userID).
		Scan(&registeredAt, &verifiedAt); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	var organizationCreatedAt *time.Time
	if projectID != "" {
		var created time.Time
		var projectOrganizationID string
		err := s.pool.QueryRow(ctx, `SELECT p.organization_id,o.created_at FROM projects p
			JOIN organizations o ON o.id=p.organization_id
			JOIN project_memberships pm ON pm.project_id=p.id AND pm.user_id=$1 AND pm.status='ACTIVE'
			JOIN organization_memberships om ON om.organization_id=p.organization_id AND om.user_id=$1 AND om.status='ACTIVE'
			WHERE p.id=$2 AND p.status='ACTIVE' AND o.status='ACTIVE'
				AND (NULLIF($3,'')::uuid IS NULL OR p.organization_id=NULLIF($3,'')::uuid)`,
			userID, projectID, organizationID).Scan(&projectOrganizationID, &created)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		organizationID = projectOrganizationID
		organizationCreatedAt = &created
	} else if organizationID == "" {
		var created time.Time
		err := s.pool.QueryRow(ctx, `SELECT o.id,o.created_at FROM organizations o
			JOIN organization_memberships m ON m.organization_id=o.id
			WHERE m.user_id=$1 AND m.status='ACTIVE' AND o.status='ACTIVE'
			ORDER BY (m.role='OWNER') DESC,o.created_at,o.id LIMIT 1`, userID).
			Scan(&organizationID, &created)
		if err == nil {
			organizationCreatedAt = &created
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	} else {
		var created time.Time
		err := s.pool.QueryRow(ctx, `SELECT o.created_at FROM organizations o
			JOIN organization_memberships m ON m.organization_id=o.id
			WHERE o.id=$1 AND m.user_id=$2 AND m.status='ACTIVE' AND o.status='ACTIVE'`, organizationID, userID).
			Scan(&created)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		organizationCreatedAt = &created
	}
	out.OrganizationID = organizationID
	if projectID == "" && organizationID != "" {
		_ = s.pool.QueryRow(ctx, `SELECT p.id FROM projects p JOIN project_memberships m ON m.project_id=p.id
			WHERE p.organization_id=$1 AND m.user_id=$2 AND p.status='ACTIVE' AND m.status='ACTIVE'
			ORDER BY p.created_at,p.id LIMIT 1`, organizationID, userID).Scan(&projectID)
	}
	out.ProjectID = projectID

	type milestone struct {
		at       time.Time
		resource string
	}
	milestones := map[string]milestone{}
	rows, err := s.pool.Query(ctx, `SELECT event_type,min(occurred_at),min(source_resource_id)
		FROM commercial_funnel_events WHERE
			(event_type IN ('REGISTERED','EMAIL_VERIFIED') AND user_id=$1)
			OR (event_type IN ('FIRST_RECHARGE','FIRST_SUBSCRIPTION')
				AND organization_id=NULLIF($2,'')::uuid)
		GROUP BY event_type`, userID, organizationID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var eventType string
		var item milestone
		if err = rows.Scan(&eventType, &item.at, &item.resource); err != nil {
			rows.Close()
			return out, err
		}
		milestones[eventType] = item
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if projectID != "" {
		var item milestone
		err = s.pool.QueryRow(ctx, `SELECT id::text,created_at FROM api_keys
			WHERE user_id=$1 AND organization_id=$2 AND project_id=$3
			ORDER BY created_at,id LIMIT 1`, userID, organizationID, projectID).
			Scan(&item.resource, &item.at)
		if err == nil {
			milestones[FunnelAPIKeyCreated] = item
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
		callRows, callErr := s.pool.Query(ctx, `SELECT l.request_id,l.created_at FROM request_logs l
			LEFT JOIN funding_operation funding ON funding.id=l.funding_operation_id
			WHERE l.user_id=$1 AND l.organization_id=$2 AND l.project_id=$3
				AND l.status_code BETWEEN 200 AND 299
				AND (l.funding_operation_id IS NULL OR funding.status IN ('SETTLED','PARTIALLY_SETTLED','RELEASED'))
			ORDER BY l.created_at,l.id LIMIT 2`, userID, organizationID, projectID)
		if callErr != nil {
			return out, callErr
		}
		callNumber := 0
		for callRows.Next() {
			callNumber++
			var call milestone
			if err = callRows.Scan(&call.resource, &call.at); err != nil {
				callRows.Close()
				return out, err
			}
			if callNumber == 1 {
				milestones[FunnelFirstAPICall] = call
			} else {
				milestones[FunnelSecondAPICall] = call
			}
		}
		if err = callRows.Err(); err != nil {
			callRows.Close()
			return out, err
		}
		callRows.Close()
		var usage milestone
		err = s.pool.QueryRow(ctx, `SELECT usage.id::text,usage.created_at
			FROM billing_usage_records usage JOIN request_logs request ON request.request_id=usage.request_id
			LEFT JOIN funding_operation funding ON funding.id=request.funding_operation_id
			WHERE request.user_id=$1 AND usage.organization_id=$2 AND usage.project_id=$3
				AND request.status_code BETWEEN 200 AND 299
				AND (request.funding_operation_id IS NULL OR funding.status IN ('SETTLED','PARTIALLY_SETTLED','RELEASED'))
				AND usage.status IN ('RECORDED','CHARGED','WAIVED','REFUNDED')
			ORDER BY usage.created_at,usage.id LIMIT 1`, userID, organizationID, projectID).
			Scan(&usage.resource, &usage.at)
		if err == nil {
			milestones["USAGE_AND_CHARGE_RECORDED"] = usage
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}

	step := func(key string, required bool, at *time.Time, resource string) OnboardingStep {
		return OnboardingStep{Key: key, Required: required, Completed: at != nil, CompletedAt: at, ResourceID: resource}
	}
	eventStep := func(key, eventType string, required bool) OnboardingStep {
		item, ok := milestones[eventType]
		if !ok {
			return step(key, required, nil, "")
		}
		at := item.at
		return step(key, required, &at, item.resource)
	}
	out.Steps = []OnboardingStep{
		step("REGISTER", true, &registeredAt, userID),
		step("VERIFY_EMAIL", true, verifiedAt, userID),
		step("CREATE_ORGANIZATION", true, organizationCreatedAt, organizationID),
		eventStep("SELECT_PLAN", FunnelFirstSubscription, true),
		eventStep("RECHARGE", FunnelFirstRecharge, true),
		eventStep("CREATE_API_KEY", FunnelAPIKeyCreated, true),
		eventStep("FIRST_API_CALL", FunnelFirstAPICall, true),
		eventStep("VIEW_USAGE_AND_CHARGE", "USAGE_AND_CHARGE_RECORDED", true),
	}
	out.Complete = true
	for _, item := range out.Steps {
		if item.Required && !item.Completed {
			out.Complete = false
			if out.NextStep == "" {
				out.NextStep = item.Key
			}
		}
	}
	for _, eventType := range FunnelOrder {
		_, out.Milestones[eventType] = milestones[eventType]
	}
	_, out.Milestones["USAGE_AND_CHARGE_RECORDED"] = milestones["USAGE_AND_CHARGE_RECORDED"]
	return out, nil
}

func providerPublicAvailability(provider PublicProvider, region string, now time.Time) PublicAvailability {
	if !provider.Enabled {
		return unavailable(region, "PROVIDER_DISABLED")
	}
	if provider.EmergencyKillSwitch {
		return unavailable(region, "EMERGENCY_KILL_SWITCH")
	}
	if provider.qualityEnabled && provider.qualityCircuit != "CLOSED" {
		return unavailable(region, "PROVIDER_QUALITY_UNAVAILABLE")
	}
	if provider.PricingDisabled {
		return unavailable(region, "PRICING_DISABLED")
	}
	if provider.CommercialStatus != "COMMERCIAL_APPROVED" {
		return unavailable(region, "CONTRACT_NOT_APPROVED")
	}
	if provider.CommercialResaleStatus != "APPROVED" {
		return unavailable(region, "RESALE_NOT_APPROVED")
	}
	if provider.contractStartAt != nil && now.Before(*provider.contractStartAt) {
		return unavailable(region, "CONTRACT_NOT_STARTED")
	}
	if provider.contractEndAt != nil && !now.Before(*provider.contractEndAt) {
		return unavailable(region, "CONTRACT_EXPIRED")
	}
	if !stringSetAllows(provider.AllowedCustomerRegions, region) {
		return unavailable(region, "REGION_NOT_ALLOWED")
	}
	if stringSetContains(provider.ProhibitedRegions, region) {
		return unavailable(region, "REGION_PROHIBITED")
	}
	return PublicAvailability{Available: true, Region: region, Status: "AVAILABLE", ReasonCode: "AVAILABLE"}
}

func unavailable(region, reason string) PublicAvailability {
	return PublicAvailability{Available: false, Region: region, Status: "UNAVAILABLE", ReasonCode: reason}
}

func normalizePublicRegion(region string) string {
	region = strings.ToUpper(strings.TrimSpace(region))
	if region == "" {
		return "CN"
	}
	return region
}

func stringSetContains(values []string, value string) bool {
	for _, candidate := range values {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func stringSetAllows(values []string, value string) bool {
	return stringSetContains(values, "*") || stringSetContains(values, value)
}

func valueOrZero(value *string) string {
	if value == nil || *value == "" {
		return "0"
	}
	return *value
}
