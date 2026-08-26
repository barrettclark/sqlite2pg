package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"sqlite2pg/internal/review"
)

// dateLayouts are the formats validForType accepts for "date"/"timestamptz"
// — matching what a plain COPY (no transform) would need to parse, not
// every format Postgres itself understands.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// previewValueForType returns what value would look like under targetType:
// for numeric target types, the actual coerced number (truncated for
// integer types, decimal-formatted for floating-point types) rather than a
// bare valid/invalid flag, so a human can see e.g. what "3.7" becomes under
// "integer" or what "3" becomes under "double precision". For non-numeric
// target types it falls back to a validity check — whether the raw text
// would parse as that Postgres type with no transform applied — since
// there's no meaningful "conversion" to preview for e.g. a UUID string
// under "boolean". "NULL" (formatSampleValue's placeholder for a nil
// value) always displays as-is and is always valid, since NULL is valid
// for any nullable column.
func previewValueForType(value, targetType string) (display string, valid bool) {
	if value == "NULL" {
		return value, true
	}
	switch targetType {
	case "integer", "bigint", "smallint":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, false
		}
		return strconv.FormatInt(int64(f), 10), true
	case "real", "double precision", "numeric":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, false
		}
		formatted := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(formatted, ".") {
			formatted += ".0"
		}
		return formatted, true
	case "boolean":
		switch strings.ToLower(value) {
		case "0", "1", "true", "false", "t", "f":
			return value, true
		}
		return value, false
	case "date", "timestamptz":
		for _, layout := range dateLayouts {
			if _, err := time.Parse(layout, value); err == nil {
				return value, true
			}
		}
		return value, false
	default:
		// text, jsonb, bytea: any string is valid, displayed as-is.
		return value, true
	}
}

// previewColWidth is the fixed display width of each column in the
// preview grid; longer values are truncated with an ellipsis.
const previewColWidth = 16

// columnSampleValues extracts one column's sample values (in row order)
// from tv's preview grid, for display alongside the type picker.
func columnSampleValues(tv review.TableView, columnName string) []string {
	idx := -1
	for i, c := range tv.Columns {
		if c.Column == columnName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}
	values := make([]string, 0, len(tv.Rows))
	for _, row := range tv.Rows {
		if idx < len(row) {
			values = append(values, row[idx])
		}
	}
	return values
}

// renderTypePicker renders the type-picker screen: the type list on the
// left, and the selected column's real sample values on the right, so a
// human can see actual data while choosing a target type.
func (m Model) renderTypePicker() string {
	leftWidth := pickerListWidth(m.width)
	rightWidth := m.width - leftWidth

	var right strings.Builder
	right.WriteString(padOrTruncate(m.selectedColumn+" — sample values", rightWidth))
	right.WriteString("\n")
	right.WriteString(strings.Repeat("─", rightWidth))
	right.WriteString("\n")

	highlighted := ""
	if item, ok := m.typeList.SelectedItem().(typeItem); ok {
		highlighted = string(item)
	}

	visibleRows := m.height - footerLines - 2
	if visibleRows < 0 {
		visibleRows = 0
	}
	for i, v := range m.pickerColumnValues {
		if i >= visibleRows {
			break
		}
		display, valid := previewValueForType(v, highlighted)
		line := display
		if !valid {
			line = "⚠ " + display
		}
		right.WriteString(padOrTruncate(line, rightWidth))
		right.WriteString("\n")
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, m.typeList.View(), right.String())
}

func (m Model) handlePreviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = m.previewOrigin
		return m, nil
	case "up", "k":
		if m.previewRowOffset > 0 {
			m.previewRowOffset--
		}
		return m, nil
	case "down", "j":
		if m.previewRowOffset < len(m.previewTable.Rows)-1 {
			m.previewRowOffset++
		}
		return m, nil
	case "left", "h":
		if m.previewColOffset > 0 {
			m.previewColOffset--
		}
		return m, nil
	case "right", "l":
		if m.previewColOffset < len(m.previewTable.Columns)-1 {
			m.previewColOffset++
		}
		return m, nil
	}
	return m, nil
}

// renderPreview draws a scrollable grid of m.previewTable's sample rows:
// a header row of column names, a separator, and as many data rows as fit
// the terminal, starting at previewRowOffset/previewColOffset.
func (m Model) renderPreview() string {
	tv := m.previewTable
	if len(tv.Columns) == 0 {
		return tv.Name + "\n(no columns to preview)"
	}

	visibleRows := m.height - footerLines - 2 // header + separator
	if visibleRows < 1 {
		visibleRows = 1
	}

	var b strings.Builder
	b.WriteString(tv.Name)
	b.WriteString(" — ")
	if tv.RowCount > 0 {
		b.WriteString(strconv.Itoa(tv.RowCount))
		b.WriteString(" rows")
	} else {
		b.WriteString("no preview data")
	}
	b.WriteString("\n\n")

	names := make([]string, len(tv.Columns))
	for i, c := range tv.Columns {
		names[i] = c.Column
	}
	b.WriteString(renderPreviewRow(names, m.previewColOffset, m.width))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n")

	end := m.previewRowOffset + visibleRows
	if end > len(tv.Rows) {
		end = len(tv.Rows)
	}
	for _, row := range tv.Rows[m.previewRowOffset:end] {
		b.WriteString(renderPreviewRow(row, m.previewColOffset, m.width))
		b.WriteString("\n")
	}

	return b.String()
}

// renderPreviewRow renders cells starting at colOffset, truncated to fit
// width, each padded/truncated to previewColWidth and separated by " │ ".
func renderPreviewRow(cells []string, colOffset, width int) string {
	var b strings.Builder
	for i := colOffset; i < len(cells); i++ {
		if b.Len() > 0 {
			b.WriteString(" │ ")
		}
		if b.Len()+previewColWidth > width {
			break
		}
		b.WriteString(padOrTruncate(cells[i], previewColWidth))
	}
	return b.String()
}

func padOrTruncate(s string, width int) string {
	if len(s) > width {
		if width <= 1 {
			return s[:width]
		}
		return s[:width-1] + "…"
	}
	return s + strings.Repeat(" ", width-len(s))
}
