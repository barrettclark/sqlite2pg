package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/pipeline"
)

func TestRun_VerifyRequiresTwoPaths(t *testing.T) {
	err := run([]string{"verify", "--pg", "postgres://localhost/x", "onlyonearg.db"})
	if err == nil {
		t.Fatal("expected an error when verify is given only one positional argument")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected a usage message, got %q", err.Error())
	}
}

func TestRun_VerifyRequiresThePgFlag(t *testing.T) {
	err := run([]string{"verify", "source.db", "config.migration.yaml"})
	if err == nil {
		t.Fatal("expected an error when --pg is not given to verify")
	}
	if !strings.Contains(err.Error(), "--pg") {
		t.Errorf("expected the error to mention --pg, got %q", err.Error())
	}
}

func TestRun_VerifyFailsClearlyWithoutAPriorLoad(t *testing.T) {
	// verify reads which database to connect to from <config>.state.json
	// — the file `sqlite2pg load` writes (issue #19). Without ever having
	// run load, that file doesn't exist, and the error must say so rather
	// than failing some other, more confusing way (e.g. a raw "file not
	// found").
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"t": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns: map[string]config.ColumnConfig{
					"id": {TargetType: "bigint", Reviewed: true},
				},
			},
		},
	}
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := run([]string{"verify", "--pg", "postgres://localhost/x", "source.db", configPath})
	if err == nil {
		t.Fatal("expected an error when no state file exists for this config")
	}
	if !strings.Contains(err.Error(), "sqlite2pg load") {
		t.Errorf("expected the error to explain that `sqlite2pg load` must run first, got %q", err.Error())
	}
}

func TestWriteVerifyReport_CleanRunReportsPassAndZeroMismatches(t *testing.T) {
	results := []pipeline.TableVerifyResult{
		{Table: "widgets", SourceRowCount: 3, TargetRowCount: 3, RowsCompared: 3, ColumnResults: map[string]*pipeline.ColumnVerifyResult{}},
	}

	var buf bytes.Buffer
	summary := writeVerifyReport(&buf, results, nil)

	if !summary.passed() {
		t.Errorf("expected passed() true for a clean result set")
	}
	out := buf.String()
	if !strings.Contains(out, "result:                PASS") {
		t.Errorf("expected the report to say PASS, got:\n%s", out)
	}
	if !strings.Contains(out, "widgets:") {
		t.Errorf("expected the report to mention table widgets, got:\n%s", out)
	}
}

func TestWriteVerifyReport_RowCountMismatchFailsAndSkipsColumnDetail(t *testing.T) {
	results := []pipeline.TableVerifyResult{
		{Table: "widgets", SourceRowCount: 5, TargetRowCount: 4, RowCountMismatch: true},
	}

	var buf bytes.Buffer
	summary := writeVerifyReport(&buf, results, nil)

	if summary.passed() {
		t.Error("expected passed() false when a row-count mismatch is present")
	}
	if summary.rowCountFailures != 1 {
		t.Errorf("expected 1 row-count failure recorded, got %d", summary.rowCountFailures)
	}
	out := buf.String()
	if !strings.Contains(out, "ROW COUNT MISMATCH") {
		t.Errorf("expected the report to flag the row-count mismatch, got:\n%s", out)
	}
}

func TestWriteVerifyReport_ValueMismatchListsColumnAndCapsExamplesButNotTheCount(t *testing.T) {
	examples := make([]pipeline.ColumnMismatch, 20)
	for i := range examples {
		examples[i] = pipeline.ColumnMismatch{RowIndex: i, Source: "x", Expected: "x", Actual: "y"}
	}
	results := []pipeline.TableVerifyResult{
		{
			Table: "widgets", SourceRowCount: 100, TargetRowCount: 100, RowsCompared: 100,
			ColumnResults: map[string]*pipeline.ColumnVerifyResult{
				"name": {MismatchCount: 45, Examples: examples},
			},
		},
	}

	var buf bytes.Buffer
	summary := writeVerifyReport(&buf, results, nil)

	if summary.passed() {
		t.Error("expected passed() false when a value mismatch is present")
	}
	if summary.totalMismatches != 45 {
		t.Errorf("expected the total mismatch count (45) to survive the report even though examples are capped, got %d", summary.totalMismatches)
	}
	out := buf.String()
	if !strings.Contains(out, "widgets.name: 45 of 100 row(s) differ") {
		t.Errorf("expected the report to state the true mismatch count, got:\n%s", out)
	}
	if !strings.Contains(out, "and 25 more") {
		t.Errorf("expected the report to note the 25 examples beyond the cap, got:\n%s", out)
	}
}

func TestWriteVerifyReport_UnorderedMismatchDoesNotClaimRowPosition(t *testing.T) {
	results := []pipeline.TableVerifyResult{
		{
			Table: "widgets", SourceRowCount: 10, TargetRowCount: 10, RowsCompared: 10, Ordered: false,
			ColumnResults: map[string]*pipeline.ColumnVerifyResult{
				"name": {MismatchCount: 1, Examples: []pipeline.ColumnMismatch{{RowIndex: 3, Expected: "x", Actual: "y"}}},
			},
		},
	}

	var buf bytes.Buffer
	writeVerifyReport(&buf, results, nil)

	out := buf.String()
	if strings.Contains(out, "row 3:") {
		t.Errorf("expected the unordered path to never claim a row position, got:\n%s", out)
	}
	if !strings.Contains(out, "no primary key") {
		t.Errorf("expected the report to explain the table has no primary key, got:\n%s", out)
	}
}

func TestWriteVerifyReport_ListsSkippedTables(t *testing.T) {
	var buf bytes.Buffer
	writeVerifyReport(&buf, nil, []string{"geometry_only"})

	out := buf.String()
	if !strings.Contains(out, "geometry_only: skipped") {
		t.Errorf("expected the report to list the skipped table, got:\n%s", out)
	}
}

func TestFormatVerifyValue_RendersNilAsNULL(t *testing.T) {
	if got := formatVerifyValue(nil); got != "NULL" {
		t.Errorf("expected NULL, got %q", got)
	}
	if got := formatVerifyValue(int64(5)); got != "5" {
		t.Errorf("expected 5, got %q", got)
	}
}
