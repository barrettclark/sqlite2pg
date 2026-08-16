// Package wizard is the local, localhost-only web UI a human uses to
// approve or override the profiler's column-type decisions before a load
// proceeds.
package wizard

import (
	"sort"

	"sqlite2pg/internal/config"
)

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

// TableView is one table's columns, in declared order.
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
// NeedsReview is true and the column is surfaced prominently in the wizard;
// at or above it, the column is still shown but collapsed by default.
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
		for _, colName := range tc.ColumnOrder {
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
