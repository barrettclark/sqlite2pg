package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// buildGrid (re)builds m.grid from tableName's current TableView: a header
// row of "column [type]" cells (with a needs-review "!" or reviewed "✓"
// marker), and a data row per sample row. Column-only selection is
// enabled so moving left/right selects an entire column, matching the
// picker's per-column editing model.
func (m *model) buildGrid(tableName string) {
	tv := findTable(m.summary, tableName)
	m.selectedTable = tableName

	grid := m.grid
	if grid == nil {
		grid = tview.NewTable()
		grid.SetSelectable(false, true)
		grid.SetFixed(1, 0)
		grid.SetBorder(true)
		grid.SetSelectionChangedFunc(m.gridSelectionChanged)
		grid.SetSelectedFunc(m.gridColumnSelected)
		grid.SetInputCapture(m.gridKeyCapture)
	} else {
		grid.Clear()
	}

	for col, c := range tv.Columns {
		marker := ""
		if c.Reviewed {
			marker = " ✓"
		} else if c.NeedsReview {
			marker = " !"
		}
		// tview interprets literal "[...]" in any rendered text as a
		// color/region tag, so "column [type]" must be escaped or the
		// bracketed type annotation silently vanishes from the header.
		text := tview.Escape(fmt.Sprintf("%s [%s]%s", c.Column, c.TargetType, marker))
		cell := tview.NewTableCell(text)
		cell.SetSelectable(true)
		grid.SetCell(0, col, cell)
	}
	for row, dataRow := range tv.Rows {
		for col, value := range dataRow {
			// Real sample data can itself contain literal brackets (e.g.
			// a text column holding "[note]") — escape for the same
			// reason as the header, so data is never silently mangled.
			cell := tview.NewTableCell(tview.Escape(value))
			cell.SetSelectable(false)
			grid.SetCell(row+1, col, cell)
		}
	}
	grid.SetTitle(fmt.Sprintf(" %s — %d rows ", tv.Name, tv.RowCount))
	m.grid = grid
	grid.Select(0, 0)
	m.gridSelectionChanged(0, 0)
}

// columnAt returns the ColumnView for the grid's column-th column in
// m.selectedTable's current summary.
func (m *model) columnAt(column int) review.ColumnView {
	tv := findTable(m.summary, m.selectedTable)
	if column < 0 || column >= len(tv.Columns) {
		return review.ColumnView{}
	}
	return tv.Columns[column]
}

// gridSelectionChanged updates the status line with the newly selected
// column's confidence/source/rationale — this is the only place that
// information is shown now, replacing the old standalone column-detail
// screen.
func (m *model) gridSelectionChanged(row, column int) {
	col := m.columnAt(column)
	flag := ""
	switch {
	case col.Reviewed && col.NeedsReview:
		flag = "✓ reviewed — "
	case col.NeedsReview:
		flag = "⚠ needs review — "
	}
	m.status.SetText(tview.Escape(fmt.Sprintf("%s%s: confidence %.2f, source %s — %s",
		flag, col.Column, col.Confidence, col.Source, col.Rationale)))
}

// gridColumnSelected is called when Enter is pressed on the grid: it opens
// the type picker for the currently selected column.
func (m *model) gridColumnSelected(row, column int) {
	m.openTypePicker(m.columnAt(column).Column)
}

// gridKeyCapture handles keys the grid itself doesn't know about: esc
// returns to the table list; f raises the Finish confirmation; c/q raise
// the Cancel confirmation; n/N jump to the next/previous column flagged
// for review, across every table.
func (m *model) gridKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	switch {
	case event.Key() == tcell.KeyEscape:
		m.pages.SwitchToPage("tablelist")
		m.app.SetFocus(m.tableList)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'f':
		m.showConfirm(true)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'c':
		m.showConfirm(false)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'q':
		m.showConfirm(false)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'n':
		m.jumpToFlagged(true)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'N':
		m.jumpToFlagged(false)
		return nil
	}
	return event
}
