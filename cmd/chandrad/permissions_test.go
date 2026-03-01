package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyPermissions_ValidDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	// No config file exists — should pass.
	cfgPath := filepath.Join(dir, "config.toml")
	if err := verifyPermissions(dir, cfgPath); err != nil {
		t.Errorf("expected no error for valid 0700 dir, got: %v", err)
	}
}

func TestVerifyPermissions_WorldReadableDir_Fails(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	err := verifyPermissions(dir, cfgPath)
	if err == nil {
		t.Error("expected error for world-readable dir (0755), got nil")
	}
}

func TestVerifyPermissions_GroupReadableConfig_Fails(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("test"), 0640); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	err := verifyPermissions(dir, cfgPath)
	if err == nil {
		t.Error("expected error for group-readable config file (0640), got nil")
	}
}
