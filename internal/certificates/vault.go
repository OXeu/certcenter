// Package certificates coordinates certificate issuance, renewal and deployment.
package certificates

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// Vault encrypts database material with authenticated encryption and bound identities.
type Vault struct{ aead cipher.AEAD }

// NewVault requires a base64 encoded AES-256 key supplied by ConfigCenter.
func NewVault(encoded string) (*Vault, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid encryption key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create authenticated cipher: %w", err)
	}
	return &Vault{aead: aead}, nil
}

// Seal encrypts material with a fresh nonce and an authenticated identity.
func (v *Vault) Seal(identity string, data []byte) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate encryption nonce: %w", err)
	}
	return v.aead.Seal(nonce, nonce, data, []byte(identity)), nil
}

// Open verifies and decrypts material belonging to the given identity.
func (v *Vault) Open(identity string, data []byte) ([]byte, error) {
	n := v.aead.NonceSize()
	if len(data) < n {
		return nil, errors.New("invalid encrypted material")
	}
	plain, err := v.aead.Open(nil, data[:n], data[n:], []byte(identity))
	if err != nil {
		return nil, fmt.Errorf("decrypt material: %w", err)
	}
	return plain, nil
}
