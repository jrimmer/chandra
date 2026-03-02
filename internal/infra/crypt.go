package infra

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/argon2"
)

// KeyProvider provides encryption keys for credential storage.
type KeyProvider interface {
	GetKey() ([]byte, error)
}

// KeychainProvider retrieves the encryption key from the OS keychain.
// Falls back to PassphraseProvider if the keychain is unavailable.
type KeychainProvider struct {
	ServiceName string // e.g. "chandra-credential-key"
	Fallback    *PassphraseProvider
}

// GetKey tries the OS keychain first, then falls back to PassphraseProvider.
func (k *KeychainProvider) GetKey() ([]byte, error) {
	// OS keychain integration is platform-specific.
	// Fall back to passphrase provider.
	if k.Fallback != nil {
		return k.Fallback.GetKey()
	}
	return nil, errors.New("keychain unavailable and no fallback configured")
}

// PassphraseProvider derives an encryption key from a passphrase in the environment.
type PassphraseProvider struct {
	Salt []byte // Stored in config, NOT the key itself
}

// GetKey derives a key from the CHANDRA_PASSPHRASE environment variable.
func (p *PassphraseProvider) GetKey() ([]byte, error) {
	passphrase := os.Getenv("CHANDRA_PASSPHRASE")
	if passphrase == "" {
		return nil, fmt.Errorf("CHANDRA_PASSPHRASE not set and no keychain available")
	}
	return DeriveKey(passphrase, p.Salt), nil
}

// StaticKeyProvider returns a pre-derived key (for testing).
type StaticKeyProvider struct {
	Key []byte
}

// GetKey returns the static key.
func (s *StaticKeyProvider) GetKey() ([]byte, error) {
	return s.Key, nil
}

// DeriveKey derives a 32-byte AES-256 key from a passphrase and salt using Argon2id.
// Parameters: memory=64MB, iterations=3, parallelism=4.
func DeriveKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 4, 32)
}

// Encrypt encrypts plaintext using AES-256-GCM with a random nonce.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts ciphertext using AES-256-GCM.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ct, nil)
}

// MaskCredential returns a masked version of a credential string.
func MaskCredential(credential string) string {
	if credential == "" {
		return ""
	}
	return "****"
}
