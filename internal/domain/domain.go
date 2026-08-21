package domain

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Decimal preserves the exact base-10 representation used by PostgreSQL
// NUMERIC. It accepts either a JSON number or string for control-plane
// compatibility and emits a JSON number without binary floating-point
// conversion.
type Decimal string

func (d *Decimal) UnmarshalJSON(raw []byte) error {
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return err
		}
		value = strings.TrimSpace(parsed)
	}
	if value == "" || value == "null" {
		value = "0"
	}
	if _, ok := new(big.Rat).SetString(value); !ok {
		return errors.New("invalid decimal")
	}
	*d = Decimal(value)
	return nil
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	value := strings.TrimSpace(string(d))
	if value == "" {
		value = "0"
	}
	if _, ok := new(big.Rat).SetString(value); !ok {
		return nil, errors.New("invalid decimal")
	}
	return json.RawMessage(value).MarshalJSON()
}
func (d Decimal) String() string {
	if strings.TrimSpace(string(d)) == "" {
		return "0"
	}
	return string(d)
}
func (d Decimal) IsZero() bool {
	r, ok := new(big.Rat).SetString(d.String())
	return ok && r.Sign() == 0
}
func (d Decimal) IsNegative() bool {
	r, ok := new(big.Rat).SetString(d.String())
	return ok && r.Sign() < 0
}
func (d Decimal) IsPositive() bool {
	r, ok := new(big.Rat).SetString(d.String())
	return ok && r.Sign() > 0
}
func (d Decimal) Add(other Decimal) Decimal {
	a, _ := new(big.Rat).SetString(d.String())
	b, _ := new(big.Rat).SetString(other.String())
	a.Add(a, b)
	return Decimal(a.FloatString(12))
}

