package config

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GatewayAddr         string
	ControlAddr         string
	DatabaseURL         string
	PostgresMaxConns    int
	PostgresMinConns    int
	PostgresMaxIdleTime time.Duration
	PostgresMaxLifetime time.Duration
	RedisURL            string
	RedisPoolSize       int
	RedisMinIdleConns   int
	RedisDialTimeout    time.Duration
	RedisReadTimeout    time.Duration
	RedisWriteTimeout   time.Duration
	MasterKey           []byte
	APIKeyHMACSecret    []byte
	JWTSecret           []byte
	JWTLifetime         time.Duration
	JWTRefreshLifetime  time.Duration
	AdminEmail          string
	AdminPassword       string
	AdminDisplayName    string
	AllowedOrigins      []string
	TrustedProxies      []string
	MaxBodyBytes        int64
	Cooldown            time.Duration
	ShutdownTimeout     time.Duration
	LogLevel            string
	LogDir              string
	CookieSecure        bool
	WebhookAllowHTTP    bool
	WebhookAllowPrivate bool
	WebhookTimeout      time.Duration
	WebhookPollInterval time.Duration
	WebhookMaxAttempts  int
	CockpitSnapshotPath string
	CockpitBaseURL      string
	CockpitAPIKey       string
	CockpitTestModel    string
}

func Load() (Config, error) {
	c := Config{
		GatewayAddr:         env("GATEWAY_ADDR", ":8080"),
		ControlAddr:         env("CONTROL_ADDR", ":8081"),
		DatabaseURL:         env("DATABASE_URL", "postgres://relayedock:relayedock@localhost:5432/relayedock?sslmode=disable"),
		PostgresMaxConns:    integer("POSTGRES_MAX_CONNS", 20),
		PostgresMinConns:    integer("POSTGRES_MIN_CONNS", 2),
		PostgresMaxIdleTime: duration("POSTGRES_MAX_CONN_IDLE_TIME", 5*time.Minute),
		PostgresMaxLifetime: duration("POSTGRES_MAX_CONN_LIFETIME", 30*time.Minute),
		RedisURL:            env("REDIS_URL", "redis://localhost:6379/0"),
		RedisPoolSize:       integer("REDIS_POOL_SIZE", 20),
		RedisMinIdleConns:   integer("REDIS_MIN_IDLE_CONNS", 2),
		RedisDialTimeout:    duration("REDIS_DIAL_TIMEOUT", 5*time.Second),
		RedisReadTimeout:    duration("REDIS_READ_TIMEOUT", 3*time.Second),
		RedisWriteTimeout:   duration("REDIS_WRITE_TIMEOUT", 3*time.Second),
		JWTLifetime:         durationAny(15*time.Minute, "RELAYDOCK_JWT_LIFETIME", "JWT_LIFETIME"),
		JWTRefreshLifetime:  durationAny(7*24*time.Hour, "RELAYDOCK_JWT_REFRESH_LIFETIME", "JWT_REFRESH_LIFETIME"),
		AdminEmail:          strings.ToLower(strings.TrimSpace(envAny("admin@relayedock.local", "RELAYDOCK_ADMIN_EMAIL", "ADMIN_EMAIL"))),
		AdminPassword:       envAny("", "RELAYDOCK_ADMIN_PASSWORD", "ADMIN_PASSWORD"),
		AdminDisplayName:    envAny("RelayDock Administrator", "RELAYDOCK_ADMIN_DISPLAY_NAME", "ADMIN_DISPLAY_NAME"),
		AllowedOrigins:      csv(env("ALLOWED_ORIGINS", "http://localhost:3000,http://localhost:3001")),
		TrustedProxies:      csv(os.Getenv("TRUSTED_PROXIES")),
		MaxBodyBytes:        int64(integer("MAX_REQUEST_BODY_BYTES", 16<<20)),
		Cooldown:            duration("CREDENTIAL_COOLDOWN", 30*time.Second),
		ShutdownTimeout:     duration("SHUTDOWN_TIMEOUT", 20*time.Second),
		LogLevel:            env("LOG_LEVEL", "info"),
		LogDir:              strings.TrimSpace(os.Getenv("LOG_DIR")),
		CookieSecure:        boolean("COOKIE_SECURE", false),
		WebhookAllowHTTP:    boolean("WEBHOOK_ALLOW_HTTP", false),
		WebhookAllowPrivate: boolean("WEBHOOK_ALLOW_PRIVATE_NETWORK", false),
		WebhookTimeout:      duration("WEBHOOK_TIMEOUT", 10*time.Second),
		WebhookPollInterval: duration("WEBHOOK_POLL_INTERVAL", 2*time.Second),
		WebhookMaxAttempts:  integer("WEBHOOK_MAX_ATTEMPTS", 6),
		CockpitSnapshotPath: strings.TrimSpace(os.Getenv("COCKPIT_SNAPSHOT_PATH")),
		CockpitBaseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("COCKPIT_BASE_URL")), "/"),
		CockpitAPIKey:       strings.TrimSpace(os.Getenv("COCKPIT_API_KEY")),
		CockpitTestModel:    env("COCKPIT_TEST_MODEL", "gpt-5.6-luna"),
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
	if c.JWTRefreshLifetime <= c.JWTLifetime {
		return Config{}, errors.New("RELAYDOCK_JWT_REFRESH_LIFETIME must be longer than RELAYDOCK_JWT_LIFETIME")
	}
	if c.PostgresMinConns > c.PostgresMaxConns {
		return Config{}, errors.New("POSTGRES_MIN_CONNS must not exceed POSTGRES_MAX_CONNS")
	}
	if c.RedisMinIdleConns > c.RedisPoolSize {
		return Config{}, errors.New("REDIS_MIN_IDLE_CONNS must not exceed REDIS_POOL_SIZE")
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

func duration(name string, fallback time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(name))
	if err == nil && d > 0 {
		return d
	}
	return fallback
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
