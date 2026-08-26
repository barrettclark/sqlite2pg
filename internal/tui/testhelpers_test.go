package tui

import (
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/review"
)

// newTestState builds a review.State over a two-column, one-table config —
// one column below the review threshold, one above it — matching the
// fixture internal/review's own tests use, so behavior stays comparable.
func newTestState(t *testing.T) *review.State {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"bike_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"bike_id":      {TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
					"is_installed": {TargetType: "boolean", Transform: "int_to_bool", Confidence: 0.55, Source: "heuristic:boolean01"},
				},
			},
		},
	}
	if err := config.Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := review.NewState(path, 0.9)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return st
}

// newTestStateWithSource builds a review.State whose Source.Path points at
// a real sqlite fixture, so State.NewState's best-effort grid sampling
// actually populates rows — needed to test the preview screen's rendering
// of real data, not just navigation.
func newTestStateWithSource(t *testing.T) *review.State {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "bikes.db"))
	if err != nil {
		t.Fatalf("resolving fixture path: %v", err)
	}
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: abs, Kind: "sqlite"},
		Tables: map[string]config.TableConfig{
			"bikes": {
				Include:     true,
				ColumnOrder: []string{"bike_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"bike_id":      {TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
					"is_installed": {TargetType: "boolean", Transform: "int_to_bool", Confidence: 0.55, Source: "heuristic:boolean01"},
				},
			},
		},
	}
	if err := config.Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := review.NewState(path, 0.9)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return st
}