type User struct {
	ID                   string     `json:"id"`
	Email                string     `json:"email"`
	PasswordHash         string     `json:"-"`
	DisplayName          string     `json:"display_name"`
	Role                 string     `json:"role"`
	Status               string     `json:"status"`
	MonthlyTokenLimit    *int64     `json:"monthly_token_limit,omitempty"`
	MonthlyCostLimit     *float64   `json:"monthly_cost_limit,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	LastLoginAt          *time.Time `json:"last_login_at,omitempty"`
	EmailVerifiedAt      *time.Time `json:"email_verified_at,omitempty"`
	SessionVersion       int64      `json:"-"`
	MFAEnabled           bool       `json:"mfa_enabled"`
	RiskScore            int        `json:"risk_score"`
	VerificationLevel    string     `json:"verification_level"`
	PaymentRisk          string     `json:"payment_risk"`
	AbuseStatus          string     `json:"abuse_status"`
	ManualReviewStatus   string     `json:"manual_review_status"`
	NewAccountSpendLimit string     `json:"new_account_spend_limit,omitempty"`
	ClosedAt             *time.Time `json:"closed_at,omitempty"`
	LegalHold            bool       `json:"legal_hold"`
}

type OrganizationInvitation struct {
	ID               string     `json:"id"`
	OrganizationID   string     `json:"organization_id"`
	OrganizationName string     `json:"organization_name,omitempty"`
	Email            string     `json:"email"`
	Role             string     `json:"role"`
	Status           string     `json:"status"`
	ExpiresAt        time.Time  `json:"expires_at"`
	InvitedBy        *string    `json:"invited_by,omitempty"`
	AcceptedBy       *string    `json:"accepted_by,omitempty"`
	RespondedAt      *time.Time `json:"responded_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type RegistrationInvite struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	MaxUses   int       `json:"max_uses"`
	UsedCount int       `json:"used_count"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedBy *string   `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type EmailOutbox struct {
	ID               string     `json:"id"`
	Recipient        string     `json:"recipient"`
	Template         string     `json:"template"`
	EncryptedMessage []byte     `json:"-"`
	DedupeKey        string     `json:"-"`
	Status           string     `json:"status"`
	Attempts         int        `json:"attempts"`
	MaxAttempts      int        `json:"max_attempts"`
	AvailableAt      time.Time  `json:"available_at"`
	LockedAt         *time.Time `json:"locked_at,omitempty"`
	LockedUntil      *time.Time `json:"locked_until,omitempty"`
	LockedBy         string     `json:"locked_by,omitempty"`
	ClaimToken       string     `json:"-"`
	DeliveredAt      *time.Time `json:"delivered_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Provider struct {
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	Slug                   string         `json:"slug"`
	ProviderType           string         `json:"provider_type"`
	BaseURL                string         `json:"base_url"`
	Enabled                bool           `json:"enabled"`
	Config                 map[string]any `json:"config"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
	CredentialsCount       int            `json:"credentials_count"`
	ContractStatus         string         `json:"contract_status"`
	CommercialStatus       string         `json:"commercial_status"`
	AllowedRegions         []string       `json:"allowed_regions"`
	PricingDisabled        bool           `json:"pricing_disabled"`
	ContractReviewedAt     *time.Time     `json:"contract_reviewed_at,omitempty"`
	LegalEntity            string         `json:"legal_entity"`
	ContractType           string         `json:"contract_type"`
	ContractStartAt        *time.Time     `json:"contract_start_at,omitempty"`
	ContractEndAt          *time.Time     `json:"contract_end_at,omitempty"`
	CommercialResaleStatus string         `json:"commercial_resale_status"`
	CredentialOwner        string         `json:"credential_owner"`
	AllowedCustomerRegions []string       `json:"allowed_customer_regions"`
	ProhibitedRegions      []string       `json:"prohibited_regions"`
	DataProcessingRegions  []string       `json:"data_processing_regions"`
	DataRetentionPolicy    string         `json:"data_retention_policy"`
	TermsVersion           string         `json:"terms_version"`
	CostLimit              string         `json:"cost_limit,omitempty"`
	RateLimit              *int           `json:"rate_limit,omitempty"`
	SettlementCurrency     string         `json:"settlement_currency"`
	EmergencyKillSwitch    bool           `json:"emergency_kill_switch"`
}

type Credential struct {
	ID                    string     `json:"id"`
	ProviderID            string     `json:"provider_id"`
	ProviderName          string     `json:"provider_name,omitempty"`
	GroupName             string     `json:"group_name,omitempty"`
	Name                  string     `json:"name"`
	CredentialType        string     `json:"credential_type"`
	EncryptedSecret       []byte     `json:"-"`
	HasSecret             bool       `json:"has_secret"`
	SecretLast4           string     `json:"secret_last4"`
	OrganizationID        *string    `json:"organization_id,omitempty"`
	ProjectID             *string    `json:"project_id,omitempty"`
	Status                string     `json:"status"`
	Priority              int        `json:"priority"`
	Weight                int        `json:"weight"`
	MaxConcurrency        int        `json:"max_concurrency"`
	CurrentHealth         string     `json:"current_health"`
	LastSuccessAt         *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt         *time.Time `json:"last_failure_at,omitempty"`
	CooldownUntil         *time.Time `json:"cooldown_until,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	ActiveRequests        int64      `json:"active_requests"`
	EffectiveWeight       int        `json:"effective_weight,omitempty"`
	EffectivePriority     int        `json:"effective_priority,omitempty"`
	Tags                  []string   `json:"tags,omitempty"`
	CredentialOwner       string     `json:"credential_owner"`
	OwnerOrganizationID   *string    `json:"owner_organization_id,omitempty"`
	OwnershipConfirmedAt  *time.Time `json:"ownership_confirmed_at,omitempty"`
	OwnershipConfirmedBy  *string    `json:"ownership_confirmed_by,omitempty"`
	OwnershipTermsVersion string     `json:"ownership_terms_version,omitempty"`
}

type CredentialGroup struct {
	ID               string    `json:"id"`
	ProviderID       string    `json:"provider_id"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	MemberCount      int       `json:"member_count"`
	CredentialsCount int       `json:"credentials_count"`
	HealthyCount     int       `json:"healthy_count"`
	TotalCapacity    int       `json:"total_capacity"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Model struct {
	ID                              string         `json:"id"`
	ProviderID                      string         `json:"provider_id"`
	ProviderName                    string         `json:"provider_name,omitempty"`
	ProviderModelID                 string         `json:"provider_model_id"`
	DisplayName                     string         `json:"display_name"`
	ModelType                       string         `json:"model_type"`
	Enabled                         bool           `json:"enabled"`
	Capabilities                    []string       `json:"capabilities"`
	CapabilitySource                string         `json:"capability_source"`
	ContextWindow                   *int           `json:"context_window,omitempty"`
	LatencyScore                    float64        `json:"latency_score"`
	QualityScore                    float64        `json:"quality_score"`
	InputPrice                      float64        `json:"input_price"`
	OutputPrice                     float64        `json:"output_price"`
	PriceCurrency                   string         `json:"price_currency,omitempty"`
	Metadata                        map[string]any `json:"metadata"`
	AllowedRegions                  []string       `json:"allowed_regions,omitempty"`
	CreatedAt                       time.Time      `json:"created_at"`
	UpdatedAt                       time.Time      `json:"updated_at"`
	ServiceSubject                  string         `json:"service_subject,omitempty"`
	FilingInfo                      string         `json:"filing_info,omitempty"`
	GeneratedContentLabelCapability string         `json:"generated_content_label_capability"`
	UserDisclosure                  string         `json:"user_disclosure,omitempty"`
}

type ModelRoute struct {
	ID                string         `json:"id"`
	Alias             string         `json:"alias"`
	ProviderID        string         `json:"provider_id"`
	ProviderBaseURL   string         `json:"provider_base_url,omitempty"`
	UpstreamModel     string         `json:"upstream_model"`
	CredentialGroupID string         `json:"credential_group_id"`
	FallbackGroupID   *string        `json:"fallback_group_id,omitempty"`
	Enabled           bool           `json:"enabled"`
	RoutingPolicy     string         `json:"routing_policy"`
	FallbackConfig    map[string]any `json:"fallback_config"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type ModelPrice struct {
	ID               string    `json:"id"`
	ModelID          string    `json:"model_id"`
	Version          int       `json:"version"`
	EffectiveFrom    time.Time `json:"effective_from"`
	InputPrice       float64   `json:"input_price"`
	CachedInputPrice float64   `json:"cached_input_price"`
	OutputPrice      float64   `json:"output_price"`
	Currency         string    `json:"currency"`
	Unit             int64     `json:"unit"`
	Source           string    `json:"source"`
	CreatedAt        time.Time `json:"created_at"`
}

type APIKey struct {
	ID                 string     `json:"id"`
	UserID             string     `json:"user_id"`
	OrganizationID     string     `json:"organization_id,omitempty"`
	ProjectID          string     `json:"project_id,omitempty"`
	TeamID             string     `json:"team_id,omitempty"`
	Name               string     `json:"name"`
	Environment        string     `json:"environment"`
	KeyPrefix          string     `json:"key_prefix"`
	KeyHash            []byte     `json:"-"`
	Status             string     `json:"status"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	RateLimitRPM       int        `json:"rate_limit_rpm"`
	RateLimitTPM       int        `json:"rate_limit_tpm"`
	MonthlyTokenLimit  *int64     `json:"monthly_token_limit,omitempty"`
	MonthlyCostLimit   *float64   `json:"monthly_cost_limit,omitempty"`
	AllowedModels      []string   `json:"allowed_models"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	CurrentVersion     int        `json:"current_version,omitempty"`
	FrozenReason       string     `json:"frozen_reason,omitempty"`
	FrozenAt           *time.Time `json:"frozen_at,omitempty"`
	LastLeakDetectedAt *time.Time `json:"last_leak_detected_at,omitempty"`
}

type RequestLog struct {
	RequestID           string         `json:"request_id"`
	TraceID             string         `json:"trace_id,omitempty"`
	UserID              string         `json:"user_id"`
	APIKeyID            string         `json:"api_key_id"`
	OrganizationID      string         `json:"organization_id,omitempty"`
	ProjectID           string         `json:"project_id,omitempty"`
	RouteID             string         `json:"route_id,omitempty"`
	ProviderID          string         `json:"provider_id"`
	CredentialID        string         `json:"credential_id"`
	RequestedModel      string         `json:"requested_model"`
	ResolvedModel       string         `json:"resolved_model"`
	Endpoint            string         `json:"endpoint"`
	StatusCode          int            `json:"status_code"`
	Streaming           bool           `json:"streaming"`
	InputTokens         int64          `json:"input_tokens"`
	CachedInputTokens   int64          `json:"cached_input_tokens"`
	OutputTokens        int64          `json:"output_tokens"`
	TotalTokens         int64          `json:"total_tokens"`
	EstimatedCost       float64        `json:"estimated_cost"`
	ReferenceCost       float64        `json:"reference_cost"`
	SavingsAmount       float64        `json:"savings_amount"`
	LatencyMS           int64          `json:"latency_ms"`
	TTFTMS              *int64         `json:"ttft_ms,omitempty"`
	UpstreamRequestID   string         `json:"upstream_request_id,omitempty"`
	ErrorCode           string         `json:"error_code,omitempty"`
	SchedulerReason     map[string]any `json:"scheduler_reason"`
	CreatedAt           time.Time      `json:"created_at"`
	PricingVersionID    string         `json:"pricing_version_id,omitempty"`
	FundingOperationID  string         `json:"funding_operation_id,omitempty"`
	UsageSource         string         `json:"usage_source,omitempty"`
	ProviderCostAmount  string         `json:"provider_cost_amount,omitempty"`
	CustomerSaleAmount  string         `json:"customer_sale_amount,omitempty"`
	ExchangeRate        string         `json:"exchange_rate,omitempty"`
	PlatformGrossMargin string         `json:"platform_gross_margin,omitempty"`
	PromotionAmount     string         `json:"promotion_amount,omitempty"`
	PreTaxAmount        string         `json:"pre_tax_amount,omitempty"`
	TaxRate             string         `json:"tax_rate,omitempty"`
	TaxAmount           string         `json:"tax_amount,omitempty"`
	FinalUserAmount     string         `json:"final_user_amount,omitempty"`
	PricingRuleVersion  string         `json:"pricing_rule_version,omitempty"`
}

type StatusEvent struct {
	ID            string         `json:"id"`
	Component     string         `json:"component"`
	Status        string         `json:"status"`
	Summary       string         `json:"summary"`
	PublicMessage string         `json:"public_message"`
	DedupeKey     string         `json:"dedupe_key,omitempty"`
	Metadata      map[string]any `json:"metadata"`
	StartedAt     time.Time      `json:"started_at"`
	ResolvedAt    *time.Time     `json:"resolved_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

type ObservabilitySLO struct {
	Name          string    `json:"name"`
	TargetPercent Decimal   `json:"target_percent"`
	WindowMinutes int       `json:"window_minutes"`
	Description   string    `json:"description"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type SupportTicket struct {
	ID              string                 `json:"id"`
	TicketNumber    string                 `json:"ticket_number"`
	Subject         string                 `json:"subject"`
	Status          string                 `json:"status"`
	Priority        string                 `json:"priority"`
	UserID          string                 `json:"user_id,omitempty"`
	OrganizationID  string                 `json:"organization_id,omitempty"`
	RequestID       string                 `json:"request_id,omitempty"`
	OrderID         string                 `json:"order_id,omitempty"`
	LedgerJournalID string                 `json:"ledger_journal_id,omitempty"`
	CreatedBy       string                 `json:"created_by,omitempty"`
	AssignedTo      string                 `json:"assigned_to,omitempty"`
	Context         map[string]any         `json:"context"`
	Messages        []SupportTicketMessage `json:"messages,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
	ResolvedAt      *time.Time             `json:"resolved_at,omitempty"`
}

type SupportTicketMessage struct {
	ID         string    `json:"id"`
	TicketID   string    `json:"ticket_id"`
	AuthorID   string    `json:"author_id,omitempty"`
	Visibility string    `json:"visibility"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	LegacyOrganizationID = "00000000-0000-4000-8000-000000000001"
	LegacyProjectID      = "00000000-0000-4000-8000-000000000002"
)

type Organization struct {
	ID                    string         `json:"id"`
	Name                  string         `json:"name"`
	Slug                  string         `json:"slug"`
	Status                string         `json:"status"`
	BillingRegion         string         `json:"billing_region"`
	Metadata              map[string]any `json:"metadata"`
	AllowedProviderIDs    []string       `json:"allowed_provider_ids,omitempty"`
	ProhibitedProviderIDs []string       `json:"prohibited_provider_ids,omitempty"`
	RequiredDataRegions   []string       `json:"required_data_regions,omitempty"`
	MinimumGrossMargin    string         `json:"minimum_gross_margin,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	RiskScore             int            `json:"risk_score"`
	VerificationLevel     string         `json:"verification_level"`
	PaymentRisk           string         `json:"payment_risk"`
	AbuseStatus           string         `json:"abuse_status"`
	ManualReviewStatus    string         `json:"manual_review_status"`
	NewAccountSpendLimit  string         `json:"new_account_spend_limit,omitempty"`
	LegalHold             bool           `json:"legal_hold"`
}

type RiskEvent struct {
	ID             string         `json:"id"`
	IdempotencyKey string         `json:"idempotency_key"`
	UserID         string         `json:"user_id,omitempty"`
	OrganizationID string         `json:"organization_id,omitempty"`
	EventType      string         `json:"event_type"`
	ScoreDelta     int            `json:"score_delta"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
}

type ContentPolicy struct {
	ID                  string         `json:"id"`
	OrganizationID      string         `json:"organization_id,omitempty"`
	ModelID             string         `json:"model_id,omitempty"`
	Phase               string         `json:"phase"`
	Action              string         `json:"action"`
	FailureMode         string         `json:"failure_mode"`
	ProviderName        string         `json:"provider_name"`
	Config              map[string]any `json:"config"`
	Enabled             bool           `json:"enabled"`
	LegalReviewRequired bool           `json:"legal_review_required"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type ManualReview struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id,omitempty"`
	UserID         string     `json:"user_id,omitempty"`
	RequestID      string     `json:"request_id,omitempty"`
	PolicyID       string     `json:"policy_id,omitempty"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	Resolution     string     `json:"resolution,omitempty"`
	AssignedTo     string     `json:"assigned_to,omitempty"`
	DueAt          *time.Time `json:"due_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type UserReport struct {
	ID              string    `json:"id"`
	ReporterUserID  string    `json:"reporter_user_id,omitempty"`
	OrganizationID  string    `json:"organization_id,omitempty"`
	ReportType      string    `json:"report_type"`
	RequestID       string    `json:"request_id,omitempty"`
	APIKeyID        string    `json:"api_key_id,omitempty"`
	RechargeOrderID string    `json:"recharge_order_id,omitempty"`
	Description     string    `json:"description"`
	Status          string    `json:"status"`
	SLAHours        int       `json:"sla_hours"`
	DueAt           time.Time `json:"due_at"`
	Resolution      string    `json:"resolution,omitempty"`
	HandledBy       string    `json:"handled_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type PrivacySettings struct {
	SubjectType      string    `json:"subject_type"`
	SubjectID        string    `json:"subject_id"`
	SaveContent      bool      `json:"save_content"`
	RetentionDays    int       `json:"retention_days"`
	CrossBorderRoute string    `json:"cross_border_route"`
	LegalHold        bool      `json:"legal_hold"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type DataLifecycleJob struct {
	ID             string         `json:"id"`
	SubjectType    string         `json:"subject_type"`
	SubjectID      string         `json:"subject_id"`
	JobType        string         `json:"job_type"`
	Status         string         `json:"status"`
	LegalHold      bool           `json:"legal_hold"`
	IdempotencyKey string         `json:"idempotency_key"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
	Evidence       map[string]any `json:"evidence"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

const (
	ProviderStatusTechnicallyAvailable = "TECHNICALLY_AVAILABLE"
	ProviderStatusContractPending      = "CONTRACT_PENDING"
	ProviderStatusCommercialApproved   = "COMMERCIAL_APPROVED"
	ProviderStatusSuspended            = "SUSPENDED"
	ProviderStatusExpired              = "EXPIRED"
	ProviderStatusTerminated           = "TERMINATED"
	CredentialOwnerPlatform            = "PLATFORM"
	CredentialOwnerCustomer            = "CUSTOMER"
)

// ProviderCommercialStatus normalizes the legacy pricing-era aliases without
// treating an unreviewed ACTIVE row as commercial approval.
func ProviderCommercialStatus(status string, reviewed bool) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ACTIVE":
		if reviewed {
			return ProviderStatusCommercialApproved
		}
		return ProviderStatusContractPending
	case "PENDING_REVIEW":
		return ProviderStatusContractPending
	case ProviderStatusTechnicallyAvailable, ProviderStatusContractPending,
		ProviderStatusCommercialApproved, ProviderStatusSuspended,
		ProviderStatusExpired, ProviderStatusTerminated:
		return strings.ToUpper(strings.TrimSpace(status))
	default:
		return ProviderStatusContractPending
	}
}

type OrganizationMembership struct {
	OrganizationID string    `json:"organization_id"`
	UserID         string    `json:"user_id"`
	Email          string    `json:"email,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Project struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	Description    string         `json:"description"`
	Status         string         `json:"status"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type ProjectMembership struct {
	OrganizationID string    `json:"organization_id,omitempty"`
	ProjectID      string    `json:"project_id"`
	UserID         string    `json:"user_id"`
	Email          string    `json:"email,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ProjectAccess struct {
	OrganizationID   string `json:"organization_id"`
	ProjectID        string `json:"project_id"`
	OrganizationRole string `json:"organization_role,omitempty"`
	ProjectRole      string `json:"project_role,omitempty"`
}

type ProjectModelRoute struct {
	ID                string         `json:"id"`
	OrganizationID    string         `json:"organization_id"`
	ProjectID         string         `json:"project_id"`
	ModelRouteID      string         `json:"model_route_id"`
	Alias             string         `json:"alias"`
	Enabled           bool           `json:"enabled"`
	RoutingConfig     map[string]any `json:"routing_config"`
	ProviderID        string         `json:"provider_id,omitempty"`
	ProviderType      string         `json:"provider_type,omitempty"`
	ProviderBaseURL   string         `json:"provider_base_url,omitempty"`
	UpstreamModel     string         `json:"upstream_model,omitempty"`
	CredentialGroupID string         `json:"credential_group_id,omitempty"`
	FallbackGroupID   *string        `json:"fallback_group_id,omitempty"`
	RoutingPolicy     string         `json:"routing_policy,omitempty"`
	FallbackConfig    map[string]any `json:"fallback_config,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type RoutingRule struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id"`
	ProjectID      string         `json:"project_id"`
	Name           string         `json:"name"`
	Alias          string         `json:"alias"`
	Strategy       string         `json:"strategy"`
	QualityWeight  float64        `json:"quality_weight"`
	PriceWeight    float64        `json:"price_weight"`
	LatencyWeight  float64        `json:"latency_weight"`
	Enabled        bool           `json:"enabled"`
	Config         map[string]any `json:"config"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type RoutingDecision struct {
	Route      ProjectModelRoute `json:"route"`
	Strategy   string            `json:"strategy"`
	Score      float64           `json:"score"`
	Candidates int               `json:"candidates"`
}

type MarketplaceListing struct {
	ID              string         `json:"id"`
	ProviderID      string         `json:"provider_id"`
	ProviderName    string         `json:"provider_name,omitempty"`
	Endpoint        string         `json:"endpoint"`
	SupportedModels []string       `json:"supported_models"`
	Price           map[string]any `json:"price"`
	Status          string         `json:"status"`
	Uptime          float64        `json:"uptime"`
	Verified        bool           `json:"verified"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type Team struct {
	ID                string         `json:"id"`
	OrganizationID    string         `json:"organization_id"`
	Name              string         `json:"name"`
	Slug              string         `json:"slug"`
	Status            string         `json:"status"`
	MonthlyTokenLimit *int64         `json:"monthly_token_limit,omitempty"`
	MonthlyCostLimit  *float64       `json:"monthly_cost_limit,omitempty"`
	Metadata          map[string]any `json:"metadata"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type TeamMembership struct {
	TeamID         string    `json:"team_id"`
	OrganizationID string    `json:"organization_id"`
	UserID         string    `json:"user_id"`
	Email          string    `json:"email,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Wallet struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationName string    `json:"organization_name,omitempty"`
	Currency         string    `json:"currency"`
	BillingMode      string    `json:"billing_mode"`
	AvailableBalance Decimal   `json:"available_balance"`
	ReservedBalance  Decimal   `json:"reserved_balance"`
	CreditLimit      Decimal   `json:"credit_limit"`
	RiskLimit        Decimal   `json:"risk_limit"`
	RiskExposure     Decimal   `json:"risk_exposure"`
	CreditEnforced   bool      `json:"credit_enforced"`
	Status           string    `json:"status"`
	Version          int64     `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Finance API amounts are strings so JSON clients never cross a binary
// floating-point boundary. Legacy wallet and /v1 response fields remain
// unchanged for compatibility.
type WalletBalanceComposition struct {
	WalletID         string    `json:"wallet_id"`
	OrganizationID   string    `json:"organization_id"`
	Currency         string    `json:"currency"`
	CashAvailable    string    `json:"cash_available"`
	BonusAvailable   string    `json:"bonus_available"`
	CreditLimit      string    `json:"credit_limit"`
	CreditUsed       string    `json:"credit_used"`
	CreditAvailable  string    `json:"credit_available"`
	ReservedBalance  string    `json:"reserved_balance"`
	AggregateBalance string    `json:"aggregate_balance"`
	RefundableCash   string    `json:"refundable_cash"`
	AttributionGap   string    `json:"attribution_gap"`
	AsOf             time.Time `json:"as_of"`
}

type FinanceUsageDetail struct {
	RequestID             string    `json:"request_id"`
	OrganizationID        string    `json:"organization_id"`
	ProjectID             string    `json:"project_id"`
	ProviderID            string    `json:"provider_id"`
	ProviderName          string    `json:"provider_name"`
	Model                 string    `json:"model"`
	InputTokens           int64     `json:"input_tokens"`
	CachedInputTokens     int64     `json:"cached_input_tokens"`
	OutputTokens          int64     `json:"output_tokens"`
	CustomerCharge        string    `json:"customer_charge"`
	PromotionAmount       string    `json:"promotion_amount"`
	CashCharge            string    `json:"cash_charge"`
	ProviderCost          string    `json:"provider_cost,omitempty"`
	GrossMargin           string    `json:"gross_margin,omitempty"`
	Currency              string    `json:"currency"`
	ProviderCurrency      string    `json:"provider_currency,omitempty"`
	FundingOperationID    *string   `json:"funding_operation_id,omitempty"`
	WalletTransactionID   *string   `json:"wallet_transaction_id,omitempty"`
	LedgerJournalID       *string   `json:"ledger_journal_id,omitempty"`
	UpstreamRequestID     string    `json:"upstream_request_id,omitempty"`
	ProviderAttemptStatus string    `json:"provider_attempt_status,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
}

type MonthlyStatement struct {
	OrganizationID     string `json:"organization_id"`
	Month              string `json:"month"`
	Currency           string `json:"currency"`
	OpeningBalance     string `json:"opening_balance"`
	RechargeAmount     string `json:"recharge_amount"`
	UsageCharge        string `json:"usage_charge"`
	PromotionAmount    string `json:"promotion_amount"`
	SubscriptionAmount string `json:"subscription_amount"`
	RefundAmount       string `json:"refund_amount"`
	ClosingBalance     string `json:"closing_balance"`
	ProviderCost       string `json:"provider_cost,omitempty"`
	GrossMargin        string `json:"gross_margin,omitempty"`
	RequestCount       int64  `json:"request_count"`
}

type RefundApplication struct {
	ID                        string     `json:"id"`
	ApplicationNumber         string     `json:"application_number"`
	OrganizationID            string     `json:"organization_id"`
	SourceType                string     `json:"source_type"`
	RechargeOrderID           *string    `json:"recharge_order_id,omitempty"`
	SubscriptionInvoiceID     *string    `json:"subscription_invoice_id,omitempty"`
	RequestedAmount           string     `json:"requested_amount"`
	Currency                  string     `json:"currency"`
	UnusedCashAmount          string     `json:"unused_cash_amount"`
	UsedServiceAmount         string     `json:"used_service_amount"`
	BonusAmount               string     `json:"bonus_amount"`
	SubscriptionFeeAmount     string     `json:"subscription_fee_amount"`
	ProviderIrrecoverableCost string     `json:"provider_irrecoverable_cost"`
	Reason                    string     `json:"reason"`
	IdempotencyKey            string     `json:"idempotency_key,omitempty"`
	Status                    string     `json:"status"`
	RefundOrderID             *string    `json:"refund_order_id,omitempty"`
	RequestedBy               string     `json:"requested_by"`
	ReviewedBy                *string    `json:"reviewed_by,omitempty"`
	ReviewReason              string     `json:"review_reason,omitempty"`
	ReviewedAt                *time.Time `json:"reviewed_at,omitempty"`
	CompletedAt               *time.Time `json:"completed_at,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
}

type InvoiceApplication struct {
	ID                string     `json:"id"`
	ApplicationNumber string     `json:"application_number"`
	OrganizationID    string     `json:"organization_id"`
	InvoiceTitle      string     `json:"invoice_title"`
	TaxIdentifier     string     `json:"tax_identifier,omitempty"`
	Amount            string     `json:"amount"`
	Currency          string     `json:"currency"`
	PeriodStart       string     `json:"period_start"`
	PeriodEnd         string     `json:"period_end"`
	Status            string     `json:"status"`
	IdempotencyKey    string     `json:"idempotency_key,omitempty"`
	RequestedBy       string     `json:"requested_by"`
	ProcessedBy       *string    `json:"processed_by,omitempty"`
	ProcessingReason  string     `json:"processing_reason,omitempty"`
	ProcessedAt       *time.Time `json:"processed_at,omitempty"`
	ExportedAt        *time.Time `json:"exported_at,omitempty"`
	ExportBatchID     *string    `json:"export_batch_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type ReconciliationRun struct {
	ID            string         `json:"id"`
	RunKey        string         `json:"run_key"`
	BusinessDate  string         `json:"business_date"`
	Status        string         `json:"status"`
	TriggerSource string         `json:"trigger_source"`
	Summary       map[string]any `json:"summary"`
	StartedBy     *string        `json:"started_by,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	ErrorCode     string         `json:"error_code,omitempty"`
}

type ReconciliationCase struct {
	ID                    string         `json:"id"`
	CaseKey               string         `json:"case_key"`
	CheckType             string         `json:"check_type"`
	Classification        string         `json:"classification"`
	Severity              string         `json:"severity"`
	Status                string         `json:"status"`
	OrganizationID        *string        `json:"organization_id,omitempty"`
	ProviderID            *string        `json:"provider_id,omitempty"`
	RechargeOrderID       *string        `json:"recharge_order_id,omitempty"`
	FundingOperationID    *string        `json:"funding_operation_id,omitempty"`
	SubscriptionInvoiceID *string        `json:"subscription_invoice_id,omitempty"`
	ExpectedAmount        *string        `json:"expected_amount,omitempty"`
	ActualAmount          *string        `json:"actual_amount,omitempty"`
	Currency              string         `json:"currency,omitempty"`
	Details               map[string]any `json:"details"`
	OccurrenceCount       int64          `json:"occurrence_count"`
	HandledBy             *string        `json:"handled_by,omitempty"`
	HandlingReason        string         `json:"handling_reason,omitempty"`
	HandledAt             *time.Time     `json:"handled_at,omitempty"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type LedgerAccount struct {
	ID          string    `json:"id"`
	WalletID    *string   `json:"wallet_id,omitempty"`
	AccountKey  string    `json:"account_key"`
	Name        string    `json:"name"`
	AccountType string    `json:"account_type"`
	NormalSide  string    `json:"normal_side"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type JournalEntry struct {
	ID          string    `json:"id"`
	JournalID   string    `json:"journal_id"`
	AccountID   string    `json:"account_id"`
	AccountKey  string    `json:"account_key,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	Currency    string    `json:"currency"`
	EntrySide   string    `json:"entry_side"`
	Amount      Decimal   `json:"amount"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Journal struct {
	ID          string         `json:"id"`
	WalletID    *string        `json:"wallet_id,omitempty"`
	JournalType string         `json:"journal_type"`
	ExternalKey string         `json:"external_key"`
	Currency    string         `json:"currency"`
	Status      string         `json:"status"`
	Reference   string         `json:"reference,omitempty"`
	Metadata    map[string]any `json:"metadata"`
	CreatedBy   *string        `json:"created_by,omitempty"`
	PostedAt    *time.Time     `json:"posted_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Entries     []JournalEntry `json:"entries,omitempty"`
}

type FundingOperation struct {
	ID                      string     `json:"id"`
	WalletID                string     `json:"wallet_id"`
	OrganizationID          string     `json:"organization_id"`
	ProjectID               string     `json:"project_id"`
	APIKeyID                *string    `json:"api_key_id,omitempty"`
	RequestID               string     `json:"request_id"`
	IdempotencyKey          string     `json:"idempotency_key"`
	RequestFingerprint      string     `json:"-"`
	PricingVersionID        *string    `json:"pricing_version_id,omitempty"`
	Status                  string     `json:"status"`
	Currency                string     `json:"currency"`
	MaximumAmount           Decimal    `json:"maximum_amount"`
	PromotionAmount         Decimal    `json:"promotion_amount"`
	ConsumedPromotionAmount Decimal    `json:"consumed_promotion_amount"`
	TaxRate                 Decimal    `json:"tax_rate"`
	ExchangeRate            Decimal    `json:"exchange_rate"`
	SettledAmount           Decimal    `json:"settled_amount"`
	ReleasedAmount          Decimal    `json:"released_amount"`
	EstimatedInputTokens    int64      `json:"estimated_input_tokens"`
	MaxOutputTokens         int64      `json:"max_output_tokens"`
	ActualInputTokens       *int64     `json:"actual_input_tokens,omitempty"`
	ActualCachedInputTokens *int64     `json:"actual_cached_input_tokens,omitempty"`
	ActualOutputTokens      *int64     `json:"actual_output_tokens,omitempty"`
	UsageSource             string     `json:"usage_source,omitempty"`
	ObservedOutputBytes     int64      `json:"observed_output_bytes"`
	FailureCode             string     `json:"failure_code,omitempty"`
	ReservedAt              time.Time  `json:"reserved_at"`
	SettledAt               *time.Time `json:"settled_at,omitempty"`
	ReleasedAt              *time.Time `json:"released_at,omitempty"`
	HeartbeatAt             time.Time  `json:"heartbeat_at"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
	CredentialOwner         string     `json:"credential_owner"`
	ProviderCredentialID    *string    `json:"provider_credential_id,omitempty"`
	PlatformServiceFee      Decimal    `json:"platform_service_fee"`
}

type FundingProviderAttempt struct {
	ID                string     `json:"id"`
	OperationID       string     `json:"operation_id"`
	AttemptNo         int        `json:"attempt_no"`
	ProviderID        string     `json:"provider_id"`
	CredentialID      *string    `json:"credential_id,omitempty"`
	CredentialGroupID *string    `json:"credential_group_id,omitempty"`
	IsFallback        bool       `json:"is_fallback"`
	Status            string     `json:"status"`
	HTTPStatus        *int       `json:"http_status,omitempty"`
	UpstreamRequestID string     `json:"upstream_request_id,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	StartedAt         time.Time  `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

type WalletTransaction struct {
	ID              string         `json:"id"`
	WalletID        string         `json:"wallet_id"`
	UsageRecordID   *string        `json:"usage_record_id,omitempty"`
	TransactionType string         `json:"transaction_type"`
	Amount          Decimal        `json:"amount"`
	BalanceAfter    Decimal        `json:"balance_after"`
	IdempotencyKey  string         `json:"idempotency_key,omitempty"`
	Reference       string         `json:"reference,omitempty"`
	Metadata        map[string]any `json:"metadata"`
	CreatedBy       *string        `json:"created_by,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

// RechargeOrder is the durable local view of one externally verified payment.
// Amount remains an exact PostgreSQL NUMERIC value throughout the API boundary.
type RechargeOrder struct {
	ID                  string         `json:"id"`
	PlatformOrderNo     string         `json:"platform_order_no"`
	OrganizationID      string         `json:"organization_id"`
	WalletID            string         `json:"wallet_id"`
	CreatedBy           *string        `json:"created_by,omitempty"`
	PaymentProvider     string         `json:"payment_provider"`
	ProviderOrderNo     string         `json:"provider_order_no,omitempty"`
	Status              string         `json:"status"`
	Amount              Decimal        `json:"amount"`
	Currency            string         `json:"currency"`
	Region              string         `json:"region"`
	IdempotencyKey      string         `json:"idempotency_key"`
	WalletTransactionID *string        `json:"wallet_transaction_id,omitempty"`
	LedgerJournalID     *string        `json:"ledger_journal_id,omitempty"`
	ExpiresAt           time.Time      `json:"expires_at"`
	PaidAt              *time.Time     `json:"paid_at,omitempty"`
	CreditedAt          *time.Time     `json:"credited_at,omitempty"`
	ProviderClosedAt    *time.Time     `json:"provider_closed_at,omitempty"`
	FailureCode         string         `json:"failure_code,omitempty"`
	Metadata            map[string]any `json:"metadata"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
}

type PaymentAttempt struct {
	ID              string         `json:"id"`
	RechargeOrderID string         `json:"recharge_order_id"`
	AttemptNo       int            `json:"attempt_no"`
	Operation       string         `json:"operation"`
	Status          string         `json:"status"`
	ProviderOrderNo string         `json:"provider_order_no,omitempty"`
	RequestHash     string         `json:"request_hash,omitempty"`
	ResponseCode    string         `json:"response_code,omitempty"`
	ErrorCode       string         `json:"error_code,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      *time.Time     `json:"finished_at,omitempty"`
	Metadata        map[string]any `json:"metadata"`
}

type PaymentWebhookEvent struct {
	ID                string         `json:"id"`
	PaymentProvider   string         `json:"payment_provider"`
	ProviderEventID   string         `json:"provider_event_id"`
	ProviderOrderNo   string         `json:"provider_order_no"`
	RechargeOrderID   *string        `json:"recharge_order_id,omitempty"`
	EventType         string         `json:"event_type"`
	PaymentStatus     string         `json:"payment_status"`
	Amount            Decimal        `json:"amount"`
	Currency          string         `json:"currency"`
	ProviderTimestamp time.Time      `json:"provider_timestamp"`
	RawBodySHA256     string         `json:"raw_body_sha256"`
	ProcessingStatus  string         `json:"processing_status"`
	ErrorCode         string         `json:"error_code,omitempty"`
	NormalizedPayload map[string]any `json:"normalized_payload"`
	ReceivedAt        time.Time      `json:"received_at"`
	ProcessedAt       *time.Time     `json:"processed_at,omitempty"`
}

type RefundOrder struct {
	ID                  string     `json:"id"`
	PlatformRefundNo    string     `json:"platform_refund_no"`
	RechargeOrderID     string     `json:"recharge_order_id"`
	RefundApplicationID *string    `json:"refund_application_id,omitempty"`
	PaymentProvider     string     `json:"payment_provider"`
	ProviderRefundNo    string     `json:"provider_refund_no,omitempty"`
	Status              string     `json:"status"`
	Amount              Decimal    `json:"amount"`
	Currency            string     `json:"currency"`
	Reason              string     `json:"reason"`
	IdempotencyKey      string     `json:"idempotency_key"`
	WalletTransactionID *string    `json:"wallet_transaction_id,omitempty"`
	LedgerJournalID     *string    `json:"ledger_journal_id,omitempty"`
	CreatedBy           *string    `json:"created_by,omitempty"`
	FailureCode         string     `json:"failure_code,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
}

type PaymentReconciliationRecord struct {
	ID                string         `json:"id"`
	RechargeOrderID   *string        `json:"recharge_order_id,omitempty"`
	RefundOrderID     *string        `json:"refund_order_id,omitempty"`
	PaymentProvider   string         `json:"payment_provider"`
	ProviderOrderNo   string         `json:"provider_order_no,omitempty"`
	ReconciliationKey string         `json:"reconciliation_key"`
	ProviderStatus    string         `json:"provider_status"`
	LocalStatus       string         `json:"local_status"`
	ProviderAmount    *Decimal       `json:"provider_amount,omitempty"`
	LocalAmount       *Decimal       `json:"local_amount,omitempty"`
	Currency          string         `json:"currency"`
	Result            string         `json:"result"`
	Details           map[string]any `json:"details"`
	ReconciledBy      *string        `json:"reconciled_by,omitempty"`
	ReconciledAt      time.Time      `json:"reconciled_at"`
}

type ProjectBudgetPolicy struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organization_id,omitempty"`
	ProjectID        string    `json:"project_id"`
	Name             string    `json:"name"`
	Period           string    `json:"period"`
	TokenLimit       *int64    `json:"token_limit,omitempty"`
	CostLimit        *float64  `json:"cost_limit,omitempty"`
	AlertThreshold   float64   `json:"alert_threshold"`
	EnforceHardLimit bool      `json:"enforce_hard_limit"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ProjectBudgetUsage struct {
	OrganizationID string    `json:"organization_id"`
	ProjectID      string    `json:"project_id"`
	Period         string    `json:"period"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Requests       int64     `json:"requests"`
	InputTokens    int64     `json:"input_tokens"`
	CachedTokens   int64     `json:"cached_input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	TotalTokens    int64     `json:"total_tokens"`
	Cost           float64   `json:"cost"`
	Errors         int64     `json:"errors"`
}

type BudgetEvent struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id"`
	ProjectID      string         `json:"project_id"`
	PolicyID       *string        `json:"policy_id,omitempty"`
	UserID         *string        `json:"user_id,omitempty"`
	APIKeyID       *string        `json:"api_key_id,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	EventType      string         `json:"event_type"`
	Tokens         int64          `json:"tokens"`
	Cost           float64        `json:"cost"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Metadata       map[string]any `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
}

type APIKeyVersion struct {
	ID             string     `json:"id"`
	APIKeyID       string     `json:"api_key_id"`
	Version        int        `json:"version"`
	KeyPrefix      string     `json:"key_prefix"`
	KeyHash        []byte     `json:"-"`
	Status         string     `json:"status"`
	ValidFrom      time.Time  `json:"valid_from"`
	GraceExpiresAt *time.Time `json:"grace_expires_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
}

type APIKeyAuthentication struct {
	Key     APIKey        `json:"key"`
	Version APIKeyVersion `json:"version"`
}

type WebhookEndpoint struct {
	ID              string     `json:"id"`
	OrganizationID  string     `json:"organization_id"`
	ProjectID       string     `json:"project_id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	EncryptedSecret []byte     `json:"-"`
	SecretLast4     string     `json:"secret_last4"`
	EventTypes      []string   `json:"event_types"`
	Enabled         bool       `json:"enabled"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastDeliveryAt  *time.Time `json:"last_delivery_at,omitempty"`
}

type WebhookOutbox struct {
	ID              string         `json:"id"`
	EndpointID      string         `json:"endpoint_id"`
	OrganizationID  string         `json:"organization_id"`
	ProjectID       string         `json:"project_id"`
	EventID         string         `json:"event_id"`
	EventType       string         `json:"event_type"`
	Payload         map[string]any `json:"payload"`
	Status          string         `json:"status"`
	Attempts        int            `json:"attempts"`
	MaxAttempts     int            `json:"max_attempts"`
	AvailableAt     time.Time      `json:"available_at"`
	LockedAt        *time.Time     `json:"locked_at,omitempty"`
	LockedUntil     *time.Time     `json:"locked_until,omitempty"`
	LockedBy        string         `json:"locked_by,omitempty"`
	ClaimToken      string         `json:"claim_token,omitempty"`
	DeliveredAt     *time.Time     `json:"delivered_at,omitempty"`
	LastHTTPStatus  *int           `json:"last_http_status,omitempty"`
	LastResponse    string         `json:"last_response,omitempty"`
	LastError       string         `json:"last_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	EndpointURL     string         `json:"endpoint_url,omitempty"`
	EncryptedSecret []byte         `json:"-"`
}

type UsageExportFilter struct {
	OrganizationID string
	ProjectID      string
	UserID         string
	APIKeyID       string
	RouteID        string
	Model          string
	StatusCode     *int
	From           time.Time
	To             time.Time
	Limit          int
}

type UsageExportRow struct {
	RequestID      string    `json:"request_id"`
	OrganizationID string    `json:"organization_id"`
	ProjectID      string    `json:"project_id"`
	UserID         string    `json:"user_id"`
	APIKeyID       string    `json:"api_key_id"`
	RouteID        string    `json:"route_id"`
	Model          string    `json:"model"`
	Endpoint       string    `json:"endpoint"`
	StatusCode     int       `json:"status_code"`
	InputTokens    int64     `json:"input_tokens"`
	CachedTokens   int64     `json:"cached_input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	TotalTokens    int64     `json:"total_tokens"`
	EstimatedCost  float64   `json:"estimated_cost"`
	LatencyMS      int64     `json:"latency_ms"`
	CreatedAt      time.Time `json:"created_at"`
}

// Commercial pricing values are decimal strings on the Go/API boundary. They
// map to PostgreSQL NUMERIC and must not be converted to float64.
type ProviderCostPriceBook struct {
	ID                   string     `json:"id"`
	ProviderID           string     `json:"provider_id"`
	ModelID              string     `json:"model_id"`
	InputTokenCost       string     `json:"input_token_cost"`
	CachedInputTokenCost string     `json:"cached_input_token_cost"`
	OutputTokenCost      string     `json:"output_token_cost"`
	RequestFixedCost     string     `json:"request_fixed_cost"`
	Currency             string     `json:"currency"`
	Unit                 int64      `json:"unit"`
	EffectiveAt          time.Time  `json:"effective_at"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	Source               string     `json:"source"`
	CreatedBy            *string    `json:"created_by,omitempty"`
	ApprovalStatus       string     `json:"approval_status"`
	CreatedAt            time.Time  `json:"created_at"`
}

type ProviderCostChangeRequest struct {
	ID                   string         `json:"id"`
	IdempotencyKey       string         `json:"idempotency_key"`
	ProviderID           string         `json:"provider_id"`
	ModelID              string         `json:"model_id"`
	SourceType           string         `json:"source_type"`
	SourceReference      string         `json:"source_reference"`
	InputTokenCost       string         `json:"input_token_cost"`
	CachedInputTokenCost string         `json:"cached_input_token_cost"`
	OutputTokenCost      string         `json:"output_token_cost"`
	RequestFixedCost     string         `json:"request_fixed_cost"`
	Currency             string         `json:"currency"`
	Unit                 int64          `json:"unit"`
	EffectiveAt          time.Time      `json:"effective_at"`
	ExpiresAt            *time.Time     `json:"expires_at,omitempty"`
	Status               string         `json:"status"`
	PreviousPriceBookID  *string        `json:"previous_price_book_id,omitempty"`
	PublishedPriceBookID *string        `json:"published_price_book_id,omitempty"`
	ChangeSummary        map[string]any `json:"change_summary"`
	RequestedBy          *string        `json:"requested_by,omitempty"`
	ReviewedBy           *string        `json:"reviewed_by,omitempty"`
	ReviewReason         string         `json:"review_reason,omitempty"`
	ReviewedAt           *time.Time     `json:"reviewed_at,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

// BYOKServiceFeePolicy is append-only pricing for the platform service that
// wraps a customer-owned Provider credential. Monetary values remain decimal
// strings until PostgreSQL stores them as NUMERIC.
type BYOKServiceFeePolicy struct {
	ID                  string     `json:"id"`
	OrganizationID      *string    `json:"organization_id,omitempty"`
	ProviderID          *string    `json:"provider_id,omitempty"`
	FixedFee            string     `json:"fixed_fee"`
	InputTokenFee       string     `json:"input_token_fee"`
	CachedInputTokenFee string     `json:"cached_input_token_fee"`
	OutputTokenFee      string     `json:"output_token_fee"`
	Currency            string     `json:"currency"`
	Unit                int64      `json:"unit"`
	EffectiveAt         time.Time  `json:"effective_at"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	Enabled             bool       `json:"enabled"`
	CreatedBy           *string    `json:"created_by,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type CustomerRetailPriceBook struct {
	ID                    string     `json:"id"`
	OrganizationID        *string    `json:"organization_id,omitempty"`
	ProviderID            string     `json:"provider_id"`
	ModelID               string     `json:"model_id"`
	InputTokenPrice       string     `json:"input_token_price"`
	CachedInputTokenPrice string     `json:"cached_input_token_price"`
	OutputTokenPrice      string     `json:"output_token_price"`
	RequestFixedPrice     string     `json:"request_fixed_price"`
	Currency              string     `json:"currency"`
	Unit                  int64      `json:"unit"`
	EffectiveAt           time.Time  `json:"effective_at"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	Source                string     `json:"source"`
	CreatedBy             *string    `json:"created_by,omitempty"`
	ApprovalStatus        string     `json:"approval_status"`
	CreatedAt             time.Time  `json:"created_at"`
}

type OrganizationPricePlan struct {
	ID                    string     `json:"id"`
	OrganizationID        string     `json:"organization_id"`
	Name                  string     `json:"name"`
	PlanType              string     `json:"plan_type"`
	ProviderID            string     `json:"provider_id"`
	ModelID               string     `json:"model_id"`
	InputTokenPrice       string     `json:"input_token_price"`
	CachedInputTokenPrice string     `json:"cached_input_token_price"`
	OutputTokenPrice      string     `json:"output_token_price"`
	RequestFixedPrice     string     `json:"request_fixed_price"`
	Currency              string     `json:"currency"`
	Unit                  int64      `json:"unit"`
	EffectiveAt           time.Time  `json:"effective_at"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	Source                string     `json:"source"`
	CreatedBy             *string    `json:"created_by,omitempty"`
	ApprovalStatus        string     `json:"approval_status"`
	CreatedAt             time.Time  `json:"created_at"`
}

type PricingMarginPolicy struct {
	ID                  string  `json:"id"`
	OrganizationID      *string `json:"organization_id,omitempty"`
	ProviderID          *string `json:"provider_id,omitempty"`
	ModelID             *string `json:"model_id,omitempty"`
	MinimumMarginAmount string  `json:"minimum_margin_amount"`
	MinimumMarginBPS    int64   `json:"minimum_margin_bps"`
	Enabled             bool    `json:"enabled"`
	CreatedBy           *string `json:"created_by,omitempty"`
}

type ModelPriceVersion struct {
	ID                        string     `json:"id"`
	ProviderID                string     `json:"provider_id"`
	ModelID                   string     `json:"model_id"`
	OrganizationID            *string    `json:"organization_id,omitempty"`
	ProviderCostPriceBookID   *string    `json:"provider_cost_price_book_id,omitempty"`
	CustomerRetailPriceBookID *string    `json:"customer_retail_price_book_id,omitempty"`
	OrganizationPricePlanID   *string    `json:"organization_price_plan_id,omitempty"`
	Version                   int64      `json:"version"`
	ProviderInputTokenCost    string     `json:"provider_input_token_cost"`
	ProviderCachedInputCost   string     `json:"provider_cached_input_token_cost"`
	ProviderOutputTokenCost   string     `json:"provider_output_token_cost"`
	ProviderRequestFixedCost  string     `json:"provider_request_fixed_cost"`
	RetailInputTokenPrice     string     `json:"retail_input_token_price"`
	RetailCachedInputPrice    string     `json:"retail_cached_input_token_price"`
	RetailOutputTokenPrice    string     `json:"retail_output_token_price"`
	RetailRequestFixedPrice   string     `json:"retail_request_fixed_price"`
	ProviderCurrency          string     `json:"provider_currency"`
	RetailCurrency            string     `json:"retail_currency"`
	ProviderUnit              int64      `json:"provider_unit"`
	RetailUnit                int64      `json:"retail_unit"`
	EffectiveAt               time.Time  `json:"effective_at"`
	ExpiresAt                 *time.Time `json:"expires_at,omitempty"`
	Source                    string     `json:"source"`
	ApprovalStatus            string     `json:"approval_status"`
	CreatedAt                 time.Time  `json:"created_at"`
}

type PricingQuote struct {
	ID                         string    `json:"id"`
	OrganizationID             string    `json:"organization_id"`
	ProviderID                 string    `json:"provider_id"`
	ModelID                    string    `json:"model_id"`
	Model                      string    `json:"model"`
	EstimatedInputTokens       int64     `json:"estimated_input_tokens"`
	EstimatedCachedInputTokens int64     `json:"estimated_cached_input_tokens"`
	EstimatedOutputTokens      int64     `json:"estimated_output_tokens"`
	PricingVersionID           string    `json:"pricing_version_id"`
	ProviderCostAmount         string    `json:"provider_cost_amount"`
	RetailAmount               string    `json:"retail_amount"`
	PromotionAmount            string    `json:"promotion_amount"`
	Currency                   string    `json:"currency"`
	ExchangeRate               string    `json:"exchange_rate"`
	GrossMargin                string    `json:"gross_margin"`
	PreTaxAmount               string    `json:"pre_tax_amount"`
	TaxRate                    string    `json:"tax_rate"`
	TaxAmount                  string    `json:"tax_amount"`
	FinalAmount                string    `json:"final_amount"`
	ExpiresAt                  time.Time `json:"expires_at"`
	CreatedAt                  time.Time `json:"created_at"`
}

type UsagePriceSnapshot struct {
	ID                       string    `json:"id"`
	RequestID                string    `json:"request_id"`
	PricingVersionID         string    `json:"pricing_version_id"`
	ProviderID               string    `json:"provider_id"`
	ModelID                  string    `json:"model_id,omitempty"`
	InputTokens              int64     `json:"input_tokens"`
	CachedInputTokens        int64     `json:"cached_input_tokens"`
	OutputTokens             int64     `json:"output_tokens"`
	ProviderInputTokenCost   string    `json:"provider_input_token_cost"`
	ProviderCachedInputCost  string    `json:"provider_cached_input_token_cost"`
	ProviderOutputTokenCost  string    `json:"provider_output_token_cost"`
	ProviderRequestFixedCost string    `json:"provider_request_fixed_cost"`
	RetailInputTokenPrice    string    `json:"retail_input_token_price"`
	RetailCachedInputPrice   string    `json:"retail_cached_input_token_price"`
	RetailOutputTokenPrice   string    `json:"retail_output_token_price"`
	RetailRequestFixedPrice  string    `json:"retail_request_fixed_price"`
	ProviderUnit             int64     `json:"provider_unit"`
	RetailUnit               int64     `json:"retail_unit"`
	ProviderCostAmount       string    `json:"provider_cost_amount"`
	ProviderCurrency         string    `json:"provider_currency"`
	CustomerSaleAmount       string    `json:"customer_sale_amount"`
	CustomerCurrency         string    `json:"customer_currency"`
	ExchangeRate             string    `json:"exchange_rate"`
	PlatformGrossMargin      string    `json:"platform_gross_margin"`
	PromotionAmount          string    `json:"promotion_amount"`
	PreTaxAmount             string    `json:"pre_tax_amount"`
	TaxRate                  string    `json:"tax_rate"`
	TaxAmount                string    `json:"tax_amount"`
	FinalUserAmount          string    `json:"final_user_amount"`
	PricingRuleVersion       string    `json:"pricing_rule_version"`
	SettledAt                time.Time `json:"settled_at"`
	CredentialOwner          string    `json:"credential_owner"`
	PlatformServiceFee       string    `json:"platform_service_fee"`
	BYOKServiceFeePolicyID   *string   `json:"byok_service_fee_policy_id,omitempty"`
}

type PromotionCredit struct {
	ID              string     `json:"id"`
	OrganizationID  string     `json:"organization_id"`
	Currency        string     `json:"currency"`
	AmountGranted   string     `json:"amount_granted"`
	AmountRemaining string     `json:"amount_remaining"`
	IdempotencyKey  string     `json:"idempotency_key,omitempty"`
	Source          string     `json:"source"`
	NonRefundable   bool       `json:"non_refundable"`
	Status          string     `json:"status"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CreatedBy       *string    `json:"created_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Subscription prices are decimal strings on the API boundary and PostgreSQL
// NUMERIC values in storage. Token usage is intentionally absent: it is always
// settled by the independent usage pricing and wallet ledger paths.
type SubscriptionPlan struct {
	ID               string         `json:"id"`
	Slug             string         `json:"slug"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	PlanKind         string         `json:"plan_kind"`
	Enabled          bool           `json:"enabled"`
	CurrentVersionID *string        `json:"current_version_id,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	CreatedBy        *string        `json:"created_by,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type PlanEntitlement struct {
	ID             string    `json:"id"`
	PlanVersionID  string    `json:"plan_version_id"`
	EntitlementKey string    `json:"entitlement_key"`
	ValueType      string    `json:"value_type"`
	IntegerValue   *int64    `json:"integer_value,omitempty"`
	BooleanValue   *bool     `json:"boolean_value,omitempty"`
	StringValue    *string   `json:"string_value,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type PlanVersion struct {
	ID                 string            `json:"id"`
	SubscriptionPlanID string            `json:"subscription_plan_id"`
	Version            int               `json:"version"`
	Status             string            `json:"status"`
	BillingInterval    string            `json:"billing_interval"`
	SubscriptionFee    Decimal           `json:"subscription_fee"`
	Currency           string            `json:"currency"`
	TrialDays          int               `json:"trial_days"`
	GracePeriodDays    int               `json:"grace_period_days"`
	TokenBillingMode   string            `json:"token_billing_mode"`
	EnterpriseContract bool              `json:"enterprise_contract"`
	EffectiveAt        time.Time         `json:"effective_at"`
	FrozenAt           *time.Time        `json:"frozen_at,omitempty"`
	RetiredAt          *time.Time        `json:"retired_at,omitempty"`
	Metadata           map[string]any    `json:"metadata"`
	CreatedBy          *string           `json:"created_by,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	Entitlements       []PlanEntitlement `json:"entitlements"`
}

type OrganizationSubscription struct {
	ID                   string            `json:"id"`
	OrganizationID       string            `json:"organization_id"`
	PlanVersionID        string            `json:"plan_version_id"`
	PendingPlanVersionID *string           `json:"pending_plan_version_id,omitempty"`
	PlanSlug             string            `json:"plan_slug,omitempty"`
	PlanName             string            `json:"plan_name,omitempty"`
	PlanVersion          int               `json:"plan_version,omitempty"`
	Status               string            `json:"status"`
	CurrentPeriodStart   time.Time         `json:"current_period_start"`
	CurrentPeriodEnd     time.Time         `json:"current_period_end"`
	GracePeriodEnd       *time.Time        `json:"grace_period_end,omitempty"`
	CancelAtPeriodEnd    bool              `json:"cancel_at_period_end"`
	CanceledAt           *time.Time        `json:"canceled_at,omitempty"`
	EndedAt              *time.Time        `json:"ended_at,omitempty"`
	ContractReference    string            `json:"contract_reference,omitempty"`
	ContractStartsAt     *time.Time        `json:"contract_starts_at,omitempty"`
	ContractEndsAt       *time.Time        `json:"contract_ends_at,omitempty"`
	CouponID             *string           `json:"coupon_id,omitempty"`
	Version              int64             `json:"version"`
	Metadata             map[string]any    `json:"metadata"`
	CreatedBy            *string           `json:"created_by,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	Entitlements         []PlanEntitlement `json:"entitlements,omitempty"`
}

type SubscriptionInvoice struct {
	ID                         string         `json:"id"`
	InvoiceNumber              string         `json:"invoice_number"`
	OrganizationID             string         `json:"organization_id"`
	OrganizationSubscriptionID string         `json:"organization_subscription_id"`
	PlanVersionID              string         `json:"plan_version_id"`
	CouponID                   *string        `json:"coupon_id,omitempty"`
	InvoiceType                string         `json:"invoice_type"`
	Status                     string         `json:"status"`
	Subtotal                   Decimal        `json:"subtotal"`
	DiscountAmount             Decimal        `json:"discount_amount"`
	TaxAmount                  Decimal        `json:"tax_amount"`
	TotalAmount                Decimal        `json:"total_amount"`
	Currency                   string         `json:"currency"`
	PeriodStart                time.Time      `json:"period_start"`
	PeriodEnd                  time.Time      `json:"period_end"`
	DueAt                      time.Time      `json:"due_at"`
	PaidAt                     *time.Time     `json:"paid_at,omitempty"`
	FailedAt                   *time.Time     `json:"failed_at,omitempty"`
	PaymentProvider            string         `json:"payment_provider,omitempty"`
	ProviderPaymentReference   string         `json:"provider_payment_reference,omitempty"`
	LedgerJournalID            *string        `json:"ledger_journal_id,omitempty"`
	PlanSnapshot               map[string]any `json:"plan_snapshot"`
	CreatedBy                  *string        `json:"created_by,omitempty"`
	CreatedAt                  time.Time      `json:"created_at"`
	UpdatedAt                  time.Time      `json:"updated_at"`
}

type SubscriptionEvent struct {
	ID                         string         `json:"id"`
	OrganizationID             string         `json:"organization_id"`
	OrganizationSubscriptionID *string        `json:"organization_subscription_id,omitempty"`
	EventType                  string         `json:"event_type"`
	FromStatus                 string         `json:"from_status,omitempty"`
	ToStatus                   string         `json:"to_status,omitempty"`
	ActorID                    *string        `json:"actor_id,omitempty"`
	Payload                    map[string]any `json:"payload"`
	CreatedAt                  time.Time      `json:"created_at"`
}

type Trial struct {
	ID                         string     `json:"id"`
	OrganizationSubscriptionID string     `json:"organization_subscription_id"`
	OrganizationID             string     `json:"organization_id"`
	PlanVersionID              string     `json:"plan_version_id"`
	Status                     string     `json:"status"`
	StartsAt                   time.Time  `json:"starts_at"`
	EndsAt                     time.Time  `json:"ends_at"`
	ConvertedAt                *time.Time `json:"converted_at,omitempty"`
	CreatedAt                  time.Time  `json:"created_at"`
}

type Coupon struct {
	ID             string         `json:"id"`
	Code           string         `json:"code"`
	Name           string         `json:"name"`
	DiscountType   string         `json:"discount_type"`
	PercentBPS     *int           `json:"percent_bps,omitempty"`
	FixedAmount    *Decimal       `json:"fixed_amount,omitempty"`
	Currency       *string        `json:"currency,omitempty"`
	MaxRedemptions *int           `json:"max_redemptions,omitempty"`
	RedeemedCount  int            `json:"redeemed_count"`
	StartsAt       time.Time      `json:"starts_at"`
	ExpiresAt      *time.Time     `json:"expires_at,omitempty"`
	Enabled        bool           `json:"enabled"`
	Metadata       map[string]any `json:"metadata"`
	CreatedBy      *string        `json:"created_by,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type EffectiveEntitlements struct {
	OrganizationID      string `json:"organization_id"`
	SubscriptionID      string `json:"subscription_id"`
	PlanVersionID       string `json:"plan_version_id"`
	PlanSlug            string `json:"plan_slug"`
	SubscriptionStatus  string `json:"subscription_status"`
	APIKeyCount         int64  `json:"api_key_count"`
	OrganizationMembers int64  `json:"organization_member_count"`
	Concurrency         int64  `json:"concurrency"`
	RequestsPerMinute   int64  `json:"requests_per_minute"`
	LogRetentionDays    int64  `json:"log_retention_days"`
	AdvancedRouting     bool   `json:"advanced_routing"`
	CostAnalysis        bool   `json:"cost_analysis"`
	CustomBudget        bool   `json:"custom_budget"`
	WebhookCount        int64  `json:"webhook_count"`
	PrioritySupport     bool   `json:"priority_support"`
	SLALevel            string `json:"sla_level"`
	TokenBillingMode    string `json:"token_billing_mode"`
}
