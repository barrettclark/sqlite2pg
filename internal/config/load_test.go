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

func TestLoad_RejectsV1ConfigWithoutWithoutRowID(t *testing.T) {
	cfg := &MigrationConfig{
		ConfigVersion: 1,
		Tables: map[string]TableConfig{
			"bikes": {Include: true},
		},
	}
	path := filepath.Join(t.TempDir(), "v1.migration.yaml")
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected Load to reject a v1 config")
	}
	if !strings.Contains(err.Error(), "re-run `sqlite2pg profile`") {
		t.Errorf("expected the error to say to re-run `sqlite2pg profile`, got %q", err.Error())
	}
}

func TestLoad_KeepsWithoutRowIDFromCurrentConfig(t *testing.T) {
	cfg := &MigrationConfig{
		ConfigVersion: CurrentConfigVersion,
		Tables: map[string]TableConfig{
			"bikes": {Include: true, WithoutRowID: true},
		},
	}
	path := filepath.Join(t.TempDir(), "wr.migration.yaml")
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Tables["bikes"].WithoutRowID {
		t.Error("without_rowid was lost in the round trip")
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
