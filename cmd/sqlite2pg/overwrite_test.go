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
		{name: "terminal Y uppercase overwrites", interactive: true, answer: "Y\n", wantWrite: true},
		{name: "terminal YES uppercase overwrites", interactive: true, answer: "YES\n", wantWrite: true},
		{name: "terminal y without a newline is declined at EOF", interactive: true, answer: "y", wantOut: "no answer"},
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
			state := out + ".state.json"
			if err := writeState(state, loadState{Database: "old_db", Completed: []string{"t"}}); err != nil {
				t.Fatalf("seeding state: %v", err)
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
			_, stateErr := os.Stat(state)
			if tt.wantWrite {
				if _, err := config.Load(out); err != nil {
					t.Fatalf("expected --out to be rewritten as a config, got: %v (bytes %q)", err, got)
				}
				if !os.IsNotExist(stateErr) {
					t.Errorf("state survived a written config: %v", stateErr)
				}
				return
			}
			if string(got) != sentinel {
				t.Errorf("--out was changed without confirmation: %q", got)
			}
			if stateErr != nil {
				t.Errorf("a refused overwrite removed the load state: %v", stateErr)
			}
			if tt.wantOut != "" && !strings.Contains(prompt.String(), tt.wantOut) {
				t.Errorf("expected output to contain %q, got %q", tt.wantOut, prompt.String())
			}
		})
	}
}

func TestRunProfileIO_RefusesSymlinkOut(t *testing.T) {
	tests := []struct {
		name string
		// target is the link's destination; it must be byte-identical after the run.
		target  func(dir string) string
		existed bool
	}{
		{name: "link to an existing file", target: func(dir string) string { return filepath.Join(dir, "real.yaml") }, existed: true},
		{name: "dangling link", target: func(dir string) string { return filepath.Join(dir, "gone.yaml") }, existed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src.db")
			makeSQLiteFile(t, src)
			target := tt.target(dir)
			if tt.existed {
				if err := os.WriteFile(target, []byte("sentinel"), 0o644); err != nil {
					t.Fatalf("seeding target: %v", err)
				}
			}
			out := filepath.Join(dir, "out.migration.yaml")
			requireSymlink(t, target, out)

			for _, force := range []bool{false, true} {
				args := []string{"--out", out}
				if force {
					args = append(args, "--force")
				}
				err := runProfileIO(append(args, src), strings.NewReader("y\n"), &bytes.Buffer{}, true)
				if err == nil || !strings.Contains(err.Error(), "symbolic link") {
					t.Fatalf("force=%v: expected a symlink refusal, got: %v", force, err)
				}
			}
			got, statErr := os.ReadFile(target)
			if tt.existed {
				if statErr != nil || string(got) != "sentinel" {
					t.Errorf("link target was written through: %q, %v", got, statErr)
				}
			} else if !os.IsNotExist(statErr) {
				t.Errorf("dangling link target was created: %v", statErr)
			}
		})
	}
}

func TestRunProfileIO_RemovesStaleLoadState(t *testing.T) {
	tests := []struct {
		name           string
		existingConfig bool
		force          bool
	}{
		{name: "no config yet, stale state beside it", existingConfig: false},
		{name: "forced overwrite of an existing config", existingConfig: true, force: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src.db")
			makeSQLiteFile(t, src)
			out := filepath.Join(dir, "out.migration.yaml")
			state := out + ".state.json"
			if err := writeState(state, loadState{Database: "old_db", Completed: []string{"t"}}); err != nil {
				t.Fatalf("seeding stale state: %v", err)
			}
			if tt.existingConfig {
				if err := os.WriteFile(out, []byte("sentinel"), 0o644); err != nil {
					t.Fatalf("seeding config: %v", err)
				}
			}

			args := []string{"--out", out}
			if tt.force {
				args = append(args, "--force")
			}
			if err := runProfileIO(append(args, src), strings.NewReader(""), &bytes.Buffer{}, false); err != nil {
				t.Fatalf("runProfileIO: %v", err)
			}
			if _, err := os.Stat(state); !os.IsNotExist(err) {
				t.Errorf("stale load state survived a regenerated config: %v", err)
			}
			if _, err := config.Load(out); err != nil {
				t.Errorf("config not regenerated: %v", err)
			}
		})
	}
}

