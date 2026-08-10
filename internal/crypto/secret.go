package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const envelopeVersion byte = 1

type Vault struct{ aead cipher.AEAD }

func NewVault(key []byte) (*Vault, error) {
	if len(key) != 32 {
		return nil, errors.New("AES-256-GCM key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Encrypt(secret string, associatedID string) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("secret is empty")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 1+len(nonce))
	out[0] = envelopeVersion
	copy(out[1:], nonce)
	out = v.aead.Seal(out, nonce, []byte(secret), []byte(associatedID))
	return out, nil
}

func (v *Vault) Decrypt(envelope []byte, associatedID string) (string, error) {
	if len(envelope) < 1+v.aead.NonceSize()+v.aead.Overhead() || envelope[0] != envelopeVersion {
		return "", errors.New("invalid encrypted secret envelope")
	}
	nonceEnd := 1 + v.aead.NonceSize()
	plain, err := v.aead.Open(nil, envelope[1:nonceEnd], envelope[nonceEnd:], []byte(associatedID))
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plain), nil
}

func Last4(secret string) string {
	if len(secret) <= 4 {
		return secret
	}
	return secret[len(secret)-4:]
}
