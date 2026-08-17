package wizard

import (
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
)

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("resolving fixture path: %v", err)
	}
	return abs
}

func TestSampleGridData_ReturnsSynchronizedRowsAndTotalRowCount(t *testing.T) {
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: fixturePath(t, "bikes.db"), Kind: "sqlite"},
		Tables: map[string]config.TableConfig{
			"bikes": {
				Include:     true,
				ColumnOrder: []string{"station_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"station_id":   {DeclaredType: "TEXT"},
					"is_installed": {DeclaredType: "INTEGER"},
				},
			},
		},
	}

	grid := sampleGridData(cfg, 5)

	preview, ok := grid["bikes"]
	if !ok {
		t.Fatal("expected a preview for bikes")
	}
	if len(preview.Rows) == 0 {
		t.Fatal("expected at least one preview row")
	}
	if len(preview.Rows) > 5 {
		t.Errorf("expected at most 5 preview rows, got %d", len(preview.Rows))
	}
	for _, row := range preview.Rows {
		if len(row) != 2 {
			t.Fatalf("expected 2 cells per row (station_id, is_installed), got %d: %v", len(row), row)
		}
	}
	// bikes.db has 2,509 rows — the real total, not just the preview size.
	if preview.RowCount != 2509 {
		t.Errorf("expected RowCount 2509, got %d", preview.RowCount)
	}
}

func TestSampleGridData_ExcludesDroppedColumnsFromRows(t *testing.T) {
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: fixturePath(t, "bikes.db"), Kind: "sqlite"},
		Tables: map[string]config.TableConfig{
			"bikes": {
				Include:     true,
				ColumnOrder: []string{"station_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"station_id":   {DeclaredType: "TEXT"},
					"is_installed": {DeclaredType: "INTEGER", TargetType: "__drop__"},
				},
			},
		},
	}

	grid := sampleGridData(cfg, 5)
	preview := grid["bikes"]
	for _, row := range preview.Rows {
		if len(row) != 1 {
			t.Fatalf("expected 1 cell per row (is_installed dropped), got %d: %v", len(row), row)
		}
	}
}

func TestSampleGridData_ReturnsEmptySetWhenSourceIsUnreachable(t *testing.T) {
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: "/nonexistent/path/does-not-exist.db", Kind: "sqlite"},
		Tables: map[string]config.TableConfig{
			"t": {ColumnOrder: []string{"a"}, Columns: map[string]config.ColumnConfig{"a": {}}},
		},
	}

	grid := sampleGridData(cfg, 5)
	if len(grid) != 0 {
		t.Errorf("expected no grid data when source is unreachable, got %v", grid)
	}
}
