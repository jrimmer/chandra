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

func TestDaemonCheck_NoSocket(t *testing.T) {
	// Use a guaranteed-nonexistent path so the test is not affected by daemon state.
	check := doctor.NewDaemonCheck("/tmp/chandra-test-nonexistent.sock")
	result := check.Run(context.Background())
	if result.Status != doctor.Warn {
		t.Errorf("expected Warn when socket unreachable, got %v", result.Status)
	}
}

func TestSchedulerCheck_NoSocket(t *testing.T) {
	check := doctor.NewSchedulerCheck("/tmp/chandra-test-nonexistent.sock")
	result := check.Run(context.Background())
	if result.Status != doctor.Warn {
		t.Errorf("expected Warn when daemon not running, got %v", result.Status)
	}
}
