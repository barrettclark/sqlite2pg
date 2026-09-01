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

// TestOnTypeSelected_SelectingTimestamptzForAnEpochIntegerColumnAttachesTheMatchingTransform
// reproduces issue #41's exact failure scenario: bikes.last_reported is
// integer holding a raw Unix epoch seconds value, timestamptz is offered by
// the picker (issue #27's transform-aware previewValueForType, credited via
// dateTransformPreview) because unix_epoch_seconds actually converts it —
// but selecting it is a genuine type change (integer -> timestamptz), so
// issue #18's "type changed -> clear transform" rule fires. Without this
// fix, the saved config ends up with target_type: timestamptz and
// transform: "", and the real COPY sends a raw int64 into a timestamptz
// column and fails. Selecting timestamptz here must attach the
// unix_epoch_seconds transform that made the option valid in the first
// place, not discard it.
func TestOnTypeSelected_SelectingTimestamptzForAnEpochIntegerColumnAttachesTheMatchingTransform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"last_reported"},
				Columns: map[string]config.ColumnConfig{
					"last_reported": {
						TargetType: "integer",
						Confidence: 0.99,
						Source:     "heuristic:default_passthrough",
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

	summary := review.ReviewSummary{Tables: []review.TableView{
		{
			Name: "bikes",
			Columns: []review.ColumnView{
				{Column: "last_reported", DeclaredType: "INTEGER", TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
			},
			Rows: [][]string{{"1712345678"}},
		},
	}}

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("last_reported")

	idx := -1
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		if text == "timestamptz" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("expected \"timestamptz\" to be offered as a valid option for a plausible Unix epoch value")
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
		t.Errorf("expected Transform unix_epoch_seconds (the transform that made timestamptz valid), got %q", col.Transform)
	}
}

// TestOnTypeSelected_SelectingUUIDArrayForAUUIDListColumnAttachesUUIDListFormatTransform
// mirrors the epoch/timestamptz scenario above for issue #12's uuid[]
// option: a text column holding beets' NUL-joined UUID list format offers
// uuid[] in the picker, but without uuid_list_format attached the raw
// NUL-joined string goes to a uuid[] column and fails at COPY time.
func TestOnTypeSelected_SelectingUUIDArrayForAUUIDListColumnAttachesUUIDListFormatTransform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"mb_albumids"},
				Columns: map[string]config.ColumnConfig{
					"mb_albumids": {
						TargetType: "text",
						Confidence: 0.99,
						Source:     "heuristic:default_passthrough",
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

	summary := review.ReviewSummary{Tables: []review.TableView{
		{
			Name: "bikes",
			Columns: []review.ColumnView{
				{Column: "mb_albumids", DeclaredType: "TEXT", TargetType: "text", Confidence: 0.99, Source: "heuristic:default_passthrough"},
			},
			Rows: [][]string{{"cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022"}},
		},
	}}

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("mb_albumids")

	idx := -1
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		if text == "uuid[]" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("expected \"uuid[]\" to be offered as a valid option for a NUL-joined UUID list value")
	}
	m.onTypeSelected(idx, "uuid[]", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["mb_albumids"]
	if col.TargetType != "uuid[]" {
		t.Errorf("expected TargetType uuid[], got %q", col.TargetType)
	}
	if col.Transform != "uuid_list_format" {
		t.Errorf("expected Transform uuid_list_format (the transform that made uuid[] valid), got %q", col.Transform)
	}
}

// TestOnTypeSelected_SelectingTextForAPlainStringColumnClearsTheTransform
// guards against overcorrecting: a genuine type change to a type that
// needs no transform at all (e.g. text for an ordinary string value) must
// still result in Transform "", not spuriously carry over some other
// type's transform.
func TestOnTypeSelected_SelectingTextForAPlainStringColumnClearsTheTransform(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"label"},
				Columns: map[string]config.ColumnConfig{
					"label": {
						TargetType: "integer",
						Transform:  "numeric_text_to_integer",
						Confidence: 0.6,
						Source:     "heuristic:numeric_text",
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

	summary := review.ReviewSummary{Tables: []review.TableView{
		{
			Name: "bikes",
			Columns: []review.ColumnView{
				{Column: "label", DeclaredType: "TEXT", TargetType: "integer", Transform: "numeric_text_to_integer", Confidence: 0.6, Source: "heuristic:numeric_text"},
			},
			Rows: [][]string{{"hello world"}},
		},
	}}

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("label")

	idx := -1
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		if text == "text" {
			idx = i
		}
	}
	if idx == -1 {
		t.Fatal("expected \"text\" to be offered as a valid option for a plain string value")
	}
	m.onTypeSelected(idx, "text", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["label"]
	if col.TargetType != "text" {
		t.Errorf("expected TargetType text, got %q", col.TargetType)
	}
	if col.Transform != "" {
		t.Errorf("expected Transform cleared for a type that needs no transform, got %q", col.Transform)
	}
}

// TestCommonTransformForType_UnanimousAndMixed is issue #64's core: the
// picker offers date/timestamptz when EVERY sample converts to it, but
// different rows can legitimately need different transforms. A single
// ColumnConfig.Transform can't express "iso8601 for some rows, yyyymmdd
// for others", so onTypeSelected must only attach a transform when all
// non-NULL samples resolve to the same one.
func TestCommonTransformForType_UnanimousAndMixed(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		typ    string
		want   string
		wantOK bool
	}{
		{"all ISO dates", []string{"2021-06-01", "2022-01-15"}, "date", "iso8601_to_date", true},
		{"all yyyymmdd", []string{"20210601", "20220115"}, "date", "yyyymmdd_to_date", true},
		{"mixed ISO + yyyymmdd", []string{"2021-06-01", "20210704", "2022-01-15"}, "date", "", false},
		{"mixed epoch + excel serial", []string{"1712345678", "40000"}, "timestamptz", "", false},
		{"NULLs ignored, rest unanimous", []string{"NULL", "2021-06-01", "", "2022-01-15"}, "date", "iso8601_to_date", true},
		{"all NULL", []string{"NULL", ""}, "date", "", true},
		{"plain text needs no transform", []string{"a", "b"}, "text", "", true},
		{"uuid always uuid_format", []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "11111111-1111-1111-1111-111111111111"}, "uuid", "uuid_format", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := commonTransformForType(c.values, c.typ)
			if got != c.want || ok != c.wantOK {
				t.Errorf("commonTransformForType(%v, %q) = (%q, %v), want (%q, %v)", c.values, c.typ, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestOnTypeSelected_RefusesADateTypeWhenSamplesNeedDifferentTransforms is
// the end-to-end #64 case: a text column mixing ISO and compact date
// spellings offers "date" (every row converts), but the old code stored
// the first row's transform (iso8601_to_date) and the real COPY then
// failed on every 20210704-style row. onTypeSelected must refuse the pick
// rather than persist a config guaranteed to break the load.
func TestOnTypeSelected_RefusesADateTypeWhenSamplesNeedDifferentTransforms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"t": {
				ColumnOrder: []string{"d"},
				Columns: map[string]config.ColumnConfig{
					"d": {TargetType: "text", Confidence: 0.6, Source: "heuristic:default_passthrough"},
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

	summary := review.ReviewSummary{Tables: []review.TableView{
		{
			Name: "t",
			Columns: []review.ColumnView{
				{Column: "d", DeclaredType: "TEXT", TargetType: "text", Confidence: 0.6, Source: "heuristic:default_passthrough"},
			},
			Rows: [][]string{{"2021-06-01"}, {"20210704"}, {"2022-01-15"}},
		},
	}}

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "t", "", 0)
	m.openTypePicker("d")

	idx := pickerIndexOf(m, "date")
	if idx == -1 {
		t.Fatal("expected \"date\" to be offered (every sample converts to a date, just via different transforms)")
	}
	m.onTypeSelected(idx, "date", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["t"].Columns["d"]
	if col.TargetType != "text" {
		t.Errorf("expected the mixed-format date pick to be refused, leaving TargetType text, got %q (transform %q)", col.TargetType, col.Transform)
	}
}

// TestOnTypeSelected_AttachesTheSharedTransformWhenEverySampleAgrees is the
// positive counterpart: an all-ISO-date column still gets iso8601_to_date.
func TestOnTypeSelected_AttachesTheSharedTransformWhenEverySampleAgrees(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"t": {
				ColumnOrder: []string{"d"},
				Columns: map[string]config.ColumnConfig{
					"d": {TargetType: "text", Confidence: 0.6, Source: "heuristic:default_passthrough"},
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

	summary := review.ReviewSummary{Tables: []review.TableView{
		{
			Name: "t",
			Columns: []review.ColumnView{
				{Column: "d", DeclaredType: "TEXT", TargetType: "text", Confidence: 0.6, Source: "heuristic:default_passthrough"},
			},
			Rows: [][]string{{"2021-06-01"}, {"2022-01-15"}, {"2023-12-31"}},
		},
	}}

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "t", "", 0)
	m.openTypePicker("d")

	idx := pickerIndexOf(m, "date")
	if idx == -1 {
		t.Fatal("expected \"date\" to be offered for an all-ISO-date column")
	}
	m.onTypeSelected(idx, "date", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["t"].Columns["d"]
	if col.TargetType != "date" || col.Transform != "iso8601_to_date" {
		t.Errorf("expected date + iso8601_to_date, got %q + %q", col.TargetType, col.Transform)
	}
}

func pickerIndexOf(m *model, typeName string) int {
	for i := 0; i < m.picker.GetItemCount(); i++ {
		if text, _ := m.picker.GetItemText(i); text == typeName {
			return i
		}
	}
	return -1
}
