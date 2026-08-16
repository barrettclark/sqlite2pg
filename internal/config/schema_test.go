package config

import (
	"path/filepath"
	"testing"
)

func TestSaveThenLoad_RoundTripsAMigrationConfig(t *testing.T) {
	cfg := &MigrationConfig{
		ConfigVersion: 1,
		Source: SourceInfo{
			Path:         "/tmp/bikes.db",
			SQLiteSHA256: "deadbeef",
			Kind:         "sqlite",
		},
		ToolVersion: "0.1.0",
		Tables: map[string]TableConfig{
			"bikes": {
				Include: true,
				Columns: map[string]ColumnConfig{
					"is_installed": {
						DeclaredType: "INTEGER",
						TargetType:   "boolean",
						Transform:    "int_to_bool",
						Confidence:   0.71,
						Source:       "human_override",
						Rationale:    "human confirmed via wizard: only 0/1/NULL present",
						Reviewed:     true,
						OriginalSuggestion: &Suggestion{
							TargetType: "boolean",
							Confidence: 0.55,
							Source:     "heuristic:boolean01",
						},
					},
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "bikes.migration.yaml")
	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	col := loaded.Tables["bikes"].Columns["is_installed"]
	if col.TargetType != "boolean" {
		t.Errorf("expected target_type boolean, got %q", col.TargetType)
	}
	if col.OriginalSuggestion == nil || col.OriginalSuggestion.Confidence != 0.55 {
		t.Errorf("expected original_suggestion to round-trip, got %+v", col.OriginalSuggestion)
	}
	if loaded.Source.SQLiteSHA256 != "deadbeef" {
		t.Errorf("expected sqlite_sha256 to round-trip, got %q", loaded.Source.SQLiteSHA256)
	}
}
