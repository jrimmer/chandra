package infra

import (
	"os"
	"testing"
)

func TestCredentialEncryption_RoundTrip(t *testing.T) {
	key := DeriveKey("test-passphrase", []byte("test-salt-16byte"))
	plaintext := "ssh-rsa AAAA..."

	encrypted, err := Encrypt(key, []byte(plaintext))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	decrypted, err := Decrypt(key, encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if string(decrypted) != plaintext {
		t.Errorf("expected %q, got %q", plaintext, string(decrypted))
	}
}

func TestCredentialEncryption_WrongKey(t *testing.T) {
	key1 := DeriveKey("passphrase-1", []byte("test-salt-16byte"))
	key2 := DeriveKey("passphrase-2", []byte("test-salt-16byte"))

	encrypted, _ := Encrypt(key1, []byte("secret"))
	_, err := Decrypt(key2, encrypted)
	if err == nil {
		t.Error("expected error with wrong key")
	}
}

func TestCredentialEncryption_DifferentCiphertext(t *testing.T) {
	key := DeriveKey("test", []byte("test-salt-16byte"))
	plaintext := []byte("same-plaintext")

	enc1, _ := Encrypt(key, plaintext)
	enc2, _ := Encrypt(key, plaintext)

	// Each encryption should produce different ciphertext (random nonce).
	if string(enc1) == string(enc2) {
		t.Error("expected different ciphertext for same plaintext (random nonce)")
	}
}

func TestKeyProvider_PassphraseProvider(t *testing.T) {
	salt := []byte("sixteen-byte-sal")
	provider := &PassphraseProvider{Salt: salt}

	// Without env var, should fail.
	os.Unsetenv("CHANDRA_PASSPHRASE")
	_, err := provider.GetKey()
	if err == nil {
		t.Error("expected error without CHANDRA_PASSPHRASE set")
	}

	// With env var, should succeed.
	os.Setenv("CHANDRA_PASSPHRASE", "test-passphrase")
	defer os.Unsetenv("CHANDRA_PASSPHRASE")

	key, err := provider.GetKey()
	if err != nil {
		t.Fatalf("expected no error with CHANDRA_PASSPHRASE set: %v", err)
	}
	if len(key) != 32 {
		t.Errorf("expected 32-byte key, got %d bytes", len(key))
	}
}

func TestKeyProvider_StaticProvider(t *testing.T) {
	key := DeriveKey("test", []byte("test-salt-16byte"))
	provider := &StaticKeyProvider{Key: key}

	got, err := provider.GetKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(key) {
		t.Error("expected same key back from static provider")
	}
}

func TestDeriveKey_Length(t *testing.T) {
	key := DeriveKey("any-passphrase", []byte("sixteen-byte-sal"))
	if len(key) != 32 {
		t.Errorf("expected 32-byte key (AES-256), got %d bytes", len(key))
	}
}

func TestMaskCredential(t *testing.T) {
	masked := MaskCredential("ssh-rsa AAAA...")
	if masked != "****" {
		t.Errorf("expected '****', got %q", masked)
	}

	empty := MaskCredential("")
	if empty != "" {
		t.Errorf("expected empty string, got %q", empty)
	}
}
