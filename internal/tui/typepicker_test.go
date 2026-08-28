package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestOpenTypePicker_ListsOnlyValidTypesAndSelectsCurrentType(t *testing.T) {
	m := testModel()
	m.onTableSelected(0, "bikes", "", 0)

	m.openTypePicker("is_installed")

	if m.pickerColumn != "is_installed" {
		t.Fatalf("expected pickerColumn is_installed, got %q", m.pickerColumn)
	}
	if m.picker == nil {
		t.Fatal("expected picker to be built")
	}
	if !m.pages.HasPage("picker") {
		t.Fatal("expected a picker page to be added")
	}
	if m.picker.GetItemCount() == 0 {
		t.Fatal("expected at least one type option (current type is always included)")
	}
	current, _ := m.picker.GetItemText(m.picker.GetCurrentItem())
	if current != "boolean" {
		t.Errorf("expected the picker's initial selection to be is_installed's current type \"boolean\", got %q", current)
	}
}

func TestGridColumnSelected_OpensThePickerForThatColumn(t *testing.T) {
	m := testModel()
	m.onTableSelected(0, "bikes", "", 0)

	m.gridColumnSelected(0, 1) // column 1 is is_installed

	if m.pickerColumn != "is_installed" {
		t.Fatalf("expected pickerColumn is_installed, got %q", m.pickerColumn)
	}
}

func TestPickerKeyCapture_EscClosesWithoutChangingAnything(t *testing.T) {
	m := testModel()
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("is_installed")

	event := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	if got := m.pickerKeyCapture(event); got != nil {
		t.Errorf("expected esc to be consumed (nil), got %v", got)
	}
	if m.pages.HasPage("picker") {
		t.Fatal("expected the picker page to be removed after esc")
	}
}
