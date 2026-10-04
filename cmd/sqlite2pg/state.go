package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// stateMu serializes every state-file write and each read-modify-write cycle
// within this process. It does not coordinate separate sqlite2pg processes.
var stateMu sync.Mutex

// renameFile is a seam for tests. Tests swap it; do not add t.Parallel to them.
var renameFile = os.Rename

// openStateTemp is a seam for tests to fail temp-file creation, write, sync, or close.
var openStateTemp = func(name string) (stateTempFile, error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// afterStateRead runs between readState and writeState in updateState. Tests
// swap it to force overlapping read-modify-write cycles; do not add t.Parallel.
var afterStateRead = func() {}

// stateTempFile is the part of *os.File that writeState uses.
type stateTempFile interface {
	io.Writer
	Sync() error
	Close() error
}

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
// same directory, fsyncs it, and renames it over path, so a crash never
// leaves a truncated file. Holds stateMu. The replace is crash-safe on Unix;
// on Windows it depends on the platform's rename behavior.
//
// The rename is not durable across power loss without a directory fsync, so
// the old state can reappear and a completed table is re-run; that is safe
// for --resume. Two sqlite2pg processes writing the same state file can still
// lose updates, because stateMu is in-process only.
func writeState(path string, st loadState) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	return writeStateLocked(path, st)
}

// writeStateLocked is writeState for callers that already hold stateMu.
func writeStateLocked(path string, st loadState) (err error) {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp, tmpName, err := createStateTemp(path)
	if err != nil {
		return fmt.Errorf("creating temp state file for %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("writing temp state file %s: %w", tmpName, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temp state file %s: %w", tmpName, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("closing temp state file %s: %w", tmpName, err)
	}
	if err = renameFile(tmpName, path); err != nil {
		return fmt.Errorf("replacing state file %s: %w", path, err)
	}
	return nil
}

// createStateTemp creates a new, uniquely named temp file next to path. O_EXCL
// means it never clobbers an existing file; the mode is subject to the umask.
func createStateTemp(path string) (stateTempFile, string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", err
	}
	name := filepath.Join(filepath.Dir(path), ".sqlite2pg-state-"+hex.EncodeToString(b[:]))
	f, err := openStateTemp(name)
	return f, name, err
}

// updateState runs a read-modify-write on the state file under stateMu.
func updateState(path string, mutate func(*loadState)) error {
	stateMu.Lock()
	defer stateMu.Unlock()
	st, err := readState(path)
	if err != nil {
		return err
	}
	afterStateRead()
	mutate(&st)
	return writeStateLocked(path, st)
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
	return updateState(path, func(st *loadState) {
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
	})
}

// markForeignKeysApplied records that executeLoad's foreign-key
// constraints and indexes step has completed, preserving whatever
// database name and completed-tables list are already recorded — the same
// read-modify-write shape as markTableCompleted, for the same reason: a
// later --resume needs both pieces of information intact.
func markForeignKeysApplied(path string) error {
	return updateState(path, func(st *loadState) {
		st.FKsApplied = true
	})
}
