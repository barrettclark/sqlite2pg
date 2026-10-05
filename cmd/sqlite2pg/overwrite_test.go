package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

func TestRunProfileIO_OverwriteConfirmation(t *testing.T) {
	const sentinel = "sentinel"
	tests := []struct {
		name        string
		force       bool
		interactive bool
		answer      string
		wantWrite   bool
		wantErr     string
		wantOut     string
	}{
		{name: "no force, non-terminal stdin is refused", interactive: false, wantErr: "pass --force"},
		{name: "force overwrites without asking", force: true, interactive: false, wantWrite: true},
		{name: "terminal y overwrites", interactive: true, answer: "y\n", wantWrite: true},
		{name: "terminal yes overwrites", interactive: true, answer: "yes\n", wantWrite: true},
		{name: "terminal n leaves the file unchanged", interactive: true, answer: "n\n", wantOut: "left "},
		{name: "terminal empty answer leaves the file unchanged", interactive: true, answer: "\n", wantOut: "left "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src.db")
			makeSQLiteFile(t, src)
			out := filepath.Join(dir, "out.migration.yaml")
			if err := os.WriteFile(out, []byte(sentinel), 0o644); err != nil {
				t.Fatalf("seeding --out: %v", err)
			}

			args := []string{"--out", out}
			if tt.force {
				args = append(args, "--force")
			}
			args = append(args, src)
			var prompt bytes.Buffer
			err := runProfileIO(args, strings.NewReader(tt.answer), &prompt, tt.interactive)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), out) {
					t.Fatalf("expected a refusal naming %s and %q, got: %v", out, tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("runProfileIO: %v", err)
			}

			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading --out: %v", err)
			}
			if tt.wantWrite {
				if _, err := config.Load(out); err != nil {
					t.Fatalf("expected --out to be rewritten as a config, got: %v (bytes %q)", err, got)
				}
				return
			}
			if string(got) != sentinel {
				t.Errorf("--out was changed without confirmation: %q", got)
			}
			if tt.wantOut != "" && !strings.Contains(prompt.String(), tt.wantOut) {
				t.Errorf("expected output to contain %q, got %q", tt.wantOut, prompt.String())
			}
		})
	}
}
