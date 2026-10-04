package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// stateMu serializes read-modify-write cycles on state files within this
// process. It does not coordinate separate sqlite2pg processes.
var stateMu sync.Mutex

// renameFile is a seam so tests can simulate a failure between the temp write and the rename.
var renameFile = os.Rename

// loadState is the schema of the per-run state file `sqlite2pg load --resume`
// consults: which database the run provisioned (so a later --resume
// reconnects to the very same database instead of provisioning a new,
// empty one — see issue #19) and which tables have already been loaded
// into it.
type loadState struct {
	Database  string   `json:"database"`
	Completed []string `json:"completed"`

	// FKsApplied records that executeLoad's foreign-key step has completed
	// at least once. Purely informational — executeLoad re-runs the step
	// on every invocation regardless (the FK set is re-derived from a
	// config that can change between runs — issue #142), which is safe
	// because the generated DDL is idempotent (DROP CONSTRAINT IF EXISTS +
	// ADD, CREATE INDEX IF NOT EXISTS) and runs in one transaction
	// (issues #109, #128).
	FKsApplied bool `json:"fks_applied,omitempty"`
}

// readState reads the given state file. A missing file just means no run
// has started yet, so it returns a zero-value state and no error.
func readState(path string) (loadState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return loadState{}, nil
	}
	if err != nil {
		return loadState{}, fmt.Errorf("reading state %s: %w", path, err)
	}
	var st loadState
	if err := json.Unmarshal(data, &st); err != nil {
		return loadState{}, fmt.Errorf("parsing state %s: %w", path, err)
	}
	return st, nil
}

// writeState replaces the state file with st. It writes a temp file in the
// same directory, fsyncs it, and renames it over path, so a crash leaves
// either the old or the new file intact, never a truncated one.
func writeState(path string, st loadState) (err error) {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp state file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("writing temp state file %s: %w", tmp.Name(), err)
	}
	if err = tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("setting mode on %s: %w", tmp.Name(), err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temp state file %s: %w", tmp.Name(), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temp state file %s: %w", tmp.Name(), err)
	}
	if err = renameFile(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing state file %s: %w", path, err)
	}
	return nil
}

// loadCompletedTables reads the state file and returns the set of tables
// already loaded, for `sqlite2pg load --resume` to skip.
func loadCompletedTables(path string) (map[string]bool, error) {
	st, err := readState(path)
	if err != nil {
		return nil, err
	}
	completed := make(map[string]bool, len(st.Completed))
	for _, n := range st.Completed {
		completed[n] = true
	}
	return completed, nil
}

// markTableCompleted appends table to the state file's completed list,
// preserving whatever database name is already recorded there, so a later
// --resume both skips this table and reconnects to the right database.
// Unlike pgloader's all-or-nothing LOAD DATABASE, each table's COPY is its
// own unit of resumable work.
func markTableCompleted(path, table string) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := readState(path)
	if err != nil {
		return err
	}

	completed := make(map[string]bool, len(st.Completed)+1)
	for _, n := range st.Completed {
		completed[n] = true
	}
	completed[table] = true

	names := make([]string, 0, len(completed))
	for n := range completed {
		names = append(names, n)
	}
	st.Completed = names
	return writeState(path, st)
}

// markForeignKeysApplied records that executeLoad's foreign-key
// constraints and indexes step has completed, preserving whatever
// database name and completed-tables list are already recorded — the same
// read-modify-write shape as markTableCompleted, for the same reason: a
// later --resume needs both pieces of information intact.
func markForeignKeysApplied(path string) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := readState(path)
	if err != nil {
		return err
	}
	st.FKsApplied = true
	return writeState(path, st)
}
