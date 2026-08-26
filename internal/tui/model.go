// Package tui is the terminal UI a human uses to approve or override the
// profiler's column-type decisions before a load proceeds. It replaces the
// old browser-based wizard: the same review.State/review.ReviewSummary data,
// presented as an in-terminal flow (table list -> column detail -> type
// picker, with an inline Finish/Cancel confirmation) instead of a page in a
// browser.
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"sqlite2pg/internal/review"
)

type screen int

const (
	screenTableList screen = iota
	screenColumnDetail
	screenTypePicker
	screenPreview
)

// footerLines is how much vertical space the key-hint footer takes, so the
// embedded list.Model is sized to leave room for it.
const footerLines = 2

type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmFinish
	confirmCancel
)

// Model is the Bubble Tea model driving the review TUI.
type Model struct {
	st      *review.State
	summary review.ReviewSummary

	screen screen

	tableList  list.Model
	columnList list.Model
	typeList   list.Model

	selectedTable  string
	selectedColumn string

	confirming    bool
	confirmAction confirmAction

	previewOrigin    screen
	previewTable     review.TableView
	previewRowOffset int
	previewColOffset int

	width, height int

	err error
}

// New builds the initial Model for st, sized to width x height (the
// terminal's current dimensions; Update resizes it on tea.WindowSizeMsg).
func New(st *review.State, width, height int) Model {
	m := Model{st: st, summary: st.Summary(), width: width, height: height}
	m.tableList = newTableList(m.summary, width, height-footerLines)
	m.columnList = list.New(nil, list.NewDefaultDelegate(), width, height-footerLines)
	m.columnList.DisableQuitKeybindings()
	m.columnList.SetFilteringEnabled(false)
	m.typeList = list.New(nil, list.NewDefaultDelegate(), width, height-footerLines)
	m.typeList.DisableQuitKeybindings()
	m.typeList.SetFilteringEnabled(false)
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tableList.SetSize(msg.Width, msg.Height-footerLines)
		m.columnList.SetSize(msg.Width, msg.Height-footerLines)
		m.typeList.SetSize(msg.Width, msg.Height-footerLines)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenTableList:
		return m.handleTableListKey(msg)
	case screenColumnDetail:
		return m.handleColumnDetailKey(msg)
	case screenTypePicker:
		return m.handleTypePickerKey(msg)
	case screenPreview:
		return m.handlePreviewKey(msg)
	}
	return m, nil
}

func (m Model) handleTableListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirming {
		return m.handleConfirmKey(msg)
	}
	switch msg.String() {
	case "f":
		m.confirming = true
		m.confirmAction = confirmFinish
		return m, nil
	case "c", "q", "esc":
		m.confirming = true
		m.confirmAction = confirmCancel
		return m, nil
	case "enter":
		if item, ok := m.tableList.SelectedItem().(tableItem); ok {
			m.selectedTable = item.name
			tv := findTable(m.summary, item.name)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
			m.screen = screenColumnDetail
		}
		return m, nil
	case "v":
		if item, ok := m.tableList.SelectedItem().(tableItem); ok {
			m.openPreview(item.name, screenTableList)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.tableList, cmd = m.tableList.Update(msg)
	return m, cmd
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "y" {
		m.confirming = false
		m.confirmAction = confirmNone
		return m, nil
	}
	if m.confirmAction == confirmFinish {
		if err := m.st.Finish(); err != nil {
			m.err = err
			m.confirming = false
			return m, nil
		}
	} else {
		m.st.Cancel()
	}
	return m, tea.Quit
}

func (m Model) handleColumnDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.screen = screenTableList
		return m, nil
	case "enter":
		if item, ok := m.columnList.SelectedItem().(columnItem); ok {
			m.selectedColumn = item.col.Column
			m.typeList = newTypeList(item.col.TargetType, m.width, m.height-footerLines)
			m.screen = screenTypePicker
		}
		return m, nil
	case "v":
		m.openPreview(m.selectedTable, screenColumnDetail)
		return m, nil
	}
	var cmd tea.Cmd
	m.columnList, cmd = m.columnList.Update(msg)
	return m, cmd
}

// openPreview switches to the preview screen for tableName, remembering
// origin so esc returns to whichever screen the preview was opened from.
func (m *Model) openPreview(tableName string, origin screen) {
	m.previewTable = findTable(m.summary, tableName)
	m.previewOrigin = origin
	m.previewRowOffset = 0
	m.previewColOffset = 0
	m.screen = screenPreview
}

