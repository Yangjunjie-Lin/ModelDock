package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadPublicContactEmails(t *testing.T) {
	t.Run("safe defaults", func(t *testing.T) {
		setValidLoadEnvironment(t)
		t.Setenv("RELAYDOCK_PUBLIC_SUPPORT_EMAIL", "")
		t.Setenv("RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL", "")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PublicSupportEmail != "support@example.invalid" || cfg.PublicEnterpriseEmail != "enterprise@example.invalid" {
			t.Fatalf("unexpected public contact defaults: support=%q enterprise=%q", cfg.PublicSupportEmail, cfg.PublicEnterpriseEmail)
		}
	})

	t.Run("configured addresses", func(t *testing.T) {
		setValidLoadEnvironment(t)
		t.Setenv("RELAYDOCK_PUBLIC_SUPPORT_EMAIL", "helpdesk@example.invalid")
		t.Setenv("RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL", "sales@example.invalid")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PublicSupportEmail != "helpdesk@example.invalid" || cfg.PublicEnterpriseEmail != "sales@example.invalid" {
			t.Fatalf("configured public contacts were not preserved: support=%q enterprise=%q", cfg.PublicSupportEmail, cfg.PublicEnterpriseEmail)
		}
	})

	t.Run("invalid support address", func(t *testing.T) {
		setValidLoadEnvironment(t)
		t.Setenv("RELAYDOCK_PUBLIC_SUPPORT_EMAIL", "Support Team <support@example.invalid>")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "RELAYDOCK_PUBLIC_SUPPORT_EMAIL") {
			t.Fatalf("expected a public support email validation error, got %v", err)
		}
	})

	t.Run("invalid enterprise address", func(t *testing.T) {
		setValidLoadEnvironment(t)
		t.Setenv("RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL", "not-an-email")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "RELAYDOCK_PUBLIC_ENTERPRISE_EMAIL") {
			t.Fatalf("expected a public enterprise email validation error, got %v", err)
		}
	})
}

func TestValidPublicMailboxRejectsHeaderInjection(t *testing.T) {
	if validPublicMailbox("support@example.invalid\r\nBcc: recipient@example.invalid") {
		t.Fatal("public mailbox validation accepted a header injection payload")
	}
}

func TestPaymentAllowedRegionsAreNormalizedAndValidated(t *testing.T) {
	setValidLoadEnvironment(t)
	t.Setenv("RELAYDOCK_PAYMENT_ALLOWED_REGIONS", "cn, sg, CN")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.PaymentAllowedRegions, []string{"CN", "SG"}) {
		t.Fatalf("payment regions=%v", cfg.PaymentAllowedRegions)
	}

	for _, invalid := range []string{",,", "1!", "C1", "C"} {
		t.Run("reject_"+strings.ReplaceAll(invalid, ",", "comma"), func(t *testing.T) {
			setValidLoadEnvironment(t)
			t.Setenv("RELAYDOCK_PAYMENT_ALLOWED_REGIONS", invalid)
			if _, loadErr := Load(); loadErr == nil || !strings.Contains(loadErr.Error(), "RELAYDOCK_PAYMENT_ALLOWED_REGIONS") {
				t.Fatalf("invalid regions %q error=%v", invalid, loadErr)
			}
		})
	}
}

func TestPayoutAllowedRegionsAndSandboxSecret(t *testing.T) {
	setValidLoadEnvironment(t)
	t.Setenv("RELAYDOCK_PAYOUT_ALLOWED_REGIONS", "us, cn, US")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.PayoutAllowedRegions, []string{"US", "CN"}) {
		t.Fatalf("payout regions=%v", cfg.PayoutAllowedRegions)
	}
	t.Setenv("RELAYDOCK_PAYOUT_ALLOWED_REGIONS", "U1")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "RELAYDOCK_PAYOUT_ALLOWED_REGIONS") {
		t.Fatalf("expected payout region validation, got %v", err)
	}
	setValidLoadEnvironment(t)
	t.Setenv("RELAYDOCK_PAYOUT_SANDBOX_ENABLED", "true")
	t.Setenv("RELAYDOCK_PAYOUT_SANDBOX_SECRET", "short")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "RELAYDOCK_PAYOUT_SANDBOX_SECRET") {
		t.Fatalf("expected payout sandbox secret validation, got %v", err)
	}
}

func setValidLoadEnvironment(t *testing.T) {
	t.Helper()
	values := map[string]string{
		"RELAYDOCK_MASTER_KEY":                  "0123456789abcdef0123456789abcdef",
		"RELAYDOCK_API_KEY_HMAC_SECRET":         "abcdef0123456789abcdef0123456789",
		"RELAYDOCK_JWT_SECRET":                  "89abcdef0123456789abcdef01234567",
		"RELAYDOCK_JWT_PREVIOUS_SECRET":         "",
		"RELAYDOCK_JWT_LIFETIME":                "15m",
		"RELAYDOCK_JWT_REFRESH_LIFETIME":        "168h",
		"POSTGRES_MAX_CONNS":                    "20",
		"POSTGRES_MIN_CONNS":                    "2",
		"REDIS_POOL_SIZE":                       "20",
		"REDIS_MIN_IDLE_CONNS":                  "2",
		"RELAYDOCK_MIGRATION_MODE":              "startup",
		"RELAYDOCK_STREAM_MAX_BYTES":            "67108864",
		"RELAYDOCK_PUBLIC_FUNNEL_RATE_LIMIT":    "120",
		"RELAYDOCK_REGISTRATION_MODE":           "CLOSED",
		"RELAYDOCK_MAIL_PROVIDER":               "local",
		"RELAYDOCK_SMTP_TLS_MODE":               "starttls",
		"RELAYDOCK_CONTENT_POLICY_FAILURE_MODE": "FAIL_CLOSED",
		"RELAYDOCK_PROVIDER_TIMEOUT":            "10m",
		"RELAYDOCK_FUNDING_STALE_AFTER":         "15m",
		"RELAYDOCK_RECONCILIATION_RUN_AT":       "02:00",
		"RELAYDOCK_PAYMENT_ALLOWED_REGIONS":     "CN",
		"RELAYDOCK_PAYMENT_SANDBOX_ENABLED":     "false",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
}
