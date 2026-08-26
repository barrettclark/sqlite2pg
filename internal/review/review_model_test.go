package review

import (
	"testing"

	"sqlite2pg/internal/config"
)

func sampleConfig() *config.MigrationConfig {
	return &config.MigrationConfig{
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
}

func TestBuildReviewSummary_SplitsColumnsByConfidenceThreshold(t *testing.T) {
	summary := BuildReviewSummary(sampleConfig(), 0.9, nil)

	if summary.AutoApprovedCount != 1 {
		t.Errorf("expected 1 auto-approved column, got %d", summary.AutoApprovedCount)
	}
	if summary.NeedsReviewCount != 1 {
		t.Errorf("expected 1 needs-review column, got %d", summary.NeedsReviewCount)
	}

	if len(summary.Tables) != 1 || summary.Tables[0].Name != "bikes" {
		t.Fatalf("expected one bikes table, got %+v", summary.Tables)
	}

	var sawInstalled, sawBikeID bool
	for _, c := range summary.Tables[0].Columns {
		switch c.Column {
		case "is_installed":
			sawInstalled = true
			if !c.NeedsReview {
				t.Error("expected is_installed (confidence 0.55) to need review")
			}
		case "bike_id":
			sawBikeID = true
			if c.NeedsReview {
				t.Error("expected bike_id (confidence 0.99) to be auto-approved")
			}
		}
	}
	if !sawInstalled || !sawBikeID {
		t.Fatalf("expected both columns present, got %+v", summary.Tables[0].Columns)
	}
}

func TestBuildReviewSummary_PreservesColumnOrder(t *testing.T) {
	summary := BuildReviewSummary(sampleConfig(), 0.9, nil)
	cols := summary.Tables[0].Columns
	if len(cols) != 2 || cols[0].Column != "bike_id" || cols[1].Column != "is_installed" {
		t.Errorf("expected columns in declared order [bike_id, is_installed], got %+v", cols)
	}
}

func TestBuildReviewSummary_ExcludesDroppedColumnsFromTheGrid(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"schoolsites": {
				ColumnOrder: []string{"OBJECTID", "SHAPE"},
				Columns: map[string]config.ColumnConfig{
					"OBJECTID": {TargetType: "integer", Confidence: 0.99},
					"SHAPE":    {TargetType: "__drop__", Confidence: 0.99},
				},
			},
		},
	}
	summary := BuildReviewSummary(cfg, 0.9, nil)
	cols := summary.Tables[0].Columns
	if len(cols) != 1 || cols[0].Column != "OBJECTID" {
		t.Errorf("expected only OBJECTID (SHAPE is dropped), got %+v", cols)
	}
}

func TestBuildReviewSummary_AttachesGridRowsAndRowCountWhenProvided(t *testing.T) {
	grid := GridData{
		"bikes": TablePreview{
			Rows:     [][]string{{"1", "0"}, {"2", "1"}},
			RowCount: 2509,
		},
	}
	summary := BuildReviewSummary(sampleConfig(), 0.9, grid)

	tv := summary.Tables[0]
	if tv.RowCount != 2509 {
		t.Errorf("expected RowCount 2509, got %d", tv.RowCount)
	}
	if len(tv.Rows) != 2 || tv.Rows[0][0] != "1" {
		t.Errorf("expected 2 preview rows aligned to columns, got %+v", tv.Rows)
	}
}

func TestBuildReviewSummary_NilGridDataProducesNoPanic(t *testing.T) {
	summary := BuildReviewSummary(sampleConfig(), 0.9, nil)
	if len(summary.Tables[0].Rows) != 0 {
		t.Errorf("expected no rows, got %v", summary.Tables[0].Rows)
	}
	if summary.Tables[0].RowCount != 0 {
		t.Errorf("expected RowCount 0, got %d", summary.Tables[0].RowCount)
	}
}
