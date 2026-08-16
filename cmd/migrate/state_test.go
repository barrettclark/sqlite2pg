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
