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
)

// footerLines is how much vertical space the key-hint footer takes, so the
// embedded list.Model is sized to leave room for it.
const footerLines = 2

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

	width, height int

	err error
}

// New builds the initial Model for st, sized to width x height (the
// terminal's current dimensions; Update resizes it on tea.WindowSizeMsg).
func New(st *review.State, width, height int) Model {
	m := Model{st: st, summary: st.Summary(), width: width, height: height}
	m.tableList = newTableList(m.summary, width, height-footerLines)
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tableList.SetSize(msg.Width, msg.Height-footerLines)
		m.columnList.SetSize(msg.Width, msg.Height-footerLines)
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
	}
	return m, nil
}

func (m Model) handleTableListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if item, ok := m.tableList.SelectedItem().(tableItem); ok {
			m.selectedTable = item.name
			tv := findTable(m.summary, item.name)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
			m.screen = screenColumnDetail
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.tableList, cmd = m.tableList.Update(msg)
	return m, cmd
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
	}
	var cmd tea.Cmd
	m.columnList, cmd = m.columnList.Update(msg)
	return m, cmd
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
			}); err != nil {
				m.err = err
				return m, nil
			}
			m.summary = m.st.Summary()
			tv := findTable(m.summary, m.selectedTable)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
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
		body = m.columnList.View() + "\nesc: back to tables  enter: change type\n"
	case screenTypePicker:
		body = m.typeList.View() + "\nenter: select  esc: cancel\n"
	default:
		body = m.tableList.View() + "\nenter: open table\n"
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
	return l
}

type columnItem struct {
	col review.ColumnView
}

func (i columnItem) Title() string {
	marker := "  "
	if i.col.NeedsReview {
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
	l.Select(selected)
	return l
}
