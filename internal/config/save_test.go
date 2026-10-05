package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			found = append(found, e.Name())
		}
	}
	return found
}

func TestSave_FailedWriteLeavesOriginalAndNoTempFile(t *testing.T) {
	const original = "config_version: 2\n# reviewed by hand\n"
	data := []byte("config_version: 2\ntables: {bikes: {include: true}}\n")
	tests := []struct {
		name string
		// wantSize is the temp file's length inside the failing write, which
		// proves how much of the partial write reached the file.
		wantSize int
		write    func(f *os.File, data []byte, sizeSeen *int64) error
	}{
		{
			name:     "write fails partway through",
			wantSize: len(data) / 2,
			write: func(f *os.File, data []byte, sizeSeen *int64) error {
				if _, err := f.Write(data[:len(data)/2]); err != nil {
					return err
				}
				st, err := f.Stat()
				if err != nil {
					return err
				}
				*sizeSeen = st.Size()
				return errors.New("no space left on device")
			},
		},
		{
			name:     "write fails before any byte",
			wantSize: 0,
			write: func(f *os.File, data []byte, sizeSeen *int64) error {
				st, err := f.Stat()
				if err != nil {
					return err
				}
				*sizeSeen = st.Size()
				return errors.New("I/O error")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "bikes.migration.yaml")
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatalf("seeding config: %v", err)
			}

			var sizeSeen int64 = -1
			write := func(f *os.File, d []byte) error { return tt.write(f, d, &sizeSeen) }
			if err := writeAtomic(path, data, write); err == nil {
				t.Fatal("expected writeAtomic to fail")
			}
			if sizeSeen != int64(tt.wantSize) {
				t.Errorf("temp file length at failure = %d, want %d", sizeSeen, tt.wantSize)
			}

			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, []byte(original)) {
				t.Errorf("original config changed after a failed write: %q, %v", got, err)
			}
			if tmps := tempFilesIn(t, dir); len(tmps) != 0 {
				t.Errorf("temp files left behind: %v", tmps)
			}
		})
	}
}

func TestWriteAtomic_RenameFailureLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	// A directory at path makes the rename over it fail.
	path := filepath.Join(dir, "bikes.migration.yaml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeAtomic(path, []byte("x"), writeAll); err == nil {
		t.Fatal("expected the rename over a directory to fail")
	}
	if tmps := tempFilesIn(t, dir); len(tmps) != 0 {
		t.Errorf("temp files left behind after a rename failure: %v", tmps)
	}
}

func TestSave_NewFileGetsUmaskedDefaultMode(t *testing.T) {
	dir := t.TempDir()
	// os.WriteFile applies the same umask to 0644 as the atomic path does, so
	// a reference file in the same directory gives the expected mode.
	ref := filepath.Join(dir, "ref.txt")
	if err := os.WriteFile(ref, nil, 0o644); err != nil {
		t.Fatalf("writing reference: %v", err)
	}
	refInfo, err := os.Stat(ref)
	if err != nil {
		t.Fatalf("stat reference: %v", err)
	}

	path := filepath.Join(dir, "new.migration.yaml")
	cfg := &MigrationConfig{ConfigVersion: CurrentConfigVersion}
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != refInfo.Mode().Perm() {
		t.Errorf("new file mode = %v, want %v (0644 with the umask)", info.Mode().Perm(), refInfo.Mode().Perm())
	}
}

func TestWriteAtomic_SymlinkIsReplacedNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	const original = "real target, must not change"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("seeding target: %v", err)
	}
	link := filepath.Join(dir, "link.migration.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg := &MigrationConfig{ConfigVersion: CurrentConfigVersion}
	if err := Save(cfg, link); err != nil {
		t.Fatalf("Save through a symlink: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("the symlink at path was not replaced")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != original {
		t.Errorf("the symlink target was written through: %q, %v", got, err)
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
