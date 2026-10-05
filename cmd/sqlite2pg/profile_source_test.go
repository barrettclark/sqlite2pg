package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunProfile_RejectsMissingOrDirectorySourceWithoutTouchingOut(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name   string
		source func(t *testing.T) string
	}{
		{
			name:   "missing source",
			source: func(t *testing.T) string { return filepath.Join(dir, "typo.db") },
		},
		{
			name: "source is a directory",
			source: func(t *testing.T) string {
				d := filepath.Join(dir, "a-dir")
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return d
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := tt.source(t)
			out := filepath.Join(t.TempDir(), "existing.migration.yaml")
			const original = "config_version: 2\n# reviewed by hand\n"
			if err := os.WriteFile(out, []byte(original), 0o644); err != nil {
				t.Fatalf("writing existing config: %v", err)
			}

			err := runProfile([]string{"--out", out, source})
			if err == nil {
				t.Fatal("expected runProfile to reject the source")
			}
			if !strings.Contains(err.Error(), source) {
				t.Errorf("error should name the source %q, got: %v", source, err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading --out: %v", err)
			}
			if string(got) != original {
				t.Errorf("--out was modified:\n%s", got)
			}
		})
	}
}
