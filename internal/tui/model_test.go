package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNew_StartsOnTableListWithOneTable(t *testing.T) {
	m := New(newTestState(t), 80, 24)

	if m.screen != screenTableList {
		t.Fatalf("expected screenTableList, got %v", m.screen)
	}
	if len(m.tableList.Items()) != 1 {
		t.Fatalf("expected 1 table item, got %d", len(m.tableList.Items()))
	}
}

func TestModel_EnterOnTableListDrillsIntoColumnDetail(t *testing.T) {
	m := New(newTestState(t), 80, 24)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	if m.screen != screenColumnDetail {
		t.Fatalf("expected screenColumnDetail, got %v", m.screen)
	}
	if m.selectedTable != "bikes" {
		t.Fatalf("expected selectedTable %q, got %q", "bikes", m.selectedTable)
	}
	if len(m.columnList.Items()) != 2 {
		t.Fatalf("expected 2 column items, got %d", len(m.columnList.Items()))
	}
}

func TestModel_EscOnColumnDetailReturnsToTableList(t *testing.T) {
	m := New(newTestState(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.screen != screenTableList {
		t.Fatalf("expected screenTableList, got %v", m.screen)
	}
}

func TestModel_EnterOnColumnOpensTypePicker(t *testing.T) {
	m := New(newTestState(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker for bike_id (first column)
	m = updated.(Model)

	if m.screen != screenTypePicker {
		t.Fatalf("expected screenTypePicker, got %v", m.screen)
	}
	if m.selectedColumn != "bike_id" {
		t.Fatalf("expected selectedColumn %q, got %q", "bike_id", m.selectedColumn)
	}
}

func TestModel_PickerEscCancelsWithoutChangingAnything(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.screen != screenColumnDetail {
		t.Fatalf("expected screenColumnDetail, got %v", m.screen)
	}
	if st.Summary().Tables[0].Columns[0].Source != "heuristic:default_passthrough" {
		t.Fatalf("expected source unchanged by esc, got %q", st.Summary().Tables[0].Columns[0].Source)
	}
}

func TestModel_PickerEnterCommitsDecisionAndReturnsToColumnDetail(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker for bike_id (currently "integer", TypeOptions[1])
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // move to TypeOptions[2] ("bigint")
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	m = updated.(Model)

	if m.screen != screenColumnDetail {
		t.Fatalf("expected screenColumnDetail after commit, got %v", m.screen)
	}
	col := st.Summary().Tables[0].Columns[0]
	if col.Column != "bike_id" {
		t.Fatalf("expected bike_id in position 0, got %q", col.Column)
	}
	if col.TargetType != "bigint" {
		t.Fatalf("expected TargetType %q, got %q", "bigint", col.TargetType)
	}
	if col.Source != "human_override" {
		t.Fatalf("expected source %q, got %q", "human_override", col.Source)
	}
	if col.Transform != "" {
		t.Fatalf("expected Transform cleared on override, got %q", col.Transform)
	}
}
