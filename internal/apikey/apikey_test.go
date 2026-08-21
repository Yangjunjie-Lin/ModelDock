package apikey

import (
	"bytes"
	"testing"
)

func TestGeneratedKeysDoNotCollideAndHashesAreSecretScoped(t *testing.T) {
	first, err := NewManager(bytes.Repeat([]byte("a"), 32))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(bytes.Repeat([]byte("b"), 32))
	if err != nil {
		t.Fatal(err)
	}
	seenKeys := make(map[string]struct{}, 4096)
	seenHashes := make(map[string]struct{}, 4096)
	for i := 0; i < 4096; i++ {
		key, _, digest, generateErr := first.Generate("test")
		if generateErr != nil {
			t.Fatal(generateErr)
		}
		if _, exists := seenKeys[key]; exists {
			t.Fatal("generated API key collision")
		}
		if _, exists := seenHashes[string(digest)]; exists {
			t.Fatal("generated API key HMAC collision")
		}
		seenKeys[key] = struct{}{}
		seenHashes[string(digest)] = struct{}{}
		if bytes.Equal(digest, second.Hash(key)) {
			t.Fatal("different HMAC secrets produced the same digest")
		}
	}
}

func TestLooksValidRejectsEnumerablePrefixes(t *testing.T) {
	for _, candidate := range []string{"rdk_live_", "rdk_test_short", "sk-provider-secret", "rdk_prod_value"} {
		if LooksValid(candidate) {
			t.Fatalf("invalid or upstream key shape accepted: %q", candidate)
		}
	}
}
