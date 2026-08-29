package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// openTypePicker opens a centered list of the types columnName's sample
// values actually validate as (per validTypesForColumn), with the
// column's current target type pre-selected.
func (m *model) openTypePicker(columnName string) {
	m.pickerColumn = columnName
	tv := findTable(m.summary, m.selectedTable)
	col := columnByName(tv, columnName)
	values := columnSampleValues(tv, columnName)
	types := validTypesForColumn(values, col.TargetType)

	list := tview.NewList()
	list.ShowSecondaryText(true)
	list.SetBorder(true)
	list.SetTitle(fmt.Sprintf(" Edit type: %s ", columnName))
	list.SetInputCapture(m.pickerKeyCapture)
	list.SetSelectedFunc(m.onTypeSelected)
	sample := firstNonNullValue(values)
	for i, t := range types {
		secondary := ""
		if sample != "" {
			display, _ := previewValueForType(sample, t)
			// Escaped for the same reason as the grid's header/cell text:
			// tview treats literal "[...]" in rendered text as a tag, and
			// real sample data can contain brackets.
			secondary = tview.Escape(fmt.Sprintf("e.g. %s", display))
		}
		list.AddItem(t, secondary, 0, nil)
		if t == col.TargetType {
			list.SetCurrentItem(i)
		}
	}
	m.picker = list

	overlay := centered(list, 40, len(types)+2)
	if m.pages.HasPage("picker") {
		m.pages.RemovePage("picker")
	}
	m.pages.AddPage("picker", overlay, true, true)
	m.app.SetFocus(list)
}

// columnByName returns tv's ColumnView matching columnName, or a
// zero-value ColumnView if not found. Unlike columnAt (in grid.go, which
// looks up by grid column index against m.selectedTable), this looks up
// by column name against an explicit TableView — the picker knows which
// column it's editing by name, not by the grid's current index.
func columnByName(tv review.TableView, columnName string) review.ColumnView {
	for _, c := range tv.Columns {
		if c.Column == columnName {
			return c
		}
	}
	return review.ColumnView{}
}

// centered wraps p in a Flex that centers it at width x height within the
// screen — the standard tview pattern for a modal-style overlay, since
// tview has no built-in "centered box" primitive.
func centered(p tview.Primitive, width, height int) tview.Primitive {
	row := tview.NewFlex()
	row.SetDirection(tview.FlexRow)
	row.AddItem(nil, 0, 1, false)
	row.AddItem(p, height, 1, true)
	row.AddItem(nil, 0, 1, false)

	col := tview.NewFlex()
	col.AddItem(nil, 0, 1, false)
	col.AddItem(row, width, 1, true)
	col.AddItem(nil, 0, 1, false)
	return col
}

// onTypeSelected applies typeName as m.pickerColumn's new target type,
// refreshes the grid and status line, and closes the picker. Transform is
// always cleared and Rationale always set to the fixed string below — a
// stale transform from the prior heuristic guess is never implicitly
// carried over to a new target type.
func (m *model) onTypeSelected(index int, typeName, secondaryText string, shortcut rune) {
	err := m.st.ApplyDecision(m.selectedTable, m.pickerColumn, review.DecisionRequest{
		TargetType: typeName,
		Transform:  "",
		Rationale:  "human override via TUI",
	})
	if err != nil {
		m.closePicker()
		m.showError(fmt.Sprintf("apply decision failed: %s", err))
		return
	}

	_, selectedColumn := m.grid.GetSelection()
	m.summary = m.st.Summary()
	m.buildGrid(m.selectedTable)
	m.grid.Select(0, selectedColumn)
	m.gridSelectionChanged(0, selectedColumn)
	m.closePicker()
}

// closePicker removes the picker page and returns focus to the grid.
func (m *model) closePicker() {
	m.pages.RemovePage("picker")
	m.app.SetFocus(m.grid)
}

// pickerKeyCapture handles keys the picker list itself doesn't know
// about: esc closes it without applying anything.
func (m *model) pickerKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEscape {
		m.closePicker()
		return nil
	}
	return event
}
