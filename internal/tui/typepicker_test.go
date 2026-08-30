package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/review"
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

func TestOpenTypePicker_SecondaryTextShowsCoercedPreview(t *testing.T) {
	m := testModel()
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("is_installed")

	idx := -1
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		if text == "boolean" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("expected \"boolean\" to be a valid option")
	}
	_, secondary := m.picker.GetItemText(idx)
	if secondary == "" {
		t.Error("expected non-empty secondary text showing the coerced preview")
	}
}

func newTestState(t *testing.T) (*review.State, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"bike_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"bike_id":      {TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
					"is_installed": {TargetType: "boolean", Transform: "int_to_bool", Confidence: 0.55, Source: "heuristic:boolean01"},
				},
			},
		},
	}
	if err := config.Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := review.NewState(path, 0.9)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return st, path
}

func TestOnTypeSelected_AppliesTheDecisionAndRefreshesTheGrid(t *testing.T) {
	st, path := newTestState(t)
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: st.Summary()}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("is_installed")

	// Find "integer" in the picker's items and select it.
	idx := -1
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		if text == "integer" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("expected \"integer\" to be a valid option for is_installed (0/1 values)")
	}
	m.onTypeSelected(idx, "integer", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["is_installed"]
	if col.TargetType != "integer" {
		t.Errorf("expected TargetType integer, got %q", col.TargetType)
	}
	if col.Transform != "" {
		t.Errorf("expected Transform cleared, got %q", col.Transform)
	}
	if col.Source != "human_override" {
		t.Errorf("expected source human_override, got %q", col.Source)
	}
	if col.Rationale != "human override via TUI" {
		t.Errorf("expected rationale \"human override via TUI\", got %q", col.Rationale)
	}
	if m.pages.HasPage("picker") {
		t.Error("expected the picker to close after applying")
	}

	cell := m.grid.GetCell(0, 1)
	if cell == nil || !strings.Contains(cell.Text, "integer") {
		t.Errorf("expected the grid header to show the new type, got %v", cell)
	}
}

// TestOnTypeSelected_ReselectingTheSameTypePreservesTheTransform guards
// against issue #18: a human re-confirming the picker's own current
// selection (the natural "yes, that's correct" gesture) must not clear a
// transform the column actually needs at COPY time. Only a genuine change
// to a different target type should clear a stale transform.
func TestOnTypeSelected_ReselectingTheSameTypePreservesTheTransform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"last_reported"},
				Columns: map[string]config.ColumnConfig{
					"last_reported": {
						TargetType: "timestamptz",
						Transform:  "unix_epoch_seconds",
						Confidence: 0.85,
						Source:     "heuristic:unix_epoch_seconds",
					},
				},
			},
		},
	}
	if err := config.Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := review.NewState(path, 0.9)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: st.Summary()}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("last_reported")

	// Simulate the natural "yes, that's correct" gesture: re-selecting the
	// type already shown as current.
	idx := -1
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		if text == "timestamptz" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("expected \"timestamptz\" to be a valid option for last_reported")
	}
	m.onTypeSelected(idx, "timestamptz", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["last_reported"]
	if col.TargetType != "timestamptz" {
		t.Errorf("expected TargetType timestamptz, got %q", col.TargetType)
	}
	if col.Transform != "unix_epoch_seconds" {
		t.Errorf("expected Transform preserved as unix_epoch_seconds when type unchanged, got %q", col.Transform)
	}
}
