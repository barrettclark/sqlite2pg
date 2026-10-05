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

func TestLoad_RejectsV1ConfigWithRepairInstructions(t *testing.T) {
	tests := []struct {
		name   string
		source string
		// want and notWant are built from the config path the test wrote.
		want    func(path string) []string
		notWant []string
	}{
		{
			name:   "source recorded",
			source: "/data/bikes.db",
			want: func(path string) []string {
				return []string{"re-run `sqlite2pg profile --out '" + path + "' '/data/bikes.db'`", path}
			},
		},
		{
			name:   "source empty",
			source: "",
			want: func(path string) []string {
				return []string{"--out '" + path + "'", "replace <your SQLite file> with the path to your SQLite source"}
			},
			notWant: []string{"<source.db>"},
		},
		{
			name:   "path with spaces is quoted",
			source: "/data/my bikes.db",
			want: func(path string) []string {
				return []string{"'/data/my bikes.db'"}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &MigrationConfig{
				ConfigVersion: 1,
				Source:        SourceInfo{Path: tt.source},
				Tables:        map[string]TableConfig{"bikes": {Include: true}},
			}
			path := filepath.Join(t.TempDir(), "reviewed elsewhere.yaml")
			if err := Save(cfg, path); err != nil {
				t.Fatalf("Save: %v", err)
			}

			_, err := Load(path)
			if err == nil {
				t.Fatal("expected Load to reject a v1 config")
			}
			for _, want := range tt.want(path) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected the error to contain %q, got %q", want, err.Error())
				}
			}
			for _, bad := range tt.notWant {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("error should not contain %q, got %q", bad, err.Error())
				}
			}
		})
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
