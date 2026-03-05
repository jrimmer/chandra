package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)


// CheckSecretsPermissions verifies that secrets.toml (if present) has
// permissions no wider than 0600. Returns a non-nil error if the file exists
// and has wider permissions, describing the current mode and the required fix.
// Returns nil if the file is absent (no secrets file = no issue).
func CheckSecretsPermissions(dir string) error {
	path := filepath.Join(dir, "secrets.toml")
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil // no secrets file — nothing to check
	}
	if err != nil {
		return fmt.Errorf("config/safety: stat secrets.toml: %w", err)
	}
	mode := fi.Mode().Perm()
	if mode > 0600 {
		return fmt.Errorf(
			"secrets.toml has insecure permissions %04o (must be 0600 or stricter); " +
				"fix with: chmod 600 %s",
			mode, path,
		)
	}
	return nil
}

// SafeWriter handles atomic config writes with backup and rollback.
type SafeWriter struct {
	dir string // directory where config.toml lives
}

// NewSafeWriter returns a SafeWriter rooted at dir.
func NewSafeWriter(dir string) *SafeWriter {
	return &SafeWriter{dir: dir}
}

// configPath returns the path to config.toml.
func (w *SafeWriter) configPath() string {
	return filepath.Join(w.dir, "config.toml")
}

// backupPath returns the path to config.toml.backup.N.
func (w *SafeWriter) backupPath(n int) string {
	return filepath.Join(w.dir, fmt.Sprintf("config.toml.backup.%d", n))
}

// tmpPath returns the path to the temporary file used during atomic write.
func (w *SafeWriter) tmpPath() string {
	return filepath.Join(w.dir, "config.toml.tmp")
}

// WriteConfig validates content as TOML, backs up the existing config, and
// atomically writes the new content.
//
// Algorithm:
//  1. Validate: parse content as TOML. If invalid, return error without
//     touching disk.
//  2. Rotate backups: delete backup.3 if it exists, then
//     backup.2 → backup.3, backup.1 → backup.2, config.toml → backup.1.
//  3. Write to config.toml.tmp (mode 0600).
//  4. Atomically rename config.toml.tmp → config.toml.
func (w *SafeWriter) WriteConfig(content []byte) error {
	// Pre-flight: refuse to write if secrets.toml has insecure permissions.
	// An insecure secrets file means credentials could already be exposed;
	// writing new config in that state would compound the risk.
	if err := CheckSecretsPermissions(w.dir); err != nil {
		return fmt.Errorf("config/safety: write blocked — %w", err)
	}
	// Step 1: validate content as TOML.
	var dummy Config
	if _, err := toml.Decode(string(content), &dummy); err != nil {
		return fmt.Errorf("config/safety: invalid TOML: %w", err)
	}

	cfgPath := w.configPath()

	// Step 2: rotate backups only if config.toml already exists.
	if _, err := os.Stat(cfgPath); err == nil {
		// Delete backup.3 to make room for the rotation.
		bp3 := w.backupPath(3)
		if _, err := os.Stat(bp3); err == nil {
			if err := os.Remove(bp3); err != nil {
				return fmt.Errorf("config/safety: remove backup.3: %w", err)
			}
		}

		// Rotate: backup.2 → backup.3
		bp2 := w.backupPath(2)
		if _, err := os.Stat(bp2); err == nil {
			if err := os.Rename(bp2, bp3); err != nil {
				return fmt.Errorf("config/safety: rotate backup.2 → backup.3: %w", err)
			}
		}

		// Rotate: backup.1 → backup.2
		bp1 := w.backupPath(1)
		if _, err := os.Stat(bp1); err == nil {
			if err := os.Rename(bp1, bp2); err != nil {
				return fmt.Errorf("config/safety: rotate backup.1 → backup.2: %w", err)
			}
		}

		// Rotate: config.toml → backup.1
		if err := os.Rename(cfgPath, bp1); err != nil {
			return fmt.Errorf("config/safety: rotate config.toml → backup.1: %w", err)
		}
	}

	// Step 3: write to tmp file.
	tmpPath := w.tmpPath()
	if err := os.WriteFile(tmpPath, content, 0600); err != nil {
		// Auto-rollback: restore backup.1 → config.toml so the config
		// directory is not left without a config file.
		_ = os.Rename(w.backupPath(1), cfgPath)
		return fmt.Errorf("config/safety: write tmp file: %w", err)
	}

	// Step 4: atomic rename tmp → config.toml.
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		// Auto-rollback: restore backup.1 → config.toml.
		_ = os.Remove(tmpPath)
		_ = os.Rename(w.backupPath(1), cfgPath)
		return fmt.Errorf("config/safety: atomic rename: %w", err)
	}

	return nil
}

// RollbackToLastGood restores config.toml.backup.1 → config.toml if the
// backup exists. Returns an error if no backup is available.
func (w *SafeWriter) RollbackToLastGood() error {
	bp1 := w.backupPath(1)
	if _, err := os.Stat(bp1); os.IsNotExist(err) {
		return fmt.Errorf("config/safety: no backup available at %s", bp1)
	} else if err != nil {
		return fmt.Errorf("config/safety: stat backup.1: %w", err)
	}

	cfgPath := w.configPath()
	if err := os.Rename(bp1, cfgPath); err != nil {
		return fmt.Errorf("config/safety: rollback backup.1 → config.toml: %w", err)
	}
	return nil
}
