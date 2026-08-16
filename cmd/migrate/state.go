package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// loadCompletedTables reads the per-run state file `migrate load --resume`
// consults to skip tables already loaded after a prior failure. A missing
// file just means no table has completed yet.
func loadCompletedTables(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state %s: %w", path, err)
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, fmt.Errorf("parsing state %s: %w", path, err)
	}
	completed := make(map[string]bool, len(names))
	for _, n := range names {
		completed[n] = true
	}
	return completed, nil
}

// markTableCompleted appends table to the state file, so a later --resume
// run skips it. Unlike pgloader's all-or-nothing LOAD DATABASE, each
// table's COPY is its own unit of resumable work.
func markTableCompleted(path, table string) error {
	completed, err := loadCompletedTables(path)
	if err != nil {
		return err
	}
	completed[table] = true

	names := make([]string, 0, len(completed))
	for n := range completed {
		names = append(names, n)
	}
	data, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
