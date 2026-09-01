package main

import (
	"path/filepath"
	"testing"
)

func TestLoadState_EmptyWhenFileDoesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.state.json")
	completed, err := loadCompletedTables(path)
	if err != nil {
		t.Fatalf("loadCompletedTables: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("expected no completed tables, got %v", completed)
	}
}

func TestMarkTableCompleted_PersistsAndAccumulates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.state.json")

	if err := writeState(path, loadState{Database: "chinook_20260830_120000"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := markTableCompleted(path, "bikes"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}
	if err := markTableCompleted(path, "construction"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}

	completed, err := loadCompletedTables(path)
	if err != nil {
		t.Fatalf("loadCompletedTables: %v", err)
	}
	if !completed["bikes"] || !completed["construction"] {
		t.Errorf("expected both tables marked completed, got %v", completed)
	}
}

// TestMarkTableCompleted_PreservesRecordedDatabase is the regression test
// for issue #19: once a run has recorded which database it provisioned,
// every subsequent write to the state file (as each table finishes) must
// keep that database name intact rather than losing it. This is what lets
// --resume reconnect to the same database instead of provisioning a new,
// empty one.
func TestMarkTableCompleted_PreservesRecordedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.state.json")

	if err := writeState(path, loadState{Database: "chinook_20260830_120000"}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := markTableCompleted(path, "albums"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}
	if err := markTableCompleted(path, "artists"); err != nil {
		t.Fatalf("markTableCompleted: %v", err)
	}

	st, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Database != "chinook_20260830_120000" {
		t.Errorf("expected recorded database to survive markTableCompleted, got %q", st.Database)
	}
	if len(st.Completed) != 2 {
		t.Errorf("expected 2 completed tables, got %v", st.Completed)
	}
}

// TestReadState_EmptyWhenFileDoesNotExist mirrors
// TestLoadState_EmptyWhenFileDoesNotExist but through the new readState
// entry point: a fresh --resume invocation with no prior state must not
// treat a missing file as "reconnect to database \"\"".
func TestReadState_EmptyWhenFileDoesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.state.json")
	st, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.Database != "" || len(st.Completed) != 0 {
		t.Errorf("expected zero-value state, got %+v", st)
	}
}

// TestMarkForeignKeysApplied_PersistsAndPreservesDatabaseAndCompleted
// mirrors TestMarkTableCompleted_PreservesRecordedDatabase: recording that
// the FK step finished must not clobber the database name or the
// completed-tables list already on file.
func TestMarkForeignKeysApplied_PersistsAndPreservesDatabaseAndCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.state.json")

	if err := writeState(path, loadState{Database: "chinook_20260830_120000", Completed: []string{"albums"}}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := markForeignKeysApplied(path); err != nil {
		t.Fatalf("markForeignKeysApplied: %v", err)
	}

	st, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !st.FKsApplied {
		t.Error("expected FKsApplied to be true")
	}
	if st.Database != "chinook_20260830_120000" {
		t.Errorf("expected recorded database to survive markForeignKeysApplied, got %q", st.Database)
	}
	if len(st.Completed) != 1 || st.Completed[0] != "albums" {
		t.Errorf("expected completed-tables list to survive markForeignKeysApplied, got %v", st.Completed)
	}
}
