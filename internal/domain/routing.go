package domain

import "time"

// RequestProviderPolicy is the normalized RelayDock routing contract. Account
// and workspace defaults are merged before request values; request values may
// narrow an inherited privacy/region policy but never relax it.
type RequestProviderPolicy struct {
	Order                  []string `json:"order,omitempty"`
	Only                   []string `json:"only,omitempty"`
	Ignore                 []string `json:"ignore,omitempty"`
	AllowFallbacks         *bool    `json:"allow_fallbacks,omitempty"`
	RequireParameters      bool     `json:"require_parameters,omitempty"`
	DataCollection         string   `json:"data_collection,omitempty"`
	ZDR                    bool     `json:"zdr,omitempty"`
	Quantizations          []string `json:"quantizations,omitempty"`
	Sort                   string   `json:"sort,omitempty"`
	PreferredMinThroughput *Decimal `json:"preferred_min_throughput,omitempty"`
	PreferredMaxLatencyMS  *int64   `json:"preferred_max_latency_ms,omitempty"`
	MaxInputPrice          *Decimal `json:"max_input_price,omitempty"`
	MaxOutputPrice         *Decimal `json:"max_output_price,omitempty"`
	RequiredCapabilities   []string `json:"required_capabilities,omitempty"`
	ProcessingRegions      []string `json:"processing_regions,omitempty"`
	UseSharedCapacity      *bool    `json:"use_shared_capacity,omitempty"`
}

type RoutingCandidateTrace struct {
	ProviderID    string  `json:"provider_id"`
	ProviderSlug  string  `json:"provider_slug"`
	Model         string  `json:"model"`
	Eligible      bool    `json:"eligible"`
	Reason        string  `json:"reason"`
	Score         float64 `json:"score,omitempty"`
	InputPrice    string  `json:"input_price,omitempty"`
	OutputPrice   string  `json:"output_price,omitempty"`
	LatencyMS     *int64  `json:"latency_ms,omitempty"`
	ThroughputTPS string  `json:"throughput_tps,omitempty"`
}

type WorkspaceSettings struct {
	ProjectID                string                `json:"project_id"`
	DefaultProviderPolicy    RequestProviderPolicy `json:"default_provider_policy"`
	PrivacyPolicy            map[string]any        `json:"privacy_policy"`
	ObservabilityConfig      map[string]any        `json:"observability_config"`
	IncludeBYOKInBudgets     bool                  `json:"include_byok_in_budgets"`
	FreeDailyRequestLimit    int                   `json:"free_daily_request_limit"`
	FreeDailyTokenLimit      int64                 `json:"free_daily_token_limit"`
	AllowedProcessingRegions []string              `json:"allowed_processing_regions"`
	CreatedAt                time.Time             `json:"created_at"`
	UpdatedAt                time.Time             `json:"updated_at"`
}

type ProviderCapabilityDocument struct {
	ID            string         `json:"id"`
	ProviderID    string         `json:"provider_id"`
	ProviderName  string         `json:"provider_name,omitempty"`
	SchemaVersion string         `json:"schema_version"`
	Document      map[string]any `json:"document"`
	SourceURL     string         `json:"source_url,omitempty"`
	SourceSHA256  string         `json:"source_sha256"`
	Status        string         `json:"status"`
	FetchedAt     time.Time      `json:"fetched_at"`
	CreatedBy     *string        `json:"created_by,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
}

type EnterpriseIdentityConnection struct {
	ID              string         `json:"id"`
	OrganizationID  string         `json:"organization_id"`
	IssuerURL       string         `json:"issuer_url"`
	ClientID        string         `json:"client_id"`
	HasClientSecret bool           `json:"has_client_secret"`
	HasSCIMToken    bool           `json:"has_scim_token"`
	AllowedDomains  []string       `json:"allowed_domains"`
	SSOEnabled      bool           `json:"sso_enabled"`
	SCIMEnabled     bool           `json:"scim_enabled"`
	EnforceSSO      bool           `json:"enforce_sso"`
	Status          string         `json:"status"`
	Metadata        map[string]any `json:"metadata"`
	CreatedBy       *string        `json:"created_by,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// SCIM records deliberately project organization membership instead of
// global account ownership. One user may therefore be managed by one IdP for
// one organization while retaining independent access elsewhere.
type SCIMUserRecord struct {
	UserID         string    `json:"user_id"`
	OrganizationID string    `json:"organization_id"`
	ExternalID     string    `json:"external_id,omitempty"`
	Email          string    `json:"email"`
	DisplayName    string    `json:"display_name"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SCIMGroupRecord struct {
	TeamID         string    `json:"team_id"`
	OrganizationID string    `json:"organization_id"`
	ExternalID     string    `json:"external_id,omitempty"`
	DisplayName    string    `json:"display_name"`
	MemberIDs      []string  `json:"member_ids"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
