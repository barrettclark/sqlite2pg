package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// openTypePicker opens a centered list of the types validTypesForColumn offers,
// with the current target type pre-selected when it is listed.
func (m *model) openTypePicker(columnName string) {
	m.pickerColumn = columnName
	tv := findTable(m.summary, m.selectedTable)
	col := columnByName(tv, columnName)
	cells := columnSampleCells(tv, columnName)
	types := validTypesForColumn(cells, col.DeclaredType, col.RejectNull)

	list := tview.NewList()
	list.ShowSecondaryText(true)
	list.SetBorder(true)
	list.SetTitle(fmt.Sprintf(" Edit type: %s ", columnName))
	list.SetInputCapture(m.pickerKeyCapture)
	list.SetSelectedFunc(m.onTypeSelected)
	list.SetChangedFunc(func(_ int, typ, _ string, _ rune) {
		m.showPickerWarning(typ)
	})
	m.pickerStatusBase = m.status.GetText(false)
	sample := firstNonNullCell(cells)
	for i, t := range types {
		secondary := ""
		if sample.value != "" {
			display, _, _ := previewValueForType(sample.value, t, col.DeclaredType, sample.isText)
			// Escaped for the same reason as the grid's header/cell text:
			// tview treats literal "[...]" in rendered text as a tag, and
			// real sample data can contain brackets.
			secondary = tview.Escape(fmt.Sprintf("e.g. %s", display))
		}
		transform, _ := commonTransformForType(cells, t, col.DeclaredType)
		if emptyRowsBecomeNull(cells, transform) {
			if secondary != "" {
				secondary += "; "
			}
			secondary += tview.Escape(emptyNullMarker)
		}
		list.AddItem(t, secondary, typeShortcuts[t], nil)
		if t == col.TargetType {
			list.SetCurrentItem(i)
		}
	}
	m.picker = list
	current, _ := list.GetItemText(list.GetCurrentItem())
	m.showPickerWarning(current)

	// tview reserves 4 extra columns to print each item's "(x)" shortcut
	// prefix once any item has one, so widen the overlay to match.
	overlay := centered(list, 76, len(types)+2)
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

// onTypeSelected persists the derived transform for typeName, or shows a refusal
// and leaves the stored type and transform untouched.
func (m *model) onTypeSelected(index int, typeName, secondaryText string, shortcut rune) {
	tv := findTable(m.summary, m.selectedTable)
	col := columnByName(tv, m.pickerColumn)
	cells := columnSampleCells(tv, m.pickerColumn)
	// Re-confirming the current type keeps its stored transform when that still
	// converts every sample; otherwise the transform is re-derived and checked.
	transform := col.Transform
	if typeName != col.TargetType || !storedTransformFits(cells, col.Transform, col.RejectNull) {
		if msg := refusalMessage(m.pickerColumn, typeName, col.TargetType, cells, col.DeclaredType, col.RejectNull); msg != "" {
			m.status.SetText(m.pickerStatusBase)
			m.closePicker()
			m.showError(msg)
			return
		}
		transform, _ = commonTransformForType(cells, typeName, col.DeclaredType)
	}

	err := m.st.ApplyDecision(m.selectedTable, m.pickerColumn, review.DecisionRequest{
		TargetType: typeName,
		Transform:  transform,
		Rationale:  "human override via TUI",
	})
	if err != nil {
		m.status.SetText(m.pickerStatusBase)
		m.closePicker()
		m.showError(fmt.Sprintf("apply decision failed: %s", err))
		return
	}

	_, selectedColumn := m.grid.GetSelection()
	m.summary = m.st.Summary()
	m.buildGrid(m.selectedTable)
	m.grid.Select(0, selectedColumn)
	m.gridSelectionChanged(0, selectedColumn)
	if emptyRowsBecomeNull(columnSampleCells(tv, m.pickerColumn), transform) {
		m.status.SetText(m.status.GetText(false) + " | " + tview.Escape(emptyNullStatus))
	}
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
		m.status.SetText(m.pickerStatusBase)
		m.closePicker()
		return nil
	}
	return event
}

// refusalMessage returns "" if typeName can be saved, else one of three messages:
// disagreeing transforms, a value that does not fit the type, or (with "" rows)
// an empty string that becomes NULL on a NOT NULL column or does not load at all.
func refusalMessage(column, typeName, targetType string, cells []sampleCell, declaredType string, rejectNull bool) string {
	if _, ok := commonTransformForType(cells, typeName, declaredType); !ok {
		if allSamplesValidate(cells, typeName, declaredType) {
			return fmt.Sprintf("%s: sample rows need different %s transforms (e.g. ISO 8601 and YYYYMMDD dates); a single transform can't cover them — leave the column as %s or split it", column, typeName, targetType)
		}
		return fmt.Sprintf("%s: samples can't load as %s; leave the column as %s", column, typeName, targetType)
	}
	if !typeLoadsSamples(cells, typeName, declaredType, hasEmptyCell(cells), rejectNull) {
		transform, _ := commonTransformForType(cells, typeName, declaredType)
		if rejectNull && emptyRowsBecomeNull(cells, transform) {
			return fmt.Sprintf("%s: samples can't load as %s (an empty string would become NULL on this NOT NULL column); leave the column as %s", column, typeName, targetType)
		}
		return fmt.Sprintf("%s: samples can't load as %s (empty strings don't load as %s); leave the column as %s", column, typeName, typeName, targetType)
	}
	return ""
}

// allSamplesValidate reports whether every non-NULL, non-empty sample validates
// as typeName, ignoring whether they agree on a transform.
func allSamplesValidate(cells []sampleCell, typeName, declaredType string) bool {
	for _, c := range cells {
		if c.isNull() || c.value == "" {
			continue
		}
		if _, _, valid := previewValueForType(c.value, typeName, declaredType, c.isText); !valid {
			return false
		}
	}
	return true
}

// showPickerWarning shows the full empty-row note in the status bar while typ
// is highlighted, and clears it when the highlight moves to a type without one.
func (m *model) showPickerWarning(typ string) {
	tv := findTable(m.summary, m.selectedTable)
	col := columnByName(tv, m.pickerColumn)
	cells := columnSampleCells(tv, m.pickerColumn)
	transform, _ := commonTransformForType(cells, typ, col.DeclaredType)
	text := m.pickerStatusBase
	if emptyRowsBecomeNull(cells, transform) {
		text += " | " + tview.Escape(emptyNullStatus)
	}
	m.status.SetText(text)
}
