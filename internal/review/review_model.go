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
	"date", "timestamptz", "jsonb", "bytea",
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

// TableView is one table's columns (declared order, dropped columns
// excluded).
type TableView struct {
	Name    string
	Columns []ColumnView
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
// there's nothing to review.
func BuildReviewSummary(cfg *config.MigrationConfig, threshold float64) ReviewSummary {
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
			needsReview := col.Confidence < threshold
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
		summary.Tables = append(summary.Tables, tv)
	}

	return summary
}