func (m Model) handleTypePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenColumnDetail
		return m, nil
	case "enter":
		if item, ok := m.typeList.SelectedItem().(typeItem); ok {
			if err := m.st.ApplyDecision(m.selectedTable, m.selectedColumn, review.DecisionRequest{
				TargetType: string(item),
				Transform:  "",
				Rationale:  "human override via TUI",
			}); err != nil {
				m.err = err
				return m, nil
			}
			m.summary = m.st.Summary()
			m.tableList = newTableList(m.summary, m.width, m.height-footerLines)
			tv := findTable(m.summary, m.selectedTable)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
			idx := 0
			for i, it := range m.columnList.Items() {
				if ci, ok := it.(columnItem); ok && ci.col.Column == m.selectedColumn {
					idx = i
					break
				}
			}
			m.columnList.Select(idx)
		}
		m.screen = screenColumnDetail
		return m, nil
	}
	var cmd tea.Cmd
	m.typeList, cmd = m.typeList.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	var body string
	switch m.screen {
	case screenColumnDetail:
		body = m.columnList.View() + "\nesc: back to tables  enter: change type  v: preview data\n"
	case screenTypePicker:
		body = m.typeList.View() + "\nenter: select  esc: cancel\n"
	case screenPreview:
		body = m.renderPreview() + "\n←/→ scroll columns  ↑/↓ scroll rows  esc: back\n"
	default:
		body = m.tableList.View() + "\nenter: open table  v: preview data  f: finish  c: cancel\n"
		if m.confirming {
			verb := "Confirm & Import"
			if m.confirmAction == confirmCancel {
				verb = "Cancel"
			}
			body += fmt.Sprintf("%s? y/n\n", verb)
		}
	}
	if m.err != nil {
		body += fmt.Sprintf("\nerror: %s\n", m.err)
	}
	return body
}

func findTable(summary review.ReviewSummary, name string) review.TableView {
	for _, t := range summary.Tables {
		if t.Name == name {
			return t
		}
	}
	return review.TableView{}
}

// --- list items --------------------------------------------------------

type tableItem struct {
	name         string
	needsReview  int
	autoApproved int
	total        int
}

func (i tableItem) Title() string { return i.name }
func (i tableItem) Description() string {
	return fmt.Sprintf("%d column(s) — %d need review, %d auto-approved", i.total, i.needsReview, i.autoApproved)
}
func (i tableItem) FilterValue() string { return i.name }

func newTableList(summary review.ReviewSummary, width, height int) list.Model {
	items := make([]list.Item, 0, len(summary.Tables))
	for _, t := range summary.Tables {
		needs, auto := 0, 0
		for _, c := range t.Columns {
			if c.NeedsReview {
				needs++
			} else {
				auto++
			}
		}
		items = append(items, tableItem{name: t.Name, needsReview: needs, autoApproved: auto, total: len(t.Columns)})
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = fmt.Sprintf("%d table(s) — %d column(s) need review, %d auto-approved", len(summary.Tables), summary.NeedsReviewCount, summary.AutoApprovedCount)
	l.DisableQuitKeybindings()
	l.SetFilteringEnabled(false)
	return l
}

type columnItem struct {
	col review.ColumnView
}

func (i columnItem) Title() string {
	marker := "  "
	if i.col.Reviewed {
		marker = "✓ "
	} else if i.col.NeedsReview {
		marker = "! "
	}
	return fmt.Sprintf("%s%s: %s -> %s", marker, i.col.Column, i.col.DeclaredType, i.col.TargetType)
}
func (i columnItem) Description() string {
	return fmt.Sprintf("confidence %.2f, source %s — %s", i.col.Confidence, i.col.Source, i.col.Rationale)
}
func (i columnItem) FilterValue() string { return i.col.Column }

func newColumnList(tv review.TableView, width, height int) list.Model {
	items := make([]list.Item, 0, len(tv.Columns))
	for _, c := range tv.Columns {
		items = append(items, columnItem{col: c})
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = tv.Name
	l.DisableQuitKeybindings()
	l.SetFilteringEnabled(false)
	return l
}

type typeItem string

func (i typeItem) Title() string       { return string(i) }
func (i typeItem) Description() string { return "" }
func (i typeItem) FilterValue() string { return string(i) }

func newTypeList(current string, width, height int) list.Model {
	items := make([]list.Item, len(review.TypeOptions))
	selected := 0
	for idx, t := range review.TypeOptions {
		items[idx] = typeItem(t)
		if t == current {
			selected = idx
		}
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = "select target type"
	l.DisableQuitKeybindings()
	l.SetFilteringEnabled(false)
	l.Select(selected)
	return l
}
