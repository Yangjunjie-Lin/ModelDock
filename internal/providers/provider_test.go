package providers_test

import (
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
