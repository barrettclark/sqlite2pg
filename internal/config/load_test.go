package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_AcceptsTheCurrentConfigVersion(t *testing.T) {
	cfg := &MigrationConfig{
		ConfigVersion: CurrentConfigVersion,
		Tables: map[string]TableConfig{
			"bikes": {Include: true},
		},
	}
	path := filepath.Join(t.TempDir(), "current.migration.yaml")
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load: expected the current config version to load cleanly, got %v", err)
	}
}

func TestLoad_RejectsAMismatchedConfigVersion(t *testing.T) {
	cfg := &MigrationConfig{
		ConfigVersion: CurrentConfigVersion + 1,
		Tables: map[string]TableConfig{
			"bikes": {Include: true},
		},
	}
	path := filepath.Join(t.TempDir(), "future.migration.yaml")
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected Load to reject a config_version other than the current schema version")
	}
	if !strings.Contains(err.Error(), "sqlite2pg profile") {
		t.Errorf("expected the error to point at re-running sqlite2pg profile, got %q", err.Error())
	}
}
