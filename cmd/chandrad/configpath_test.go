package main

import (
	"os"
	"testing"
)

func TestResolveConfigPath_UsesEnvVar(t *testing.T) {
	os.Setenv("CHANDRA_CONFIG", "/tmp/my-chandra.toml")
	defer os.Unsetenv("CHANDRA_CONFIG")

	_, path := resolveConfigPath()
	if path != "/tmp/my-chandra.toml" {
		t.Fatalf("expected /tmp/my-chandra.toml, got %s", path)
	}
}
