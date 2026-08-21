package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GatewayAddr                 string
	ControlAddr                 string
	DatabaseURL                 string
	PostgresMaxConns            int32
	PostgresMinConns            int32
	PostgresMaxIdleTime         time.Duration
	PostgresMaxLifetime         time.Duration
	RedisURL                    string
	RedisPoolSize               int
	RedisMinIdleConns           int
	RedisDialTimeout            time.Duration
	RedisReadTimeout            time.Duration
	RedisWriteTimeout           time.Duration
	MasterKey                   []byte
	APIKeyHMACSecret            []byte
	JWTSecret                   []byte
	JWTPreviousSecret           []byte
	JWTLifetime                 time.Duration
	JWTRefreshLifetime          time.Duration
	AdminEmail                  string
	AdminPassword               string
	AdminDisplayName            string
	AllowedOrigins              []string
	TrustedProxies              []string
	MaxBodyBytes                int64
	Cooldown                    time.Duration
	ProviderTimeout             time.Duration
	FundingRecoveryPoll         time.Duration
	FundingStaleAfter           time.Duration
	PaymentOrderTTL             time.Duration
	PaymentPollInterval         time.Duration
	PaymentTimestampSkew        time.Duration
	PaymentAllowedRegions       []string
	PaymentSandboxEnabled       bool
	PaymentSandboxSecret        []byte
	PaymentManualEnabled        bool
	SubscriptionPoll            time.Duration
	ReconciliationInterval      time.Duration
	ReconciliationRunAt         time.Duration
	ShutdownTimeout             time.Duration
	DrainDelay                  time.Duration
	LogLevel                    string
	LogDir                      string
	OTELExporterEndpoint        string
	OTELExporterInsecure        bool
	CookieSecure                bool
	WebhookAllowHTTP            bool
	WebhookAllowPrivate         bool
	WebhookTimeout              time.Duration
	WebhookPollInterval         time.Duration
	WebhookMaxAttempts          int
	CockpitSnapshotPath         string
	CockpitBaseURL              string
	CockpitAPIKey               string
	CockpitTestModel            string
	RegistrationMode            string
	PublicConsoleURL            string
	PublicSupportEmail          string
	PublicEnterpriseEmail       string
	VerificationTTL             time.Duration
	PasswordResetTTL            time.Duration
	InvitationTTL               time.Duration
	AdminMFARequired            bool
	LoginRateLimit              int
	RegistrationLimit           int
	VerificationLimit           int
	PasswordResetLimit          int
	PublicFunnelRateLimit       int
	IdentityRateWindow          time.Duration
	MailProvider                string
	MailFrom                    string
	MailCaptureDir              string
	MailPollInterval            time.Duration
	MailMaxAttempts             int
	SMTPHost                    string
	SMTPPort                    int
	SMTPUsername                string
	SMTPPassword                string
	SMTPTLSMode                 string
	ContentPolicyFailureMode    string
	GovernanceCleanupInterval   time.Duration
	ReportSLAHours              int
	MigrationMode               string
	ProviderAllowedHosts        []string
	ProviderAllowPrivate        bool
	ProviderAllowHTTP           bool
	StreamMaxBytes              int64
	ProviderQualityProbeRegion  string
	ProviderQualityPoll         time.Duration
	ProviderQualityLease        time.Duration
	ProviderQualityBatchSize    int
	SupplierSettlementPoll      time.Duration
	SupplierSettlementBatchSize int
	PayoutAllowedRegions        []string
	PayoutSandboxEnabled        bool
	PayoutSandboxSecret         []byte
}

type MigrationConfig struct {
	DatabaseURL         string
	PostgresMaxConns    int32
	PostgresMinConns    int32
	PostgresMaxIdleTime time.Duration
	PostgresMaxLifetime time.Duration
}

