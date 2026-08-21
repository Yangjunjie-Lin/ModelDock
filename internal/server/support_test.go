package server

import (
	"strings"
	"testing"
)

func TestRedactSupportText(t *testing.T) {
	key := "rdk_test_" + strings.Repeat("A", 43)
	providerKey := "sk-" + strings.Repeat("b", 24)
	input := "email=user@example.invalid bearer abc.def token:secret-value key=" + key + " provider=" + providerKey
	redacted := redactSupportText(input)
	for _, secret := range []string{"user@example.invalid", "abc.def", "secret-value", key, providerKey} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("secret %q remains in %q", secret, redacted)
		}
	}
	for _, marker := range []string{"[REDACTED_EMAIL]", "Bearer [REDACTED]", "token=[REDACTED]", "[REDACTED_API_KEY]", "[REDACTED_PROVIDER_KEY]"} {
		if !strings.Contains(redacted, marker) {
			t.Fatalf("redaction marker %q missing from %q", marker, redacted)
		}
	}
}

func TestRedactSupportContextUsesAllowlist(t *testing.T) {
	context := redactSupportContext(map[string]any{
		"request_id": "req_fixture", "trace_id": "trace_fixture", "prompt": "private prompt",
		"authorization": "Bearer fixture", "endpoint": "/v1/responses",
	})
	if context["request_id"] != "req_fixture" || context["trace_id"] != "trace_fixture" {
		t.Fatalf("correlation fields missing: %v", context)
	}
	if _, ok := context["prompt"]; ok {
		t.Fatalf("prompt was retained: %v", context)
	}
	if _, ok := context["authorization"]; ok {
		t.Fatalf("authorization was retained: %v", context)
	}
}
