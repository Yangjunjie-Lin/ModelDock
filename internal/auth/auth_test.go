package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	const password = "RelayDock-test-password-2026!"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("HashPassword() returned unexpected format")
	}
	if !VerifyPassword(hash, password) {
		t.Fatal("VerifyPassword() rejected an Argon2id hash produced by HashPassword()")
	}
	if VerifyPassword(hash, password+"-wrong") {
		t.Fatal("VerifyPassword() accepted an incorrect password")
	}
}

func TestJWTSecretRotationAcceptsPreviousAndIssuesCurrent(t *testing.T) {
	previous := bytes.Repeat([]byte("p"), 32)
	current := bytes.Repeat([]byte("c"), 32)
	oldManager, err := NewManagerWithRefresh(previous, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, _, err := oldManager.Issue("user", "user@example.invalid", "USER")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewManagerWithRefreshAndPrevious(current, previous, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rotated.Parse(oldToken); err != nil {
		t.Fatalf("previous-key token rejected during rotation: %v", err)
	}
	newToken, _, err := rotated.Issue("user", "user@example.invalid", "USER")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = oldManager.Parse(newToken); err == nil {
		t.Fatal("new token unexpectedly validates with only the previous key")
	}
}

func TestLegacyBcryptPasswordCompatibility(t *testing.T) {
	const password = "RelayDock-test-password-2026!"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	if !VerifyPassword(string(hash), password) {
		t.Fatal("VerifyPassword() rejected a valid legacy bcrypt hash")
	}
}
