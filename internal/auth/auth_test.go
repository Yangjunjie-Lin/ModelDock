package auth

import (
	"strings"
	"testing"

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
