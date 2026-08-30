package providers_test

import (
	"net/url"
	"testing"

	"github.com/relayedock/relayedock/internal/providers"
	"github.com/relayedock/relayedock/internal/providers/openai"
)

func TestRegistryResolvesProviderTypeCaseInsensitively(t *testing.T) {
	registry := providers.NewRegistry()
	adapter := openai.New(nil)
	registry.Register("DeepSeek", adapter)
	resolved, err := registry.Resolve(" deepseek ")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != adapter {
		t.Fatal("registry returned a different adapter")
	}
	if _, err := registry.Resolve("missing"); err != providers.ErrProviderNotRegistered {
		t.Fatalf("expected ErrProviderNotRegistered, got %v", err)
	}
}

func TestRegistrySupportsCustomOpenAICompatibleProvider(t *testing.T) {
	registry := providers.NewRegistry()
	adapter := openai.New(nil)
	registry.Register("openai_compatible", adapter)
	resolved, err := registry.Resolve("OPENAI_COMPATIBLE")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != adapter {
		t.Fatal("custom OpenAI-compatible type did not resolve to the shared adapter")
	}
}

func TestProviderEndpointPolicyRequiresHTTPSAndAllowlist(t *testing.T) {
	policy := openai.EndpointPolicy{AllowedHosts: []string{"api.example.invalid", "*.provider.invalid"}}
	allowed, _ := url.Parse("https://api.example.invalid/v1")
	if err := policy.ValidateURL(allowed); err != nil {
		t.Fatalf("allowlisted HTTPS endpoint rejected: %v", err)
	}
	for _, raw := range []string{
		"http://api.example.invalid/v1",
		"https://metadata.google.internal/latest",
		"https://provider.invalid/v1",
		"https://user:pass@api.example.invalid/v1",
	} {
		target, _ := url.Parse(raw)
		if err := policy.ValidateURL(target); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", raw)
		}
	}
	subdomain, _ := url.Parse("https://edge.provider.invalid/v1")
	if err := policy.ValidateURL(subdomain); err != nil {
		t.Fatalf("explicit wildcard subdomain rejected: %v", err)
	}
}