// LoadMigration reads only database settings so the one-shot migration job
// never needs Provider, JWT, API key, mail, or payment secrets.
func LoadMigration() (MigrationConfig, error) {
	c := MigrationConfig{
		DatabaseURL:      env("MIGRATION_DATABASE_URL", env("DATABASE_URL", "postgres://relayedock:relayedock@localhost:5432/relayedock?sslmode=disable")),
		PostgresMaxConns: postgresConnections("POSTGRES_MAX_CONNS", 4), PostgresMinConns: postgresConnections("POSTGRES_MIN_CONNS", 1),
		PostgresMaxIdleTime: duration("POSTGRES_MAX_CONN_IDLE_TIME", time.Minute),
		PostgresMaxLifetime: duration("POSTGRES_MAX_CONN_LIFETIME", 5*time.Minute),
	}
	if c.PostgresMinConns > c.PostgresMaxConns {
		return MigrationConfig{}, errors.New("POSTGRES_MIN_CONNS must not exceed POSTGRES_MAX_CONNS")
	}
	return c, nil
}

func Load() (Config, error) {
	c := Config{
		GatewayAddr:                 env("GATEWAY_ADDR", ":8080"),
		ControlAddr:                 env("CONTROL_ADDR", ":8081"),
		DatabaseURL:                 env("DATABASE_URL", "postgres://relayedock:relayedock@localhost:5432/relayedock?sslmode=disable"),
		PostgresMaxConns:            postgresConnections("POSTGRES_MAX_CONNS", 20),
		PostgresMinConns:            postgresConnections("POSTGRES_MIN_CONNS", 2),
		PostgresMaxIdleTime:         duration("POSTGRES_MAX_CONN_IDLE_TIME", 5*time.Minute),
		PostgresMaxLifetime:         duration("POSTGRES_MAX_CONN_LIFETIME", 30*time.Minute),
		RedisURL:                    env("REDIS_URL", "redis://localhost:6379/0"),
		RedisPoolSize:               integer("REDIS_POOL_SIZE", 20),
		RedisMinIdleConns:           integer("REDIS_MIN_IDLE_CONNS", 2),
		RedisDialTimeout:            duration("REDIS_DIAL_TIMEOUT", 5*time.Second),
		RedisReadTimeout:            duration("REDIS_READ_TIMEOUT", 3*time.Second),
		RedisWriteTimeout:           duration("REDIS_WRITE_TIMEOUT", 3*time.Second),
		JWTLifetime:                 durationAny(15*time.Minute, "RELAYDOCK_JWT_LIFETIME", "JWT_LIFETIME"),
		JWTRefreshLifetime:          durationAny(7*24*time.Hour, "RELAYDOCK_JWT_REFRESH_LIFETIME", "JWT_REFRESH_LIFETIME"),
		MigrationMode:               strings.ToLower(env("RELAYDOCK_MIGRATION_MODE", "startup")),
		ProviderAllowedHosts:        csv(env("RELAYDOCK_PROVIDER_ALLOWED_HOSTS", "api.openai.com,api.deepseek.com,openrouter.ai,api.anthropic.com,generativelanguage.googleapis.com,open.bigmodel.cn,api.moonshot.cn,dashscope.aliyuncs.com")),
		ProviderAllowPrivate:        boolean("RELAYDOCK_PROVIDER_ALLOW_PRIVATE_NETWORK", false),
		ProviderAllowHTTP:           boolean("RELAYDOCK_PROVIDER_ALLOW_HTTP", false),
		StreamMaxBytes:              int64(integer("RELAYDOCK_STREAM_MAX_BYTES", 64<<20)),
		ProviderQualityProbeRegion:  strings.ToUpper(strings.TrimSpace(os.Getenv("RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION"))),
		ProviderQualityPoll:         duration("RELAYDOCK_PROVIDER_QUALITY_POLL_INTERVAL", 30*time.Second),
		ProviderQualityLease:        duration("RELAYDOCK_PROVIDER_QUALITY_LEASE", 2*time.Minute),
		ProviderQualityBatchSize:    integer("RELAYDOCK_PROVIDER_QUALITY_BATCH_SIZE", 20),
		SupplierSettlementPoll:      duration("RELAYDOCK_SUPPLIER_SETTLEMENT_POLL_INTERVAL", time.Minute),
		SupplierSettlementBatchSize: integer("RELAYDOCK_SUPPLIER_SETTLEMENT_BATCH_SIZE", 100),
		PayoutAllowedRegions:        upperCSV(env("RELAYDOCK_PAYOUT_ALLOWED_REGIONS", "US,CN")),
		PayoutSandboxEnabled:        boolean("RELAYDOCK_PAYOUT_SANDBOX_ENABLED", false),
		AdminEmail:                  strings.ToLower(strings.TrimSpace(envAny("admin@relayedock.local", "RELAYDOCK_ADMIN_EMAIL", "ADMIN_EMAIL"))),
		AdminPassword:               envAny("", "RELAYDOCK_ADMIN_PASSWORD", "ADMIN_PASSWORD"),
		AdminDisplayName:            envAny("RelayDock Administrator", "RELAYDOCK_ADMIN_DISPLAY_NAME", "ADMIN_DISPLAY_NAME"),
		AllowedOrigins:              csv(env("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:3001")),
		TrustedProxies:              csv(os.Getenv("TRUSTED_PROXIES")),
		MaxBodyBytes:                int64(integer("MAX_REQUEST_BODY_BYTES", 16<<20)),
		Cooldown:                    duration("CREDENTIAL_COOLDOWN", 30*time.Second),
		ProviderTimeout:             duration("RELAYDOCK_PROVIDER_TIMEOUT", 10*time.Minute),
		FundingRecoveryPoll:         duration("RELAYDOCK_FUNDING_RECOVERY_INTERVAL", 30*time.Second),
		FundingStaleAfter:           duration("RELAYDOCK_FUNDING_STALE_AFTER", 15*time.Minute),
		PaymentOrderTTL:             duration("RELAYDOCK_PAYMENT_ORDER_TTL", 30*time.Minute),
		PaymentPollInterval:         duration("RELAYDOCK_PAYMENT_POLL_INTERVAL", 10*time.Second),
		PaymentTimestampSkew:        duration("RELAYDOCK_PAYMENT_WEBHOOK_TIMESTAMP_SKEW", 5*time.Minute),
		PaymentAllowedRegions:       upperCSV(env("RELAYDOCK_PAYMENT_ALLOWED_REGIONS", "CN")),
		PaymentSandboxEnabled:       boolean("RELAYDOCK_PAYMENT_SANDBOX_ENABLED", false),
		PaymentManualEnabled:        boolean("RELAYDOCK_PAYMENT_MANUAL_ENABLED", false),
		SubscriptionPoll:            duration("RELAYDOCK_SUBSCRIPTION_POLL_INTERVAL", time.Minute),
		ReconciliationInterval:      duration("RELAYDOCK_RECONCILIATION_INTERVAL", 15*time.Minute),
		ReconciliationRunAt:         timeOfDay("RELAYDOCK_RECONCILIATION_RUN_AT", 2*time.Hour),
		ShutdownTimeout:             duration("SHUTDOWN_TIMEOUT", 20*time.Second),
		DrainDelay:                  duration("RELAYDOCK_DRAIN_DELAY", 5*time.Second),
		LogLevel:                    env("LOG_LEVEL", "info"),
		LogDir:                      strings.TrimSpace(os.Getenv("LOG_DIR")),
		OTELExporterEndpoint:        strings.TrimSpace(os.Getenv("RELAYDOCK_OTEL_EXPORTER_OTLP_ENDPOINT")),
		OTELExporterInsecure:        boolean("RELAYDOCK_OTEL_EXPORTER_OTLP_INSECURE", false),
		CookieSecure:                boolean("COOKIE_SECURE", false),
		WebhookAllowHTTP:            boolean("WEBHOOK_ALLOW_HTTP", false),
		WebhookAllowPrivate:         boolean("WEBHOOK_ALLOW_PRIVATE_NETWORK", false),
		WebhookTimeout:              duration("WEBHOOK_TIMEOUT", 10*time.Second),
		WebhookPollInterval:         duration("WEBHOOK_POLL_INTERVAL", 2*time.Second),
		WebhookMaxAttempts:          integer("WEBHOOK_MAX_ATTEMPTS", 6),
		CockpitSnapshotPath:         strings.TrimSpace(os.Getenv("COCKPIT_SNAPSHOT_PATH")),
		CockpitBaseURL:              strings.TrimRight(strings.TrimSpace(os.Getenv("COCKPIT_BASE_URL")), "/"),
		CockpitAPIKey:               strings.TrimSpace(os.Getenv("COCKPIT_API_KEY")),
		CockpitTestModel:            env("COCKPIT_TEST_MODEL", "gpt-5.6-luna"),
		RegistrationMode:            strings.ToUpper(env("RELAYDOCK_REGISTRATION_MODE", "CLOSED")),
		PublicConsoleURL:            strings.TrimRight(env("RELAYDOCK_PUBLIC_CONSOLE_URL", "http://localhost:3001"), "/"),
		PublicSupportEmail:          env("RELAYDOCK_PUBLIC_SUPPORT_EMAIL", "support@example.invalid"),
		PublicEnterpriseEmail:       env("RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL", "enterprise@example.invalid"),
		VerificationTTL:             duration("RELAYDOCK_EMAIL_VERIFICATION_TTL", 24*time.Hour),
		PasswordResetTTL:            duration("RELAYDOCK_PASSWORD_RESET_TTL", time.Hour),
		InvitationTTL:               duration("RELAYDOCK_INVITATION_TTL", 7*24*time.Hour),
		AdminMFARequired:            boolean("RELAYDOCK_ADMIN_MFA_REQUIRED", false),
		LoginRateLimit:              integer("RELAYDOCK_LOGIN_RATE_LIMIT", 10),
		RegistrationLimit:           integer("RELAYDOCK_REGISTRATION_RATE_LIMIT", 5),
		VerificationLimit:           integer("RELAYDOCK_VERIFICATION_RATE_LIMIT", 10),
		PasswordResetLimit:          integer("RELAYDOCK_PASSWORD_RESET_RATE_LIMIT", 5),
		PublicFunnelRateLimit:       integer("RELAYDOCK_PUBLIC_FUNNEL_RATE_LIMIT", 120),
		IdentityRateWindow:          duration("RELAYDOCK_IDENTITY_RATE_WINDOW", 15*time.Minute),
		MailProvider:                strings.ToLower(env("RELAYDOCK_MAIL_PROVIDER", "local")),
		MailFrom:                    env("RELAYDOCK_MAIL_FROM", "ModelDock <no-reply@modeldock.local>"),
		MailCaptureDir:              strings.TrimSpace(env("RELAYDOCK_MAIL_CAPTURE_DIR", "./logs/mail")),
		MailPollInterval:            duration("RELAYDOCK_MAIL_POLL_INTERVAL", 2*time.Second),
		MailMaxAttempts:             integer("RELAYDOCK_MAIL_MAX_ATTEMPTS", 6),
		SMTPHost:                    strings.TrimSpace(os.Getenv("RELAYDOCK_SMTP_HOST")),
		SMTPPort:                    integer("RELAYDOCK_SMTP_PORT", 587),
		SMTPUsername:                strings.TrimSpace(os.Getenv("RELAYDOCK_SMTP_USERNAME")),
		SMTPPassword:                os.Getenv("RELAYDOCK_SMTP_PASSWORD"),
		SMTPTLSMode:                 strings.ToLower(env("RELAYDOCK_SMTP_TLS_MODE", "starttls")),
		ContentPolicyFailureMode:    strings.ToUpper(env("RELAYDOCK_CONTENT_POLICY_FAILURE_MODE", "FAIL_CLOSED")),
		GovernanceCleanupInterval:   duration("RELAYDOCK_GOVERNANCE_CLEANUP_INTERVAL", time.Hour),
		ReportSLAHours:              integer("RELAYDOCK_REPORT_SLA_HOURS", 72),
	}
	var err error
	if c.MasterKey, err = secret32("RELAYDOCK_MASTER_KEY"); err != nil {
		return Config{}, err
	}
	if c.APIKeyHMACSecret, err = secretMinAny(32, "RELAYDOCK_API_KEY_HMAC_SECRET", "API_KEY_HMAC_SECRET"); err != nil {
		return Config{}, err
	}
	if c.JWTSecret, err = secretMinAny(32, "RELAYDOCK_JWT_SECRET", "JWT_SECRET"); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(os.Getenv("RELAYDOCK_JWT_PREVIOUS_SECRET")); raw != "" {
		if c.JWTPreviousSecret, err = decodeSecret(raw); err != nil || len(c.JWTPreviousSecret) < 32 {
			return Config{}, errors.New("RELAYDOCK_JWT_PREVIOUS_SECRET must contain at least 32 bytes")
		}
	}
	if c.PaymentSandboxEnabled {
		if c.PaymentSandboxSecret, err = secretMinAny(32, "RELAYDOCK_PAYMENT_SANDBOX_SECRET"); err != nil {
			return Config{}, err
		}
	}
	if c.PayoutSandboxEnabled {
		if c.PayoutSandboxSecret, err = secretMinAny(32, "RELAYDOCK_PAYOUT_SANDBOX_SECRET"); err != nil {
			return Config{}, err
		}
	}
	if c.JWTRefreshLifetime <= c.JWTLifetime {
		return Config{}, errors.New("RELAYDOCK_JWT_REFRESH_LIFETIME must be longer than RELAYDOCK_JWT_LIFETIME")
	}
	if c.PostgresMinConns > c.PostgresMaxConns {
		return Config{}, errors.New("POSTGRES_MIN_CONNS must not exceed POSTGRES_MAX_CONNS")
	}
	if c.MigrationMode != "startup" && c.MigrationMode != "external" && c.MigrationMode != "disabled" {
		return Config{}, errors.New("RELAYDOCK_MIGRATION_MODE must be startup, external, or disabled")
	}
	if c.StreamMaxBytes <= 0 {
		return Config{}, errors.New("RELAYDOCK_STREAM_MAX_BYTES must be positive")
	}
	if c.ProviderQualityProbeRegion != "" && !validRegionCode(c.ProviderQualityProbeRegion) {
		return Config{}, errors.New("RELAYDOCK_PROVIDER_QUALITY_PROBE_REGION must be an ISO 3166-1 alpha-2 region")
	}
	if c.ProviderQualityBatchSize > 100 {
		return Config{}, errors.New("RELAYDOCK_PROVIDER_QUALITY_BATCH_SIZE must not exceed 100")
	}
	if c.SupplierSettlementBatchSize <= 0 || c.SupplierSettlementBatchSize > 500 {
		return Config{}, errors.New("RELAYDOCK_SUPPLIER_SETTLEMENT_BATCH_SIZE must be between 1 and 500")
	}
	if len(c.PayoutAllowedRegions) == 0 {
		return Config{}, errors.New("RELAYDOCK_PAYOUT_ALLOWED_REGIONS must contain at least one ISO 3166-1 alpha-2 region")
	}
	for _, region := range c.PayoutAllowedRegions {
		if !validRegionCode(region) {
			return Config{}, errors.New("RELAYDOCK_PAYOUT_ALLOWED_REGIONS must contain ISO 3166-1 alpha-2 regions")
		}
	}
	if c.PublicFunnelRateLimit <= 0 {
		return Config{}, errors.New("RELAYDOCK_PUBLIC_FUNNEL_RATE_LIMIT must be positive")
	}
	if c.RedisMinIdleConns > c.RedisPoolSize {
		return Config{}, errors.New("REDIS_MIN_IDLE_CONNS must not exceed REDIS_POOL_SIZE")
	}
	if c.RegistrationMode != "CLOSED" && c.RegistrationMode != "INVITE_ONLY" && c.RegistrationMode != "PUBLIC" {
		return Config{}, errors.New("RELAYDOCK_REGISTRATION_MODE must be CLOSED, INVITE_ONLY, or PUBLIC")
	}
	if !validPublicMailbox(c.PublicSupportEmail) {
		return Config{}, errors.New("RELAYDOCK_PUBLIC_SUPPORT_EMAIL must be a plain valid email address")
	}
	if !validPublicMailbox(c.PublicEnterpriseEmail) {
		return Config{}, errors.New("RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL must be a plain valid email address")
	}
	if c.MailProvider != "local" && c.MailProvider != "smtp" {
		return Config{}, errors.New("RELAYDOCK_MAIL_PROVIDER must be local or smtp")
	}
	if c.MailProvider == "smtp" && (c.SMTPHost == "" || c.MailFrom == "") {
		return Config{}, errors.New("RELAYDOCK_SMTP_HOST and RELAYDOCK_MAIL_FROM are required for the smtp mail provider")
	}
	if c.SMTPTLSMode != "starttls" && c.SMTPTLSMode != "tls" && c.SMTPTLSMode != "none" {
		return Config{}, errors.New("RELAYDOCK_SMTP_TLS_MODE must be starttls, tls, or none")
	}
	if c.ContentPolicyFailureMode != "FAIL_OPEN" && c.ContentPolicyFailureMode != "FAIL_CLOSED" {
		return Config{}, errors.New("RELAYDOCK_CONTENT_POLICY_FAILURE_MODE must be FAIL_OPEN or FAIL_CLOSED")
	}
	if c.FundingStaleAfter <= c.ProviderTimeout {
		return Config{}, errors.New("RELAYDOCK_FUNDING_STALE_AFTER must be longer than RELAYDOCK_PROVIDER_TIMEOUT")
	}
	if c.ReconciliationRunAt < 0 || c.ReconciliationRunAt >= 24*time.Hour {
		return Config{}, errors.New("RELAYDOCK_RECONCILIATION_RUN_AT must use UTC HH:MM")
	}
	if len(c.PaymentAllowedRegions) == 0 {
		return Config{}, errors.New("RELAYDOCK_PAYMENT_ALLOWED_REGIONS must contain at least one ISO 3166-1 alpha-2 region")
	}
	for _, region := range c.PaymentAllowedRegions {
		if !validRegionCode(region) {
			return Config{}, errors.New("RELAYDOCK_PAYMENT_ALLOWED_REGIONS must contain ISO 3166-1 alpha-2 regions")
		}
	}
	return c, nil
}

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envAny(fallback string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return fallback
}

