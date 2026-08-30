package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCleanupConfigAfterLoad_LoadFailureLeavesConfigInPlace is the
// regression test for issue #38: `migrate run`'s config cleanup used to be
// registered via `defer os.Remove(configPath)` before the review and load
// steps ran, so it fired on every exit path — including a load that failed
// partway through a table. A user who hit a load failure without having
// passed --keep-config up front was left with an orphaned state file
// pointing at a config that no longer existed, with no way to --resume or
// inspect what was decided. cleanupConfigAfterLoad must never remove the
// config when the load itself failed, regardless of --keep-config.
func TestCleanupConfigAfterLoad_LoadFailureLeavesConfigInPlace(t *testing.T) {
	for _, keepConfig := range []bool{false, true} {
		configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
		if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
			t.Fatalf("writing fixture config: %v", err)
		}
		loadErr := errors.New("load failed on table widgets: constraint violation")

		got := cleanupConfigAfterLoad(loadErr, configPath, keepConfig)

		if !errors.Is(got, loadErr) {
			t.Errorf("keepConfig=%v: expected the load error to propagate unchanged, got %v", keepConfig, got)
		}
		if _, err := os.Stat(configPath); err != nil {
			t.Errorf("keepConfig=%v: expected the config file to survive a failed load, but stat failed: %v", keepConfig, err)
		}
	}
}

// TestCleanupConfigAfterLoad_SuccessDeletesConfigUnlessKeepConfig confirms
// the happy path is unchanged: a successful load still deletes the
// generated config unless --keep-config was passed.
func TestCleanupConfigAfterLoad_SuccessDeletesConfigUnlessKeepConfig(t *testing.T) {
	t.Run("deletes by default", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
		if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
			t.Fatalf("writing fixture config: %v", err)
		}

		if err := cleanupConfigAfterLoad(nil, configPath, false); err != nil {
			t.Fatalf("cleanupConfigAfterLoad: %v", err)
		}
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Errorf("expected the config file to be removed after a successful load, stat err: %v", err)
		}
	})

	t.Run("keeps when --keep-config is set", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
		if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
			t.Fatalf("writing fixture config: %v", err)
		}

		if err := cleanupConfigAfterLoad(nil, configPath, true); err != nil {
			t.Fatalf("cleanupConfigAfterLoad: %v", err)
		}
		if _, err := os.Stat(configPath); err != nil {
			t.Errorf("expected the config file to survive with --keep-config, stat err: %v", err)
		}
	})
}
