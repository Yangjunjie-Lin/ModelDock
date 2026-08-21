package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 SHA-1 profile is required for authenticator interoperability and is used only inside HMAC.
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP is implemented with the RFC 6238 SHA-1 profile used by mainstream
// authenticator applications. The implementation is deliberately small and
// has no network or account-provisioning behavior.
func GenerateTOTP(email string) (secret, uri string, err error) {
	raw := make([]byte, 20)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	label := url.PathEscape("ModelDock:" + strings.ToLower(strings.TrimSpace(email)))
	issuer := url.QueryEscape("ModelDock")
	uri = "otpauth://totp/" + label + "?secret=" + secret + "&issuer=" + issuer + "&algorithm=SHA1&digits=6&period=30"
	return secret, uri, nil
}

// ValidateTOTP returns the matched 30-second counter. Callers persist this
// value with a compare-and-swap so one-time passwords cannot be replayed.
func ValidateTOTP(secret, code string, now time.Time) (int64, error) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, errors.New("invalid MFA code")
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(raw) < 16 {
		return 0, errors.New("invalid MFA secret")
	}
	for offset := int64(-1); offset <= 1; offset++ {
		counter := now.UTC().Unix()/30 + offset
		if counter < 0 {
			continue
		}
		message := make([]byte, 8)
		// #nosec G115 -- negative counters are rejected immediately above.
		binary.BigEndian.PutUint64(message, uint64(counter))
		mac := hmac.New(sha1.New, raw)
		_, _ = mac.Write(message)
		sum := mac.Sum(nil)
		index := sum[len(sum)-1] & 0x0f
		value := (uint32(sum[index])&0x7f)<<24 |
			uint32(sum[index+1])<<16 |
			uint32(sum[index+2])<<8 |
			uint32(sum[index+3])
		expected := fmt.Sprintf("%06d", value%1000000)
		if hmac.Equal([]byte(expected), []byte(code)) {
			return counter, nil
		}
	}
	return 0, errors.New("invalid MFA code")
}

func TOTPCode(secret string, at time.Time) string {
	counter := at.UTC().Unix() / 30
	if counter < 0 {
		return ""
	}
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	message := make([]byte, 8)
	// #nosec G115 -- negative counters are rejected immediately above.
	binary.BigEndian.PutUint64(message, uint64(counter))
	mac := hmac.New(sha1.New, raw)
	_, _ = mac.Write(message)
	sum := mac.Sum(nil)
	index := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[index])&0x7f)<<24 | uint32(sum[index+1])<<16 | uint32(sum[index+2])<<8 | uint32(sum[index+3])
	return fmt.Sprintf("%06d", value%1000000)
}
