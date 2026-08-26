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
