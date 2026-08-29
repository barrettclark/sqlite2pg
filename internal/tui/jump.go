package tui

import "sqlite2pg/internal/review"

// jumpToFlagged moves the grid selection to the next (or, if forward is
// false, previous) column that needs review, cycling across every table
// and wrapping around at the ends. This includes columns already
// reviewed — see flaggedColumns' comment — so a decision already made
// stays reachable, not just what's still outstanding. Does nothing but
// update the status line if nothing in the whole draft config needs
// review.
func (m *model) jumpToFlagged(forward bool) {
	flagged := flaggedColumns(m.summary)
	if len(flagged) == 0 {
		m.status.SetText("no columns need review")
		return
	}

	current := flaggedColumn{Table: m.selectedTable, Column: m.columnAt(m.currentGridColumn()).Column}
	target, ok := nextFlaggedColumn(flagged, current, forward)
	if !ok {
		return
	}

	if target.Table != m.selectedTable {
		m.onTableSelected(0, target.Table, "", 0)
	}
	if colIndex := columnIndex(m.summary, target.Table, target.Column); colIndex >= 0 {
		m.grid.Select(0, colIndex)
		m.gridSelectionChanged(0, colIndex)
	}
}

// currentGridColumn returns the grid's currently selected column index, or
// 0 if no grid has been built yet (e.g. jumping from the table list before
// any table has been opened).
func (m *model) currentGridColumn() int {
	if m.grid == nil {
		return 0
	}
	_, col := m.grid.GetSelection()
	return col
}

// columnIndex returns the grid-column index of table.column in summary, or
// -1 if not found.
func columnIndex(summary review.ReviewSummary, table, column string) int {
	tv := findTable(summary, table)
	for i, c := range tv.Columns {
		if c.Column == column {
			return i
		}
	}
	return -1
}