func validPublicMailbox(value string) bool {
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Name == "" && parsed.Address == value
}

func csv(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func upperCSV(v string) []string {
	values := csv(v)
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToUpper(value)
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validRegionCode(value string) bool {
	return len(value) == 2 && value[0] >= 'A' && value[0] <= 'Z' && value[1] >= 'A' && value[1] <= 'Z'
}

func duration(name string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(name))
	if err == nil && d > 0 {
		return d
	}
	return fallback
}

func timeOfDay(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := time.Parse("15:04", raw)
	if err != nil {
		return -1
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute
}

func durationAny(fallback time.Duration, names ...string) time.Duration {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			if d, err := time.ParseDuration(value); err == nil && d > 0 {
				return d
			}
		}
	}
	return fallback
}

func integer(name string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(name))
	if err == nil && n > 0 {
		return n
	}
	return fallback
}

func postgresConnections(name string, fallback int32) int32 {
	n, err := strconv.ParseInt(os.Getenv(name), 10, 32)
	if err == nil && n > 0 {
		return int32(n)
	}
	return fallback
}

func boolean(name string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if v == "true" || v == "1" || v == "yes" {
		return true
	}
	if v == "false" || v == "0" || v == "no" {
		return false
	}
	return fallback
}

func secret32(name string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	b, err := decodeSecret(raw)
	if err != nil || len(b) != 32 {
		return nil, errors.New(name + " must decode to exactly 32 bytes (base64, hex, or a 32-byte raw value)")
	}
	return b, nil
}

func secretMinAny(min int, names ...string) ([]byte, error) {
	name := names[0]
	raw := ""
	for _, candidate := range names {
		if value := strings.TrimSpace(os.Getenv(candidate)); value != "" {
			name, raw = candidate, value
			break
		}
	}
	b, err := decodeSecret(raw)
	if (err != nil || len(b) < min) && len(raw) >= min {
		return []byte(raw), nil
	}
	if err != nil || len(b) < min {
		return nil, errors.New(name + " must contain at least 32 bytes (base64, hex, or raw)")
	}
	return b, nil
}

func decodeSecret(v string) ([]byte, error) {
	if v == "" {
		return nil, errors.New("missing secret")
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(v); err == nil {
		return b, nil
	}
	if b, err := hex.DecodeString(v); err == nil {
		return b, nil
	}
	return []byte(v), nil
}