func TestRunRun_RefusesExistingConfigWithoutTerminal(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	makeSQLiteFile(t, src)
	cfgPath := src + ".migration.yaml"
	if err := os.WriteFile(cfgPath, []byte("sentinel"), 0o644); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
	if err := writeState(cfgPath+".state.json", loadState{Database: "old_db"}); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	// runRun reads os.Stdin for its prompt; /dev/null is not a terminal, so the
	// refusal path is taken without any risk of blocking on a developer's tty.
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}
	defer devnull.Close()
	orig := os.Stdin
	os.Stdin = devnull
	defer func() { os.Stdin = orig }()

	err = runRun([]string{"--pg", "postgres://u@localhost:5432/?sslmode=disable", src})
	if err == nil || !strings.Contains(err.Error(), "pass --force") || !strings.Contains(err.Error(), cfgPath) {
		t.Fatalf("expected a refusal naming %s and --force, got: %v", cfgPath, err)
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil || string(got) != "sentinel" {
		t.Errorf("config changed by a refused run: %q, %v", got, err)
	}
	if _, err := os.Stat(cfgPath + ".state.json"); err != nil {
		t.Errorf("a refused run removed the load state: %v", err)
	}
}

func TestIsTerminal_CharacterDevicesAreNotTerminals(t *testing.T) {
	for _, dev := range []string{os.DevNull, "/dev/zero"} {
		t.Run(dev, func(t *testing.T) {
			f, err := os.Open(dev)
			if err != nil {
				t.Skipf("cannot open %s: %v", dev, err)
			}
			defer f.Close()
			if isTerminal(f) {
				t.Errorf("%s reported as a terminal", dev)
			}
		})
	}
}

// A failed config write must keep the state: the state is only discarded once
// the new config is on disk.
func TestRunProfileIO_StateSurvivesFailedSave(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	makeSQLiteFile(t, src)
	// A directory at --out makes the write fail, while --force still proceeds.
	out := filepath.Join(dir, "out.migration.yaml")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := out + ".state.json"
	if err := writeState(state, loadState{Database: "old_db", Completed: []string{"t"}}); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	err := runProfileIO([]string{"--force", "--out", out, src}, strings.NewReader(""), &bytes.Buffer{}, false)
	if err == nil || !strings.Contains(err.Error(), "writing config") || !strings.Contains(err.Error(), out) {
		t.Fatalf("expected the failure to be the config save naming %s, got: %v", out, err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Errorf("failed save removed the load state: %v", err)
	}
}

// A profile that fails before any write must leave the config and state alone.
func TestRunProfileIO_StateSurvivesFailedProfile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "not-sqlite.db")
	if err := os.WriteFile(src, []byte("this is not a sqlite database, just text padding padding padding"), 0o644); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	out := filepath.Join(dir, "out.migration.yaml")
	if err := os.WriteFile(out, []byte("sentinel"), 0o644); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
	state := out + ".state.json"
	if err := writeState(state, loadState{Database: "old_db", Completed: []string{"t"}}); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	err := runProfileIO([]string{"--force", "--out", out, src}, strings.NewReader(""), &bytes.Buffer{}, false)
	if err == nil || strings.Contains(err.Error(), "writing config") {
		t.Fatalf("expected the profile itself to fail before any save, got: %v", err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Errorf("failed profile removed the load state: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != "sentinel" {
		t.Errorf("failed profile changed the config: %q, %v", got, err)
	}
}

// A run whose profile fails must keep the config and the state, like a
// refused run does. Profiling fails before any connection is attempted.
func TestRunRun_FailedProfileKeepsConfigAndState(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "not-sqlite.db")
	if err := os.WriteFile(src, []byte("this is not a sqlite database, just text padding padding padding"), 0o644); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	cfgPath := src + ".migration.yaml"
	if err := os.WriteFile(cfgPath, []byte("sentinel"), 0o644); err != nil {
		t.Fatalf("seeding config: %v", err)
	}
	state := cfgPath + ".state.json"
	if err := writeState(state, loadState{Database: "old_db", Completed: []string{"t"}}); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	err := runRun([]string{"--pg", "postgres://u@localhost:5432/?sslmode=disable", "--force", src})
	if err == nil || strings.Contains(err.Error(), "writing config") {
		t.Fatalf("expected the profile to fail before any save, got: %v", err)
	}
	got, readErr := os.ReadFile(cfgPath)
	if readErr != nil || string(got) != "sentinel" {
		t.Errorf("failed run changed the config: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(state); statErr != nil {
		t.Errorf("failed run removed the load state: %v", statErr)
	}
}
