package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// TestRunFinish_BothVerifyAndCleanupErrorsArePreserved is the regression
// test for the Copilot PR #59 finding that a genuine post-load
// verification failure (real data-integrity finding — by far the more
// important thing to know about) could be silently replaced by a
// subsequent cleanup error, e.g. a filesystem permission error removing
// the generated config. runRunFinish (the tail of runRun, extracted here
// for direct testing) must preserve BOTH errors when both occur, via
// errors.Join, rather than letting the cleanup error overwrite the
// verification error.
//
// The cleanup failure is forced for real here, the same way the rest of
// this file forces cleanup scenarios: configPath is made a non-empty
// directory rather than a plain file, so os.Remove(configPath) inside
// cleanupConfigAfterLoad genuinely fails ("directory not empty"), instead
// of mocking the failure.
func TestRunFinish_BothVerifyAndCleanupErrorsArePreserved(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "source.db.migration.yaml")
	if err := os.Mkdir(configPath, 0o755); err != nil {
		t.Fatalf("making configPath a directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configPath, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing a file inside configPath so os.Remove fails: %v", err)
	}

	verifyErr := errors.New("post-load verification FAILED: 1 table(s) with row-count mismatches, 3 value mismatch(es) across 2 table(s) checked")

	got := runRunFinish(verifyErr, configPath, false)
	if got == nil {
		t.Fatal("expected a non-nil combined error when both verification and cleanup fail")
	}
	if !errors.Is(got, verifyErr) {
		t.Errorf("expected errors.Is to find the original verification error in the joined result, got %v", got)
	}
	if !strings.Contains(got.Error(), "row-count mismatches") {
		t.Errorf("expected the verification failure text to survive in the combined error, got %q", got.Error())
	}
	if !strings.Contains(got.Error(), "removing generated config") {
		t.Errorf("expected the cleanup failure text to also be present in the combined error, got %q", got.Error())
	}
}

// TestRunFinish_VerifyErrorAloneIsReturnedUnchanged confirms the common
// case (verification fails, cleanup succeeds) is unaffected by the join.
func TestRunFinish_VerifyErrorAloneIsReturnedUnchanged(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "source.db.migration.yaml")
	if err := os.WriteFile(configPath, []byte("tables: {}\n"), 0o644); err != nil {
		t.Fatalf("writing fixture config: %v", err)
	}
	verifyErr := errors.New("post-load verification FAILED: 1 table(s) with row-count mismatches")

	got := runRunFinish(verifyErr, configPath, false)
	if !errors.Is(got, verifyErr) {
		t.Errorf("expected the verify error to be present, got %v", got)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("expected the config to still be removed when cleanup itself succeeds")
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
