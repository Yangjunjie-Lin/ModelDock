package apikey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
)

type Manager struct{ secret []byte }

func NewManager(secret []byte) (*Manager, error) {
	if len(secret) < 32 {
		return nil, errors.New("API key HMAC secret must be at least 32 bytes")
	}
	return &Manager{secret: append([]byte(nil), secret...)}, nil
}

func (m *Manager) Generate(environment string) (full string, prefix string, hash []byte, err error) {
	if environment != "live" && environment != "test" {
		return "", "", nil, errors.New("environment must be live or test")
	}
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return "", "", nil, err
	}
	full = "rdk_" + environment + "_" + base64.RawURLEncoding.EncodeToString(random)
	prefixLen := len("rdk_"+environment+"_") + 8
	prefix = full[:prefixLen]
	hash = m.Hash(full)
	return full, prefix, hash, nil
}

func (m *Manager) Hash(full string) []byte {
	h := hmac.New(sha256.New, m.secret)
	_, _ = h.Write([]byte(full))
	return h.Sum(nil)
}

func (m *Manager) Verify(full string, expected []byte) bool {
	actual := m.Hash(full)
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}

func LooksValid(v string) bool {
	if !(strings.HasPrefix(v, "rdk_live_") || strings.HasPrefix(v, "rdk_test_")) {
		return false
	}
	parts := strings.SplitN(v, "_", 3)
	if len(parts) != 3 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[2])
	return err == nil && len(b) == 32
}
