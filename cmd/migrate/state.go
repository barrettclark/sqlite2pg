package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// loadState is the schema of the per-run state file `migrate load --resume`
// consults: which database the run provisioned (so a later --resume
// reconnects to the very same database instead of provisioning a new,
// empty one — see issue #19) and which tables have already been loaded
// into it.
type loadState struct {
	Database  string   `json:"database"`
	Completed []string `json:"completed"`

	// FKsApplied records whether executeLoad's foreign-key-constraints-
	// and-indexes step has already succeeded for this run. Every table's
	// COPY is its own resumable unit of work (see Completed above), and
	// adding foreign keys is no different: without this flag, a --resume
	// invoked after every table already finished loading but before (or
	// after) the FK step had already run would either skip FKs entirely
	// or try to re-add constraints Postgres already has, failing with
	// "constraint already exists". Recording completion here — the same
	// way Completed does per table — makes that step idempotent across
	// separate `migrate load --resume` invocations too.
	//
	// This is a single flag for the whole step, not one entry per
	// constraint, because executeLoad runs every constraint and index in
	// one transaction: a failure anywhere in the step rolls all of it
	// back, so a --resume that finds FKsApplied still false can retry the
	// whole step wholesale (issue #109 / M6). The flag is written just
	// after that transaction commits; a crash in the gap between commit
	// and this write leaves the constraints in place with FKsApplied
	// false, and the retry would then hit "constraint ... already exists"
	// — the same narrow crash window markTableCompleted has, tracked
	// separately (see issue #128).
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

// writeState overwrites the state file with st in full.
func writeState(path string, st loadState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// loadCompletedTables reads the state file and returns the set of tables
// already loaded, for `migrate load --resume` to skip.
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
	st, err := readState(path)
	if err != nil {
		return err
	}
	st.FKsApplied = true
	return writeState(path, st)
}
