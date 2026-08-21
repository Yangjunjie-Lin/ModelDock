package domain

import "time"

// ProviderQualityPolicy is operator-owned configuration. Provider and supplier
// declaration APIs cannot mutate it, and its configuration_source is fixed by
// the database to PLATFORM_ADMIN.
type ProviderQualityPolicy struct {
	ProviderID                string    `json:"provider_id"`
	Enabled                   bool      `json:"enabled"`
	ProbeModelID              *string   `json:"probe_model_id,omitempty"`
	ProbeIntervalSeconds      int       `json:"probe_interval_seconds"`
	ProbeTimeoutMS            int64     `json:"probe_timeout_ms"`
	EvaluationWindowMinutes   int       `json:"evaluation_window_minutes"`
	MinimumSamples            int       `json:"minimum_samples"`
	AvailabilityTargetPct     Decimal   `json:"availability_target_pct"`
	MaximumErrorRatePct       Decimal   `json:"maximum_error_rate_pct"`
	Maximum429RatePct         Decimal   `json:"maximum_429_rate_pct"`
	MaximumTTFTMS             int64     `json:"maximum_ttft_ms"`
	MaximumFullLatencyMS      int64     `json:"maximum_full_latency_ms"`
	MinimumThroughputTPS      Decimal   `json:"minimum_throughput_tps"`
	MinimumOutputQualityScore Decimal   `json:"minimum_output_quality_score"`
	PriceTruthToleranceBPS    int       `json:"price_truth_tolerance_bps"`
	RequiredTestRegions       []string  `json:"required_test_regions"`
	AutoDownweightEnabled     bool      `json:"auto_downweight_enabled"`
	AutoCircuitBreakerEnabled bool      `json:"auto_circuit_breaker_enabled"`
	CircuitFailureThreshold   int       `json:"circuit_failure_threshold"`
	CircuitRecoveryThreshold  int       `json:"circuit_recovery_threshold"`
	CircuitOpenSeconds        int       `json:"circuit_open_seconds"`
	RampEnabled               bool      `json:"ramp_enabled"`
	RampInitialBPS            int       `json:"ramp_initial_bps"`
	RampStepBPS               int       `json:"ramp_step_bps"`
	RampStepIntervalSeconds   int       `json:"ramp_step_interval_seconds"`
	ConfigurationSource       string    `json:"configuration_source"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type ProviderQualityState struct {
	ProviderID            string     `json:"provider_id"`
	Grade                 string     `json:"grade"`
	QualityScore          Decimal    `json:"quality_score"`
	RoutingMultiplier     Decimal    `json:"routing_multiplier"`
	TrafficCapBPS         int        `json:"traffic_cap_bps"`
	CircuitState          string     `json:"circuit_state"`
	CircuitOpenUntil      *time.Time `json:"circuit_open_until,omitempty"`
	ConsecutiveBreaches   int        `json:"consecutive_breaches"`
	ConsecutiveRecoveries int        `json:"consecutive_recoveries"`
	AvailabilityPct       Decimal    `json:"availability_pct"`
	ErrorRatePct          Decimal    `json:"error_rate_pct"`
	RateLimitedPct        Decimal    `json:"rate_limited_pct"`
	P95TTFTMS             *int64     `json:"p95_ttft_ms,omitempty"`
	P95FullLatencyMS      *int64     `json:"p95_full_latency_ms,omitempty"`
	ThroughputTPS         *Decimal   `json:"throughput_tps,omitempty"`
	OutputQualityScore    Decimal    `json:"output_quality_score"`
	PriceTruthScore       Decimal    `json:"price_truth_score"`
	RegionCoveragePct     Decimal    `json:"region_coverage_pct"`
	MeasurementCount      int64      `json:"measurement_count"`
	RampStartedAt         *time.Time `json:"ramp_started_at,omitempty"`
	LastRampStepAt        *time.Time `json:"last_ramp_step_at,omitempty"`
	LastProbeAt           *time.Time `json:"last_probe_at,omitempty"`
	LastEvaluatedAt       *time.Time `json:"last_evaluated_at,omitempty"`
	StateVersion          int64      `json:"state_version"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type ProviderQualitySummary struct {
	ProviderID   string                `json:"provider_id"`
	ProviderName string                `json:"provider_name"`
	ProviderSlug string                `json:"provider_slug"`
	SupplierID   *string               `json:"supplier_id,omitempty"`
	SupplierName string                `json:"supplier_name,omitempty"`
	Policy       ProviderQualityPolicy `json:"policy"`
	State        ProviderQualityState  `json:"state"`
}

type ProviderQualityObservation struct {
	ID                 string    `json:"id"`
	IdempotencyKey     string    `json:"-"`
	ProviderID         string    `json:"provider_id"`
	CredentialID       *string   `json:"credential_id,omitempty"`
	ProviderAttemptID  *string   `json:"provider_attempt_id,omitempty"`
	Source             string    `json:"source"`
	Region             *string   `json:"region,omitempty"`
	ModelID            *string   `json:"model_id,omitempty"`
	StatusCode         *int      `json:"status_code,omitempty"`
	Succeeded          bool      `json:"succeeded"`
	RateLimited        bool      `json:"rate_limited"`
	TTFTMS             *int64    `json:"ttft_ms,omitempty"`
	FullLatencyMS      *int64    `json:"full_latency_ms,omitempty"`
	InputTokens        *int64    `json:"input_tokens,omitempty"`
	OutputTokens       *int64    `json:"output_tokens,omitempty"`
	ThroughputTPS      *Decimal  `json:"throughput_tps,omitempty"`
	OutputQualityScore *Decimal  `json:"output_quality_score,omitempty"`
	ResponseSHA256     *string   `json:"response_sha256,omitempty"`
	ErrorClass         string    `json:"error_class"`
	ObservedAt         time.Time `json:"observed_at"`
	CreatedAt          time.Time `json:"created_at"`
}

type ProviderQualityProbeJob struct {
	ProviderID          string
	ProviderType        string
	ProviderBaseURL     string
	Region              string
	ProbeModelID        *string
	ProbeModelName      string
	ProbeTimeoutMS      int64
	CredentialID        *string
	EncryptedCredential []byte
	CredentialOrgID     string
	CredentialProjectID string
	LeaseToken          string
}

type ProviderPriceVerification struct {
	ID                           string     `json:"id"`
	IdempotencyKey               string     `json:"-"`
	ProviderID                   string     `json:"provider_id"`
	ModelID                      string     `json:"model_id"`
	PriceBookID                  *string    `json:"price_book_id,omitempty"`
	SourceType                   string     `json:"source_type"`
	SourceReference              string     `json:"source_reference"`
	EvidenceSHA256               string     `json:"evidence_sha256"`
	ObservedInputTokenCost       string     `json:"observed_input_token_cost"`
	ObservedCachedInputTokenCost string     `json:"observed_cached_input_token_cost"`
	ObservedOutputTokenCost      string     `json:"observed_output_token_cost"`
	ObservedRequestFixedCost     string     `json:"observed_request_fixed_cost"`
	Currency                     string     `json:"currency"`
	Unit                         int64      `json:"unit"`
	Result                       string     `json:"result"`
	MaximumDeviationBPS          *int64     `json:"maximum_deviation_bps,omitempty"`
	ObservedAt                   time.Time  `json:"observed_at"`
	ExpiresAt                    *time.Time `json:"expires_at,omitempty"`
	CreatedAt                    time.Time  `json:"created_at"`
}

type ProviderSLAEvent struct {
	ID             string         `json:"id"`
	ProviderID     string         `json:"provider_id"`
	ProviderName   string         `json:"provider_name,omitempty"`
	Metric         string         `json:"metric"`
	Severity       string         `json:"severity"`
	Status         string         `json:"status"`
	ObservedValue  *Decimal       `json:"observed_value,omitempty"`
	ThresholdValue *Decimal       `json:"threshold_value,omitempty"`
	WindowStart    time.Time      `json:"window_start"`
	WindowEnd      time.Time      `json:"window_end"`
	Details        map[string]any `json:"details"`
	StartedAt      time.Time      `json:"started_at"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type SupplierProviderLink struct {
	ProviderID string     `json:"provider_id"`
	SupplierID string     `json:"supplier_id"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason"`
	LinkedAt   time.Time  `json:"linked_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
}
