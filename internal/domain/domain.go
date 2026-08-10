package domain

import "time"

type User struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	PasswordHash      string     `json:"-"`
	DisplayName       string     `json:"display_name"`
	Role              string     `json:"role"`
	Status            string     `json:"status"`
	MonthlyTokenLimit *int64     `json:"monthly_token_limit,omitempty"`
	MonthlyCostLimit  *float64   `json:"monthly_cost_limit,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastLoginAt       *time.Time `json:"last_login_at,omitempty"`
}

type Provider struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Slug             string         `json:"slug"`
	ProviderType     string         `json:"provider_type"`
	BaseURL          string         `json:"base_url"`
	Enabled          bool           `json:"enabled"`
	Config           map[string]any `json:"config"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	CredentialsCount int            `json:"credentials_count"`
}

type Credential struct {
	ID                string     `json:"id"`
	ProviderID        string     `json:"provider_id"`
	ProviderName      string     `json:"provider_name,omitempty"`
	GroupName         string     `json:"group_name,omitempty"`
	Name              string     `json:"name"`
	CredentialType    string     `json:"credential_type"`
	EncryptedSecret   []byte     `json:"-"`
	HasSecret         bool       `json:"has_secret"`
	SecretLast4       string     `json:"secret_last4"`
	OrganizationID    *string    `json:"organization_id,omitempty"`
	ProjectID         *string    `json:"project_id,omitempty"`
	Status            string     `json:"status"`
	Priority          int        `json:"priority"`
	Weight            int        `json:"weight"`
	MaxConcurrency    int        `json:"max_concurrency"`
	CurrentHealth     string     `json:"current_health"`
	LastSuccessAt     *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt     *time.Time `json:"last_failure_at,omitempty"`
	CooldownUntil     *time.Time `json:"cooldown_until,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ActiveRequests    int64      `json:"active_requests"`
	EffectiveWeight   int        `json:"effective_weight,omitempty"`
	EffectivePriority int        `json:"effective_priority,omitempty"`
	Tags              []string   `json:"tags,omitempty"`
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
	ID               string         `json:"id"`
	ProviderID       string         `json:"provider_id"`
	ProviderModelID  string         `json:"provider_model_id"`
	DisplayName      string         `json:"display_name"`
	ModelType        string         `json:"model_type"`
	Enabled          bool           `json:"enabled"`
	Capabilities     []string       `json:"capabilities"`
	CapabilitySource string         `json:"capability_source"`
	ContextWindow    *int           `json:"context_window,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
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
	ID                string     `json:"id"`
	UserID            string     `json:"user_id"`
	OrganizationID    string     `json:"organization_id,omitempty"`
	ProjectID         string     `json:"project_id,omitempty"`
	Name              string     `json:"name"`
	Environment       string     `json:"environment"`
	KeyPrefix         string     `json:"key_prefix"`
	KeyHash           []byte     `json:"-"`
	Status            string     `json:"status"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	RateLimitRPM      int        `json:"rate_limit_rpm"`
	RateLimitTPM      int        `json:"rate_limit_tpm"`
	MonthlyTokenLimit *int64     `json:"monthly_token_limit,omitempty"`
	MonthlyCostLimit  *float64   `json:"monthly_cost_limit,omitempty"`
	AllowedModels     []string   `json:"allowed_models"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	CurrentVersion    int        `json:"current_version,omitempty"`
}

type RequestLog struct {
	RequestID         string         `json:"request_id"`
	UserID            string         `json:"user_id"`
	APIKeyID          string         `json:"api_key_id"`
	OrganizationID    string         `json:"organization_id,omitempty"`
	ProjectID         string         `json:"project_id,omitempty"`
	RouteID           string         `json:"route_id,omitempty"`
	ProviderID        string         `json:"provider_id"`
	CredentialID      string         `json:"credential_id"`
	RequestedModel    string         `json:"requested_model"`
	ResolvedModel     string         `json:"resolved_model"`
	Endpoint          string         `json:"endpoint"`
	StatusCode        int            `json:"status_code"`
	Streaming         bool           `json:"streaming"`
	InputTokens       int64          `json:"input_tokens"`
	CachedInputTokens int64          `json:"cached_input_tokens"`
	OutputTokens      int64          `json:"output_tokens"`
	TotalTokens       int64          `json:"total_tokens"`
	EstimatedCost     float64        `json:"estimated_cost"`
	LatencyMS         int64          `json:"latency_ms"`
	TTFTMS            *int64         `json:"ttft_ms,omitempty"`
	UpstreamRequestID string         `json:"upstream_request_id,omitempty"`
	ErrorCode         string         `json:"error_code,omitempty"`
	SchedulerReason   map[string]any `json:"scheduler_reason"`
	CreatedAt         time.Time      `json:"created_at"`
}

const (
	LegacyOrganizationID = "00000000-0000-4000-8000-000000000001"
	LegacyProjectID      = "00000000-0000-4000-8000-000000000002"
)

type Organization struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Slug      string         `json:"slug"`
	Status    string         `json:"status"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
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
	ProviderBaseURL   string         `json:"provider_base_url,omitempty"`
	UpstreamModel     string         `json:"upstream_model,omitempty"`
	CredentialGroupID string         `json:"credential_group_id,omitempty"`
	FallbackGroupID   *string        `json:"fallback_group_id,omitempty"`
	RoutingPolicy     string         `json:"routing_policy,omitempty"`
	FallbackConfig    map[string]any `json:"fallback_config,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
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
