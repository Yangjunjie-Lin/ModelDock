package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
)

type Claims struct {
	Role      string `json:"role"`
	Email     string `json:"email"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret          []byte
	accessLifetime  time.Duration
	refreshLifetime time.Duration
}

func NewManager(secret []byte, accessLifetime time.Duration) (*Manager, error) {
	return NewManagerWithRefresh(secret, accessLifetime, 7*24*time.Hour)
}

func NewManagerWithRefresh(secret []byte, accessLifetime, refreshLifetime time.Duration) (*Manager, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT secret must be at least 32 bytes")
	}
	if accessLifetime <= 0 || refreshLifetime <= accessLifetime {
		return nil, errors.New("JWT lifetimes are invalid")
	}
	return &Manager{
		secret:          append([]byte(nil), secret...),
		accessLifetime:  accessLifetime,
		refreshLifetime: refreshLifetime,
	}, nil
}

// HashPassword produces an Argon2id PHC-style value. Parameters are stored
// with the hash so they can be raised later without invalidating old users.
func HashPassword(password string) (string, error) {
	if len(password) < 12 {
		return "", errors.New("password must contain at least 12 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest)), nil
}

func VerifyPassword(encoded, password string) bool {
	// Existing RelayDock deployments used bcrypt before the Argon2id upgrade.
	// Keep verification compatibility so an upgrade never locks out users;
	// callers can rehash to Argon2id after a successful legacy login.
	if strings.HasPrefix(encoded, "$2a$") || strings.HasPrefix(encoded, "$2b$") || strings.HasPrefix(encoded, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	// Refuse malicious database values that could force excessive work.
	if memory < 8*1024 || memory > 1024*1024 || iterations < 1 || iterations > 10 || threads < 1 || threads > 32 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func (m *Manager) Issue(userID, email, role string) (string, time.Time, error) {
	return m.issue(userID, email, role, "access", m.accessLifetime)
}

func (m *Manager) IssueRefresh(userID, email, role string) (string, time.Time, error) {
	return m.issue(userID, email, role, "refresh", m.refreshLifetime)
}

func (m *Manager) issue(userID, email, role, tokenType string, lifetime time.Duration) (string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(lifetime)
	claims := Claims{Role: role, Email: email, TokenType: tokenType, RegisteredClaims: jwt.RegisteredClaims{
		Subject: userID, Issuer: "relayedock", Audience: jwt.ClaimStrings{"relayedock-control"},
		IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(exp),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	return signed, exp, err
}

func (m *Manager) Parse(raw string) (*Claims, error) {
	return m.parse(raw, "access")
}

func (m *Manager) ParseRefresh(raw string) (*Claims, error) {
	return m.parse(raw, "refresh")
}

func (m *Manager) parse(raw, expectedType string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithAudience("relayedock-control"), jwt.WithIssuer("relayedock"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, errors.New("invalid or expired session")
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims.Subject == "" || claims.TokenType != expectedType {
		return nil, errors.New("invalid session claims")
	}
	return claims, nil
}

func CSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
