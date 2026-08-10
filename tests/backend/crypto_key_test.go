package backend_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/relayedock/relayedock/internal/apikey"
	"github.com/relayedock/relayedock/internal/auth"
	secretcrypto "github.com/relayedock/relayedock/internal/crypto"
	"github.com/relayedock/relayedock/internal/domain"
	"golang.org/x/crypto/bcrypt"
)

func TestCredentialVaultRoundTripAndBinding(t *testing.T) {
	vault, err := secretcrypto.NewVault(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := vault.Encrypt("sk-project-secret-value", "credential-a")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(envelope, []byte("sk-project-secret-value")) {
		t.Fatal("ciphertext leaks plaintext")
	}
	plain, err := vault.Decrypt(envelope, "credential-a")
	if err != nil || plain != "sk-project-secret-value" {
		t.Fatalf("round trip: %q, %v", plain, err)
	}
	if _, err := vault.Decrypt(envelope, "credential-b"); err == nil {
		t.Fatal("ciphertext must be bound to its credential ID")
	}
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-1] ^= 1
	if _, err := vault.Decrypt(tampered, "credential-a"); err == nil {
		t.Fatal("tampering was not detected")
	}
}

func TestSecretBearingFieldsNeverSerialize(t *testing.T) {
	credential := domain.Credential{ID: "cred", EncryptedSecret: []byte("ciphertext"), HasSecret: true, SecretLast4: "abcd"}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("ciphertext")) || bytes.Contains(raw, []byte("encrypted_secret")) {
		t.Fatalf("credential leaked encrypted material: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"secret_last4":"abcd"`)) {
		t.Fatalf("last4 missing: %s", raw)
	}
	key := domain.APIKey{KeyHash: []byte("stored-hmac"), KeyPrefix: "rdk_live_prefix"}
	raw, err = json.Marshal(key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("stored-hmac")) || bytes.Contains(raw, []byte("key_hash")) {
		t.Fatalf("API key hash leaked: %s", raw)
	}
}

func TestRelayDockAPIKeysAreOneWayAndEnvironmentScoped(t *testing.T) {
	manager, err := apikey.NewManager(bytes.Repeat([]byte("h"), 32))
	if err != nil {
		t.Fatal(err)
	}
	live, prefix, hash, err := manager.Generate("live")
	if err != nil {
		t.Fatal(err)
	}
	if !apikey.LooksValid(live) || len(prefix) >= len(live) {
		t.Fatalf("bad generated key %q / %q", live, prefix)
	}
	if bytes.Contains(hash, []byte(live)) {
		t.Fatal("stored hash contains full key")
	}
	if !manager.Verify(live, hash) || manager.Verify(live+"x", hash) {
		t.Fatal("HMAC verification mismatch")
	}
	testKey, _, testHash, err := manager.Generate("test")
	if err != nil {
		t.Fatal(err)
	}
	if live == testKey || bytes.Equal(hash, testHash) {
		t.Fatal("keys must be unique")
	}
	if _, _, _, err := manager.Generate("staging"); err == nil {
		t.Fatal("unsupported environment accepted")
	}
}

func TestJWTAndPasswordLifecycle(t *testing.T) {
	hash, err := auth.HashPassword("a-strong-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyPassword(hash, "a-strong-admin-password") || auth.VerifyPassword(hash, "wrong-password") {
		t.Fatal("password verification failed")
	}
	manager, err := auth.NewManager(bytes.Repeat([]byte("j"), 32), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := manager.Issue("user-id", "user@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.Parse(raw)
	if err != nil || claims.Subject != "user-id" || claims.Role != "USER" {
		t.Fatalf("claims mismatch: %#v %v", claims, err)
	}
}

func TestLegacyBcryptPasswordStillVerifiesAfterArgonUpgrade(t *testing.T) {
	legacy, err := bcrypt.GenerateFromPassword([]byte("legacy-strong-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyPassword(string(legacy), "legacy-strong-password") {
		t.Fatal("legacy bcrypt account would be locked out after upgrade")
	}
}
