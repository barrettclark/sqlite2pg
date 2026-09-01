package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// buildTableList (re)builds m.tableList from m.summary: one row per table,
// showing its needs-review/auto-approved counts. Called again after a
// decision changes m.summary (issue #93's audit, finding L7) — onTypeSelected
// used to only rebuild the grid, leaving this list's per-table
// needs-review/auto-approved counts and title stale for the rest of the
// session. Preserves the current selection across a rebuild (rather than
// resetting to the top item) since m.summary.Tables' order is stable
// between calls.
func (m *model) buildTableList() {
	list := m.tableList
	if list == nil {
		list = tview.NewList()
		list.ShowSecondaryText(true)
		list.SetBorder(true)
		list.SetInputCapture(m.tableListKeyCapture)
		list.SetSelectedFunc(m.onTableSelected)
	} else {
		current := list.GetCurrentItem()
		list.Clear()
		defer func() {
			if current < list.GetItemCount() {
				list.SetCurrentItem(current)
			}
		}()
	}
	for _, t := range m.summary.Tables {
		needs, auto := 0, 0
		for _, c := range t.Columns {
			if c.NeedsReview {
				needs++
			} else {
				auto++
			}
		}
		description := fmt.Sprintf("%d column(s) — %d need review, %d auto-approved", len(t.Columns), needs, auto)
		list.AddItem(t.Name, description, 0, nil)
	}
	title := fmt.Sprintf(" %d table(s) — %d column(s) need review, %d auto-approved ",
		len(m.summary.Tables), m.summary.NeedsReviewCount, m.summary.AutoApprovedCount)
	list.SetTitle(title)
	m.tableList = list
}

// tableListKeyCapture handles keys the table list itself doesn't know
// about: f raises the Finish confirmation, c/q raise the Cancel
// confirmation, n/N jump straight to the next/previous column flagged for
// review without having to pick a table first.
func (m *model) tableListKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	switch {
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

// onTableSelected builds tableName's grid (if not already built) and
// switches to it.
func (m *model) onTableSelected(index int, tableName, secondaryText string, shortcut rune) {
	m.selectedTable = tableName
	m.buildGrid(tableName)

	flex := tview.NewFlex()
	flex.SetDirection(tview.FlexRow)
	flex.AddItem(m.grid, 0, 1, true)
	flex.AddItem(m.status, 1, 0, false)
	gridFooter := tview.NewTextView()
	gridFooter.SetText("esc: back  enter: edit type  n/N: next/prev flagged  f: finish  c: cancel  q: cancel")
	flex.AddItem(gridFooter, 1, 0, false)

	if m.pages.HasPage("grid") {
		m.pages.RemovePage("grid")
	}
	m.pages.AddPage("grid", flex, true, true)
	m.pages.SwitchToPage("grid")
	m.app.SetFocus(m.grid)
}
