package doctor_test

import (
	"context"
	"testing"

	"github.com/jrimmer/chandra/internal/doctor"
	_ "github.com/mattn/go-sqlite3" // register sqlite3 driver for DB check tests
)

func TestConfigCheck_MissingFile(t *testing.T) {
	check := doctor.NewConfigCheck("/nonexistent/path/config.toml")
	result := check.Run(context.Background())
	if result.Status != doctor.Fail {
		t.Errorf("expected Fail for missing config, got %v", result.Status)
	}
}

func TestPermissionsCheck_Name(t *testing.T) {
	check := doctor.NewPermissionsCheck("/tmp", "/tmp/config.toml")
	if check.Name() != "Permissions" {
		t.Errorf("unexpected name: %s", check.Name())
	}
}
