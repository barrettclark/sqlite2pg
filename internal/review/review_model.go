// Package review holds the review-session core shared by every review
// UI (formerly a browser-based wizard, now a terminal UI): the state
// machine that tracks a human's approve/override decisions on the
// profiler's column-type guesses before a load proceeds.
package review

import (
	"sort"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
)

// TypeOptions is the ordered list of target Postgres types a human can pick
// from when overriding a column's decision. Kept as a plain, small, curated
// list rather than every Postgres type — these are the ones the profiler's
// heuristics and DDL generator actually understand.
var TypeOptions = []string{
	"text", "integer", "bigint", "smallint", "boolean",
	"double precision", "real", "numeric",
	"date", "timestamptz", "jsonb", "bytea", "uuid", "uuid[]",
}

// ColumnView is one column's decision, ready for the review UI.
type ColumnView struct {
	Table        string
	Column       string
	DeclaredType string
	TargetType   string
	Transform    string
	Confidence   float64
	Source       string
	Rationale    string
	Reviewed     bool
	NeedsReview  bool
}

// TablePreview is a table's real-data preview: a handful of complete rows
// (aligned to TableView.Columns order) and the source table's total row
// count, so the preview screen can show "this many rows, this many
// sampled" the way a spreadsheet import preview does.
type TablePreview struct {
	Rows     [][]string
	RowCount int
}

// GridData is table -> its preview, used to show real rows in the preview
// screen alongside each column's decision.
type GridData map[string]TablePreview

// TableView is one table's columns (declared order, dropped columns
// excluded) plus its data preview.
type TableView struct {
	Name     string
	Columns  []ColumnView
	Rows     [][]string
	RowCount int
}

// ReviewSummary is the full draft config split into what needs a human's
// attention and what the profiler was confident enough to auto-approve.
type ReviewSummary struct {
	Tables            []TableView
	NeedsReviewCount  int
	AutoApprovedCount int
}

// BuildReviewSummary classifies every column against threshold: below it,
// NeedsReview is true and the column is surfaced prominently in the review
// UI; at or above it, the column is still shown but collapsed by default.
// Columns dropped from the load (target type __drop__, e.g. Esri SHAPE
// blobs) are excluded entirely — they're never part of what gets loaded, so
// there's nothing to review. grid may be nil (no preview data attached,
// e.g. if the source file couldn't be re-read for display purposes).
func BuildReviewSummary(cfg *config.MigrationConfig, threshold float64, grid GridData) ReviewSummary {
	var summary ReviewSummary

	tableNames := make([]string, 0, len(cfg.Tables))
	for name := range cfg.Tables {
		tableNames = append(tableNames, name)
	}
	sort.Strings(tableNames)

	for _, tableName := range tableNames {
		tc := cfg.Tables[tableName]
		tv := TableView{Name: tableName}
		for _, colName := range ddl.IncludedColumns(tc) {
			col, ok := tc.Columns[colName]
			if !ok {
				continue
			}
			// col.NeedsReview persists resolver.Decide's disagreement-tie
			// verdict (issue #20): a contested decision can leave
			// Confidence at the winning finding's original value, above
			// threshold, so Confidence alone isn't a reliable signal here.
			needsReview := col.Confidence < threshold || col.NeedsReview
			if needsReview {
				summary.NeedsReviewCount++
			} else {
				summary.AutoApprovedCount++
			}
			tv.Columns = append(tv.Columns, ColumnView{
				Table:        tableName,
				Column:       colName,
				DeclaredType: col.DeclaredType,
				TargetType:   col.TargetType,
				Transform:    col.Transform,
				Confidence:   col.Confidence,
				Source:       col.Source,
				Rationale:    col.Rationale,
				Reviewed:     col.Reviewed,
				NeedsReview:  needsReview,
			})
		}
		if preview, ok := grid[tableName]; ok {
			tv.Rows = preview.Rows
			tv.RowCount = preview.RowCount
		}
		summary.Tables = append(summary.Tables, tv)
	}

	return summary
}
