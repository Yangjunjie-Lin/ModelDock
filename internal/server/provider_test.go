package server

import "testing"

func TestNormalizeProviderType(t *testing.T) {
	tests := map[string]string{
		"":           "openai",
		"OpenAI":     "openai",
		" deepseek ": "deepseek",
		"OPENROUTER": "openrouter",
	}
	for input, expected := range tests {
		actual, ok := normalizeProviderType(input)
		if !ok || actual != expected {
			t.Fatalf("normalizeProviderType(%q) = %q, %v; want %q, true", input, actual, ok, expected)
		}
	}
	if actual, ok := normalizeProviderType("unsupported"); ok || actual != "" {
		t.Fatalf("unsupported provider type = %q, %v; want empty, false", actual, ok)
	}
}
