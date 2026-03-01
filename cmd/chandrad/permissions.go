package main

import (
	"fmt"
	"os"
)

// verifyPermissions checks that the config directory and config file
// have restrictive enough permissions (not world-readable or group-readable).
// dir must be mode 0700 or stricter (no group or other bits).
// cfgPath (if it exists) must be mode 0600 or stricter.
func verifyPermissions(dir, cfgPath string) error {
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("permissions: stat config dir %q: %w", dir, err)
	}

	dirMode := dirInfo.Mode().Perm()
	if dirMode&0077 != 0 {
		return fmt.Errorf(
			"permissions: config directory %q has insecure permissions %04o; must be 0700 or stricter (no group or other bits)",
			dir, dirMode,
		)
	}

	// Only check the config file if it exists.
	cfgInfo, err := os.Stat(cfgPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("permissions: stat config file %q: %w", cfgPath, err)
	}

	cfgMode := cfgInfo.Mode().Perm()
	if cfgMode&0077 != 0 {
		return fmt.Errorf(
			"permissions: config file %q has insecure permissions %04o; must be 0600 or stricter (no group or other bits)",
			cfgPath, cfgMode,
		)
	}

	return nil
}
