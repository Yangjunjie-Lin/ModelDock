package store

import "testing"

func TestCapabilityDocumentDigestCanonicalAndSensitiveToContent(t *testing.T) {
	left, _, err := capabilityDocumentDigest(map[string]any{"models": []any{"a", "b"}, "zdr": true})
	if err != nil {
		t.Fatal(err)
	}
	right, _, err := capabilityDocumentDigest(map[string]any{"zdr": true, "models": []any{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	changed, _, err := capabilityDocumentDigest(map[string]any{"zdr": false, "models": []any{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("map key order changed canonical digest: %s != %s", left, right)
	}
	if left == changed || len(left) != 64 {
		t.Fatalf("capability digest did not bind content: %s / %s", left, changed)
	}
}

func TestNormalizeStringsLowercasesDeduplicatesAndDropsEmpty(t *testing.T) {
	values := normalizeStrings([]string{" OpenAI ", "openai", "", "ANTHROPIC"})
	if len(values) != 2 || values[0] != "openai" || values[1] != "anthropic" {
		t.Fatalf("unexpected normalized strings: %#v", values)
	}
}
