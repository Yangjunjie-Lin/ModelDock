package auth

import (
	"testing"
	"time"
)

func TestValidateTOTPUsesRFC6238SHA1Profile(t *testing.T) {
	// RFC 6238 SHA-1 test secret. The RFC's 8-digit value at Unix 59 is
	// 94287082; the standard 6-digit authenticator profile is therefore 287082.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	at := time.Unix(59, 0).UTC()
	step, err := ValidateTOTP(secret, "287082", at)
	if err != nil {
		t.Fatalf("ValidateTOTP() error = %v", err)
	}
	if step != 1 {
		t.Fatalf("ValidateTOTP() step = %d, want 1", step)
	}
	if generated := TOTPCode(secret, at); generated != "287082" {
		t.Fatalf("TOTPCode() = %q, want 287082", generated)
	}
}

func TestValidateTOTPRejectsMalformedCodeAndSecret(t *testing.T) {
	if _, err := ValidateTOTP("not-base32", "123456", time.Now()); err == nil {
		t.Fatal("ValidateTOTP() accepted malformed secret")
	}
	if _, err := ValidateTOTP("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "12345", time.Now()); err == nil {
		t.Fatal("ValidateTOTP() accepted malformed code")
	}
}

func TestDigestTokenIsKeyedAndStable(t *testing.T) {
	first, err := NewManager([]byte("0123456789abcdef0123456789abcdef"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager([]byte("abcdef0123456789abcdef0123456789"), 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	a := first.DigestToken("synthetic-token")
	b := first.DigestToken("synthetic-token")
	c := second.DigestToken("synthetic-token")
	if string(a) != string(b) {
		t.Fatal("DigestToken() is not deterministic")
	}
	if string(a) == string(c) {
		t.Fatal("DigestToken() did not depend on manager secret")
	}
}
