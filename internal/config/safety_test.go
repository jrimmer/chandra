package config

import (
	"os"
	"path/filepath"
	"testing"
)

// validTOML is a minimal valid config for testing the SafeWriter.
const validTOML = `
[agent]
name = "Chandra"
persona = "A helpful AI assistant"
max_tool_rounds = 5

[provider]
base_url = "http://localhost:11434"
api_key = "test"
model = "llama3"
type = "openai"

[embeddings]
base_url = "http://localhost:11434/v1"
api_key = "test"
model = "nomic-embed-text"
dimensions = 1536

[database]
path = "/tmp/chandra-test.db"

[channels.discord]
token = "test-token"
channel_ids = ["123"]
`

const invalidTOML = `[broken-toml` // malformed

func TestConfigSafety_BackupBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	w := NewSafeWriter(dir)

	// First write — creates config.toml, no backup yet.
	if err := w.WriteConfig([]byte(validTOML)); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.toml should exist after first write: %v", err)
	}

	// Second write — should create backup.1 of the first write.
	modified := validTOML + "\n# second write\n"
	if err := w.WriteConfig([]byte(modified)); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	backup1 := filepath.Join(dir, "config.toml.backup.1")
	if _, err := os.Stat(backup1); err != nil {
		t.Errorf("backup.1 should exist after second write: %v", err)
	}
}

func TestConfigSafety_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	w := NewSafeWriter(dir)

	content := []byte(validTOML)
	if err := w.WriteConfig(content); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	cfgPath := filepath.Join(dir, "config.toml")
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("config.toml content mismatch\nwant: %q\ngot:  %q", content, got)
	}

	// Verify file permissions are 0600.
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config.toml: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config.toml permissions: want 0600, got %04o", perm)
	}
}

func TestConfigSafety_RollbackOnInvalid(t *testing.T) {
	dir := t.TempDir()
	w := NewSafeWriter(dir)

	// Write valid config first.
	if err := w.WriteConfig([]byte(validTOML)); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	// Attempt to write invalid TOML — should be rejected before touching disk.
	err := w.WriteConfig([]byte(invalidTOML))
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}

	// config.toml should still have original content.
	cfgPath := filepath.Join(dir, "config.toml")
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if string(got) != validTOML {
		t.Errorf("config.toml content changed after rejected write\nwant: %q\ngot:  %q", validTOML, got)
	}
}

func TestConfigSafety_KeepsLast3Backups(t *testing.T) {
	dir := t.TempDir()
	w := NewSafeWriter(dir)

	// Write 5 times. Each write after the first rotates backups.
	for i := 0; i < 5; i++ {
		content := validTOML + "\n# iteration " + string(rune('0'+i)) + "\n"
		if err := w.WriteConfig([]byte(content)); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}

	// Only backup.1, .2, .3 should exist.
	for n := 1; n <= 3; n++ {
		path := filepath.Join(dir, "config.toml.backup."+string(rune('0'+n)))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("backup.%d should exist: %v", n, err)
		}
	}

	// backup.4 and backup.5 must NOT exist.
	for n := 4; n <= 5; n++ {
		path := filepath.Join(dir, "config.toml.backup."+string(rune('0'+n)))
		if _, err := os.Stat(path); err == nil {
			t.Errorf("backup.%d should NOT exist, but it does", n)
		}
	}
}
