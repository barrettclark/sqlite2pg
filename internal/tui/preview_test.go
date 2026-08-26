package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModel_VOnTableListOpensPreview(t *testing.T) {
	m := New(newTestStateWithSource(t), 80, 24)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)

	if m.screen != screenPreview {
		t.Fatalf("expected screenPreview, got %v", m.screen)
	}
	if m.previewOrigin != screenTableList {
		t.Fatalf("expected previewOrigin screenTableList, got %v", m.previewOrigin)
	}
	if m.previewTable.Name != "bikes" {
		t.Fatalf("expected previewTable bikes, got %q", m.previewTable.Name)
	}
}

func TestModel_VOnColumnDetailOpensPreviewForCurrentTable(t *testing.T) {
	m := New(newTestStateWithSource(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)

	if m.screen != screenPreview {
		t.Fatalf("expected screenPreview, got %v", m.screen)
	}
	if m.previewOrigin != screenColumnDetail {
		t.Fatalf("expected previewOrigin screenColumnDetail, got %v", m.previewOrigin)
	}
	if m.previewTable.Name != "bikes" {
		t.Fatalf("expected previewTable bikes, got %q", m.previewTable.Name)
	}
}

func TestModel_EscOnPreviewReturnsToOrigin(t *testing.T) {
	m := New(newTestStateWithSource(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes (column detail)
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}}) // preview from column detail
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.screen != screenColumnDetail {
		t.Fatalf("expected screenColumnDetail (preview's origin), got %v", m.screen)
	}
}

func TestModel_PreviewRowAndColumnOffsetsClampAtBounds(t *testing.T) {
	m := New(newTestStateWithSource(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)

	// Up/left at the origin must not go negative.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = updated.(Model)
	if m.previewRowOffset != 0 || m.previewColOffset != 0 {
		t.Fatalf("expected offsets clamped to 0, got row=%d col=%d", m.previewRowOffset, m.previewColOffset)
	}

	// Down must advance, but not past the last row.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.previewRowOffset != 1 {
		t.Fatalf("expected previewRowOffset 1, got %d", m.previewRowOffset)
	}

	// Right must advance, but bikes has only 2 columns, so clamp immediately.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = updated.(Model)
	if m.previewColOffset != len(m.previewTable.Columns)-1 {
		t.Fatalf("expected previewColOffset clamped to %d, got %d", len(m.previewTable.Columns)-1, m.previewColOffset)
	}
}

func TestModel_TypePickerShowsSampleValuesForTheSelectedColumn(t *testing.T) {
	m := New(newTestStateWithSource(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker for bike_id
	m = updated.(Model)

	if m.selectedColumn != "bike_id" {
		t.Fatalf("expected selectedColumn bike_id, got %q", m.selectedColumn)
	}
	if len(m.pickerColumnValues) == 0 {
		t.Fatal("expected sample values captured for the picker's column")
	}

	view := m.View()
	if !strings.Contains(view, "bike_id") {
		t.Errorf("expected the column name in the picker view, got:\n%s", view)
	}
	if !strings.Contains(view, m.pickerColumnValues[0]) {
		t.Errorf("expected the first sample value (%q) in the picker view, got:\n%s", m.pickerColumnValues[0], view)
	}
}

func TestModel_PreviewViewRendersRealSampleValues(t *testing.T) {
	m := New(newTestStateWithSource(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "bike_id") {
		t.Errorf("expected the bike_id column header in the preview, got:\n%s", view)
	}
	if len(m.previewTable.Rows) == 0 {
		t.Fatal("expected sample rows to be populated from the real fixture")
	}
	if !strings.Contains(view, m.previewTable.Rows[0][0]) {
		t.Errorf("expected the first sample row's first value (%q) in the rendered view, got:\n%s", m.previewTable.Rows[0][0], view)
	}
}
