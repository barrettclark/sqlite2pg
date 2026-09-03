package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCleanupConfigAfterLoad_LoadFailureLeavesConfigInPlace is the
// regression test for issue #38: `sqlite2pg run`'s config cleanup used to be
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

		statePath := configPath + ".state.json"
		if err := os.WriteFile(statePath, []byte(`{"database":"sqlite2pg_source_abc123","completed":["widgets"]}`), 0o644); err != nil {
			t.Fatalf("writing fixture state file: %v", err)
		}

		got := cleanupConfigAfterLoad(loadErr, configPath, keepConfig)

		if !errors.Is(got, loadErr) {
			t.Errorf("keepConfig=%v: expected the load error to propagate unchanged, got %v", keepConfig, got)
		}
		if _, err := os.Stat(configPath); err != nil {
			t.Errorf("keepConfig=%v: expected the config file to survive a failed load, but stat failed: %v", keepConfig, err)
		}
		if _, err := os.Stat(statePath); err != nil {
			t.Errorf("keepConfig=%v: expected the state file to survive a failed load, but stat failed: %v", keepConfig, err)
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
		statePath := configPath + ".state.json"
		if err := os.WriteFile(statePath, []byte(`{"database":"sqlite2pg_source_abc123","completed":["widgets"]}`), 0o644); err != nil {
			t.Fatalf("writing fixture state file: %v", err)
		}

		if err := cleanupConfigAfterLoad(nil, configPath, false); err != nil {
			t.Fatalf("cleanupConfigAfterLoad: %v", err)
		}
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Errorf("expected the config file to be removed after a successful load, stat err: %v", err)
		}
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Errorf("expected the state file to be removed after a successful load, stat err: %v", err)
		}
	})

	t.Run("keeps when --keep-config is set", func(t *testing.T) {
		configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
		if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
			t.Fatalf("writing fixture config: %v", err)
		}
		statePath := configPath + ".state.json"
		if err := os.WriteFile(statePath, []byte(`{"database":"sqlite2pg_source_abc123","completed":["widgets"]}`), 0o644); err != nil {
			t.Fatalf("writing fixture state file: %v", err)
		}

		if err := cleanupConfigAfterLoad(nil, configPath, true); err != nil {
			t.Fatalf("cleanupConfigAfterLoad: %v", err)
		}
		if _, err := os.Stat(configPath); err != nil {
			t.Errorf("expected the config file to survive with --keep-config, stat err: %v", err)
		}
		if _, err := os.Stat(statePath); err != nil {
			t.Errorf("expected the state file to survive with --keep-config, stat err: %v", err)
		}
	})

	t.Run("succeeds when the state file was never created", func(t *testing.T) {
		// A load that succeeds on its very first attempt, with no prior
		// partial run to resume, may never have written a state file at
		// all if it fails before the first markTableCompleted call — or,
		// for an empty config, never call it. Cleanup must tolerate that
		// rather than erroring on a missing state file.
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
}

// TestRunFinish_VerificationFailureKeepsConfigAndStateFile covers issue
// #62: a post-load verification failure means the load succeeded but the
// data in Postgres doesn't match the source — a real data-integrity
// finding. The user needs the generated config (to see which type
// decisions produced the bad data) and the .state.json (to re-run
// `sqlite2pg verify` for the full report, and to know which timestamped
// database the data landed in). runRunFinish must NOT run cleanup when
// verification failed; both files survive and the verify error is
// returned unchanged.
func TestRunFinish_VerificationFailureKeepsConfigAndStateFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
	if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
		t.Fatalf("writing fixture config: %v", err)
	}
	statePath := configPath + ".state.json"
	if err := os.WriteFile(statePath, []byte(`{"database":"source_20260101_000000","completed":["widgets"]}`), 0o644); err != nil {
		t.Fatalf("writing fixture state file: %v", err)
	}

	verifyErr := errors.New("post-load verification FAILED: 1 table(s) with row-count mismatches, 3 value mismatch(es) across 2 table(s) checked")

	got := runRunFinish(verifyErr, configPath, false)

	if !errors.Is(got, verifyErr) {
		t.Errorf("expected the verify error to be returned unchanged, got %v", got)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected the config to be kept after a verification failure, stat failed: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("expected the state file to be kept after a verification failure, stat failed: %v", err)
	}
}

// TestRunFinish_SuccessStillDeletesConfig confirms the fix is scoped to
// the failure case: a passing verification leaves cleanup behaving exactly
// as before (delete the generated config unless --keep-config).
func TestRunFinish_SuccessStillDeletesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
	if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
		t.Fatalf("writing fixture config: %v", err)
	}

	if got := runRunFinish(nil, configPath, false); got != nil {
		t.Errorf("expected nil on the fully-successful path, got %v", got)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("expected the config to be removed on the success path")
	}
}

// TestRunFinish_NoErrorsReturnsNil confirms the fully-successful path
// (verification passed, cleanup succeeded) still returns nil.
func TestRunFinish_NoErrorsReturnsNil(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
	if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
		t.Fatalf("writing fixture config: %v", err)
	}
	if got := runRunFinish(nil, configPath, false); got != nil {
		t.Errorf("expected nil when neither verification nor cleanup fail, got %v", got)
	}
}
