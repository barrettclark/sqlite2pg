package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// previewColWidth is the fixed display width of each column in the
// preview grid; longer values are truncated with an ellipsis.
const previewColWidth = 16

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

