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
			display, _, _ := previewValueForType(sample, t)
			// Escaped for the same reason as the grid's header/cell text:
			// tview treats literal "[...]" in rendered text as a tag, and
			// real sample data can contain brackets.
			secondary = tview.Escape(fmt.Sprintf("e.g. %s", display))
		}
		list.AddItem(t, secondary, typeShortcuts[t], nil)
		if t == col.TargetType {
			list.SetCurrentItem(i)
		}
	}
	m.picker = list

	// tview reserves 4 extra columns to print each item's "(x)" shortcut
	// prefix once any item has one, so widen the overlay to match.
	overlay := centered(list, 44, len(types)+2)
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
// refreshes the grid and status line, and closes the picker.
//
// Transform is preserved unchanged when typeName matches the column's
// current TargetType: re-confirming the picker's own current selection
// (issue #18) must not strip a transform the column still needs at COPY
// time (e.g. timestamptz via unix_epoch_seconds).
//
// For a genuine type change, the stale transform from the prior heuristic
// guess is never implicitly carried over — but the picker itself only
// offers a type in the first place when either it needs no transform at
// all, or some transform actually converts the column's sample data into
// it (dateTransformPreview for date/timestamptz, uuid_format/
// uuid_list_format for uuid/uuid[] — issues #27, #12). Re-deriving that
// same transform here (issue #41) means selecting one of those offered
// types attaches the transform that made it valid, instead of discarding
// it and leaving a raw value the real COPY can't write into the new
// column type.
//
// The transform is derived across EVERY non-NULL sample, not just the
// first (issue #64): a date/timestamptz column whose rows legitimately
// need different transforms (some ISO 8601, some YYYYMMDD) can't be
// expressed by a single ColumnConfig.Transform, so the pick is refused
// rather than persisting one row's transform and breaking the COPY on all
// the others.
func (m *model) onTypeSelected(index int, typeName, secondaryText string, shortcut rune) {
	tv := findTable(m.summary, m.selectedTable)
	col := columnByName(tv, m.pickerColumn)
	transform := ""
	if typeName == col.TargetType {
		transform = col.Transform
	} else {
		t, ok := commonTransformForType(columnSampleValues(tv, m.pickerColumn), typeName)
		if !ok {
			m.closePicker()
			m.showError(fmt.Sprintf("%s: sample rows need different %s transforms (e.g. ISO 8601 and YYYYMMDD dates); a single transform can't cover them — leave the column as %s or split it",
				m.pickerColumn, typeName, col.TargetType))
			return
		}
		transform = t
	}

	err := m.st.ApplyDecision(m.selectedTable, m.pickerColumn, review.DecisionRequest{
		TargetType: typeName,
		Transform:  transform,
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
	// Keeps the table list's needs-review/auto-approved counts and title
	// in sync with the decision just applied (issue #93's audit, finding
	// L7) — without this, they showed whatever they were when the TUI
	// started, for the rest of the session.
	m.buildTableList()
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
