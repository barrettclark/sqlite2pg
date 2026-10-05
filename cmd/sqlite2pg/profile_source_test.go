package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// makeSQLiteFile creates a real SQLite database at path with one table.
func makeSQLiteFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
}

func TestRunProfile_SourceChecks(t *testing.T) {
	tests := []struct {
		name string
		// setup returns the source path to pass to profile.
		setup func(t *testing.T, dir string) string
		// wantErr is a substring of the expected error; empty means profile succeeds.
		wantErr string
		// wantAbsent is a path that must still not exist after the run.
		wantAbsent func(source string) string
	}{
		{
			name:       "missing source is rejected and not created",
			setup:      func(t *testing.T, dir string) string { return filepath.Join(dir, "typo.db") },
			wantErr:    "typo.db",
			wantAbsent: func(source string) string { return source },
		},
		{
			name: "directory is rejected",
			setup: func(t *testing.T, dir string) string {
				d := filepath.Join(dir, "a-dir")
				if err := os.Mkdir(d, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				return d
			},
			wantErr: "not a regular file",
		},
		{
			name: "symlink to a regular file is accepted",
			setup: func(t *testing.T, dir string) string {
				target := filepath.Join(dir, "real.db")
				makeSQLiteFile(t, target)
				link := filepath.Join(dir, "link.db")
				if err := os.Symlink(target, link); err != nil {
					t.Fatalf("symlink: %v", err)
				}
				return link
			},
		},
		{
			name: "dangling symlink is rejected and its target not created",
			setup: func(t *testing.T, dir string) string {
				link := filepath.Join(dir, "dangling.db")
				if err := os.Symlink(filepath.Join(dir, "gone.db"), link); err != nil {
					t.Fatalf("symlink: %v", err)
				}
				return link
			},
			wantErr:    "dangling.db",
			wantAbsent: func(source string) string { return filepath.Join(filepath.Dir(source), "gone.db") },
		},
		{
			name: "FIFO is rejected as not a regular file",
			setup: func(t *testing.T, dir string) string {
				fifo := filepath.Join(dir, "pipe.db")
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					t.Fatalf("mkfifo: %v", err)
				}
				return fifo
			},
			wantErr: "not a regular file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := tt.setup(t, dir)
			out := filepath.Join(t.TempDir(), "out.migration.yaml")

			err := runProfile([]string{"--out", out, source})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected profile to succeed, got: %v", err)
				}
				if _, statErr := os.Stat(out); statErr != nil {
					t.Fatalf("expected --out to be written: %v", statErr)
				}
				return
			}
			if err == nil {
				t.Fatal("expected runProfile to reject the source")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error should contain %q, got: %v", tt.wantErr, err)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Errorf("--out must not be written on rejection, stat err = %v", statErr)
			}
			if tt.wantAbsent != nil {
				if _, statErr := os.Stat(tt.wantAbsent(source)); !os.IsNotExist(statErr) {
					t.Errorf("%s was created by the rejected run, stat err = %v", tt.wantAbsent(source), statErr)
				}
			}
		})
	}
}
