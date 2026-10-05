package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSave_FailedWriteLeavesOriginalAndNoTempFile(t *testing.T) {
	const original = "config_version: 2\n# reviewed by hand\n"
	tests := []struct {
		name  string
		write func(f *os.File, data []byte) error
	}{
		{
			name: "write fails partway through",
			write: func(f *os.File, data []byte) error {
				f.Write(data[:len(data)/2])
				return errors.New("no space left on device")
			},
		},
		{
			name:  "write fails before any byte",
			write: func(f *os.File, data []byte) error { return errors.New("I/O error") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "bikes.migration.yaml")
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatalf("seeding config: %v", err)
			}

			data := []byte("config_version: 2\ntables: {bikes: {include: true}}\n")
			if err := writeAtomic(path, data, tt.write); err == nil {
				t.Fatal("expected writeAtomic to fail")
			}

			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, []byte(original)) {
				t.Errorf("original config changed after a failed write: %q, %v", got, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("reading dir: %v", err)
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".tmp") {
					t.Errorf("temp file left behind: %s", e.Name())
				}
			}
		})
	}
}

func TestSave_ReplacesContentAndKeepsExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bikes.migration.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
	cfg := &MigrationConfig{ConfigVersion: CurrentConfigVersion, Tables: map[string]TableConfig{"bikes": {Include: true}}}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !loaded.Tables["bikes"].Include {
		t.Error("saved table lost in the round trip")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("existing file mode changed to %v, want 0600", info.Mode().Perm())
	}
}
