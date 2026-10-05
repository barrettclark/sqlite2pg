package main

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

// requireSymlink creates link -> target. A platform that refuses symlinks for
// this user skips the subtest, unless CI is set: then the skip is a failure, so
// a runner can't quietly drop the symlink cases. Any other error fails.
func requireSymlink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err == nil {
		return
	}
	if errors.Is(err, fs.ErrPermission) || symlinkPrivilegeError(err) {
		msg := "symlink creation is not permitted here: " + err.Error()
		if os.Getenv("CI") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	t.Fatalf("creating symlink %s -> %s: %v", link, target, err)
}

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
	const sentinel = "sentinel"
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
				requireSymlink(t, target, link)
				return link
			},
		},
		{
			name: "dangling symlink is rejected and its target not created",
			setup: func(t *testing.T, dir string) string {
				link := filepath.Join(dir, "dangling.db")
				requireSymlink(t, filepath.Join(dir, "gone.db"), link)
				return link
			},
			wantErr:    "dangling.db",
			wantAbsent: func(source string) string { return filepath.Join(filepath.Dir(source), "gone.db") },
		},
		{
			name:    "FIFO is rejected as not a regular file",
			setup:   func(t *testing.T, dir string) string { return makeFIFO(t, dir) },
			wantErr: "not a regular file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			source := tt.setup(t, dir)
			out := filepath.Join(t.TempDir(), "out.migration.yaml")
			// The success case starts with no --out, so nothing is overwritten.
			if tt.wantErr != "" {
				if err := os.WriteFile(out, []byte(sentinel), 0o644); err != nil {
					t.Fatalf("seeding --out: %v", err)
				}
			}

			err := runProfile([]string{"--out", out, source})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected profile to succeed, got: %v", err)
				}
				got, err := os.ReadFile(out)
				if err != nil {
					t.Fatalf("reading --out: %v", err)
				}
				if len(got) == 0 {
					t.Fatal("--out is empty after a successful profile")
				}
				cfg, err := config.Load(out)
				if err != nil {
					t.Fatalf("--out does not parse as a config: %v", err)
				}
				if _, ok := cfg.Tables["t"]; !ok {
					t.Errorf("--out has no table t, tables: %v", cfg.Tables)
				}
				return
			}

			if err == nil {
				t.Fatal("expected runProfile to reject the source")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error should contain %q, got: %v", tt.wantErr, err)
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("reading --out: %v", err)
			}
			if string(got) != sentinel {
				t.Errorf("--out was modified by a rejected run: %q", got)
			}
			if tt.wantAbsent != nil {
				if _, statErr := os.Stat(tt.wantAbsent(source)); !os.IsNotExist(statErr) {
					t.Errorf("%s was created by the rejected run, stat err = %v", tt.wantAbsent(source), statErr)
				}
			}
		})
	}
}
