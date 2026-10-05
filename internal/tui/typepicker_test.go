package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/review"
)

// withSamples sets one column's sample values; "NULL" is SQL NULL, others are TEXT.
func withSamples(sum review.ReviewSummary, table, column string, values ...string) review.ReviewSummary {
	cells := make([]sampleCell, len(values))
	for i, v := range values {
		cells[i] = sampleCell{value: v, isText: v != "NULL"}
	}
	return withSampleCells(sum, table, column, cells...)
}

// withSampleCells sets one column's sample cells, including their storage class.
func withSampleCells(sum review.ReviewSummary, table, column string, cells ...sampleCell) review.ReviewSummary {
	for i := range sum.Tables {
		tv := &sum.Tables[i]
		if tv.Name != table {
			continue
		}
		idx := -1
		for j, c := range tv.Columns {
			if c.Column == column {
				idx = j
			}
		}
		if idx == -1 {
			continue
		}
		tv.Rows = make([][]string, len(cells))
		tv.IsText = make([][]bool, len(cells))
		for r, c := range cells {
			tv.Rows[r] = make([]string, len(tv.Columns))
			tv.Rows[r][idx] = c.value
			tv.IsText[r] = make([]bool, len(tv.Columns))
			tv.IsText[r][idx] = c.isText
		}
	}
	return sum
}

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
		t.Fatal("expected at least one type option (a type the samples validate as)")
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
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: withSamples(st.Summary(), "bikes", "is_installed", "1", "0")}
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
	if col.Transform != "numeric_text_to_integer" {
		t.Errorf("expected the transform derived from the samples (numeric_text_to_integer), got %q", col.Transform)
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

// TestOnTypeSelected_RebuildsTheTableListPreservingSelection is issue
// #93's (audit finding L7) fix: onTypeSelected now calls buildTableList
// after refreshing m.summary, syncing the table list with whatever the
// decision changed. In today's data model that's a no-op for the actual
// displayed counts — ApplyDecision deliberately never touches
// col.NeedsReview or col.Confidence (issue #53: NeedsReview is a
// permanently-stable profiler verdict, not a to-do flag a decision
// clears), so the needs-review/auto-approved numbers buildTableList
// computes from those fields can't actually go stale under the current
// design. The rebuild is still correct defensively (keeps the list in
// sync with m.summary generally, not just today's specific fields), and
// what IS concretely testable is that it doesn't reset the user's
// position in the list back to the top.
func TestOnTypeSelected_RebuildsTheTableListPreservingSelection(t *testing.T) {
	// A second table, listed alphabetically after "bikes" (buildTableList
	// iterates m.summary.Tables in order — see review_model.go), so
	// selecting it and applying a decision on the FIRST table's column
	// (is_installed, in "bikes") genuinely exercises whether the rebuild
	// preserves the human's position in the list rather than resetting to
	// the top — a single-table fixture can't tell "preserved" apart from
	// "reset to the only possible position" the way this one can.
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
			"trips": {
				ColumnOrder: []string{"trip_id"},
				Columns: map[string]config.ColumnConfig{
					"trip_id": {TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
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

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: withSamples(st.Summary(), "bikes", "is_installed", "1", "0")}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	tripsIndex := -1
	for i := 0; i < m.tableList.GetItemCount(); i++ {
		name, _ := m.tableList.GetItemText(i)
		if name == "trips" {
			tripsIndex = i
		}
	}
	if tripsIndex == -1 {
		t.Fatal("expected trips to be in the table list")
	}
	m.tableList.SetCurrentItem(tripsIndex)

	m.onTableSelected(0, "bikes", "", 0)
	m.openTypePicker("is_installed")
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

	if got := m.tableList.GetCurrentItem(); got != tripsIndex {
		t.Errorf("expected the table list's selection (trips, index %d) to survive the rebuild, got index %d", tripsIndex, got)
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

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: withSamples(st.Summary(), "bikes", "last_reported", "1712345678", "1712345679")}
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
		{"one sample invalid for the type", []string{"2021-06-01", "not-a-date"}, "date", "", false},
		{"NULLs ignored, rest unanimous", []string{"NULL", "2021-06-01", "", "2022-01-15"}, "date", "iso8601_to_date", true},
		{"all NULL", []string{"NULL", ""}, "date", "iso8601_to_date", true},
		{"plain text needs no transform", []string{"a", "b"}, "text", "", true},
		{"uuid always uuid_format", []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "11111111-1111-1111-1111-111111111111"}, "uuid", "uuid_format", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := commonTransformForType(plainCells(c.values...), c.typ, "")
			if got != c.want || ok != c.wantOK {
				t.Errorf("commonTransformForType(%v, %q) = (%q, %v), want (%q, %v)", c.values, c.typ, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestOnTypeSelected_RefusesADateTypeWhenSamplesNeedDifferentTransforms: a
// mixed ISO/compact date column does not offer "date", and a direct pick is
// still refused by onTypeSelected.
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

	if idx := pickerIndexOf(m, "date"); idx != -1 {
		t.Fatal("\"date\" offered for a mixed ISO/compact column, want it omitted")
	}
	m.onTypeSelected(0, "date", "", 0)
	if !strings.Contains(m.lastError, "need different date transforms") {
		t.Errorf("refusal message %q, want it to say the samples need different transforms", m.lastError)
	}

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

func TestOpenTypePicker_AllNullIntegerColumnKeepsCurrentTypeSelected(t *testing.T) {
	m := testModel()
	m.summary = withSamples(m.summary, "bikes", "is_installed", "NULL", "NULL")
	for i := range m.summary.Tables {
		for j := range m.summary.Tables[i].Columns {
			if m.summary.Tables[i].Columns[j].Column == "is_installed" {
				m.summary.Tables[i].Columns[j].TargetType = "integer"
				m.summary.Tables[i].Columns[j].DeclaredType = "INTEGER"
			}
		}
	}
	m.onTableSelected(0, "bikes", "", 0)

	m.openTypePicker("is_installed")

	current, _ := m.picker.GetItemText(m.picker.GetCurrentItem())
	if current != "integer" {
		t.Errorf("expected the all-NULL integer column to keep integer pre-selected, got %q", current)
	}
}

// newAllNullIntegerState is a bikes config whose is_installed is integer with no
// stored transform, so a fresh pick must derive its transform.
func newAllNullIntegerState(t *testing.T) (*review.State, string, *model) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"bike_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"bike_id":      {TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
					"is_installed": {TargetType: "integer", DeclaredType: "INTEGER", Confidence: 0.55, Source: "heuristic:integer"},
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
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), st: st, summary: withSamples(st.Summary(), "bikes", "is_installed", "NULL", "NULL")}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "bikes", "", 0)
	return st, path, m
}

// An all-NULL column offers every type. A fresh pick's transform is derived from
// the type's standard representative value, not verified against the samples.
func TestOnTypeSelected_AllNullIntegerColumnPersistsStandardTransform(t *testing.T) {
	cases := []struct {
		name, choose, wantTransform string
	}{
		{"current integer persists the derived transform, not its stored one", "integer", "numeric_text_to_integer"},
		{"bigint persists numeric_text_to_integer", "bigint", "numeric_text_to_integer"},
		{"boolean persists int_to_bool", "boolean", "int_to_bool"},
		{"text persists no transform", "text", ""},
		{"bytea persists no transform", "bytea", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, path, m := newAllNullIntegerState(t)
			m.openTypePicker("is_installed")

			if current, _ := m.picker.GetItemText(m.picker.GetCurrentItem()); current != "integer" {
				t.Errorf("expected integer pre-selected, got %q", current)
			}
			idx := -1
			for i := 0; i < m.picker.GetItemCount(); i++ {
				if text, _ := m.picker.GetItemText(i); text == tc.choose {
					idx = i
				}
			}
			if idx == -1 {
				t.Fatalf("%q not offered for an all-NULL integer column", tc.choose)
			}
			m.onTypeSelected(idx, tc.choose, "", 0)

			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			col := loaded.Tables["bikes"].Columns["is_installed"]
			if col.TargetType != tc.choose {
				t.Errorf("TargetType = %q, want %q", col.TargetType, tc.choose)
			}
			if col.Transform != tc.wantTransform {
				t.Errorf("persisted Transform = %q, want %q", col.Transform, tc.wantTransform)
			}
		})
	}
}

// A NOT NULL column with "" rows refuses a type whose transform turns "" into
// NULL, and the stored config is left alone.
func TestOnTypeSelected_NotNullColumnRefusesNullProducingTransform(t *testing.T) {
	_, path, m := newAllNullIntegerState(t)
	m.summary = withSamples(m.summary, "bikes", "is_installed", "1", "")
	for i := range m.summary.Tables {
		for j := range m.summary.Tables[i].Columns {
			if m.summary.Tables[i].Columns[j].Column == "is_installed" {
				m.summary.Tables[i].Columns[j].RejectNull = true
			}
		}
	}
	m.openTypePicker("is_installed")
	// Called directly: the offer list already omits bigint, and the re-check must hold regardless.
	m.onTypeSelected(0, "bigint", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["is_installed"]
	if col.TargetType != "integer" || col.Transform != "" {
		t.Errorf("config changed to (%q, %q), want unchanged (integer, \"\")", col.TargetType, col.Transform)
	}
}

// secondaryOf returns the picker item's secondary text for typ, if listed.
func secondaryOf(m *model, typ string) (string, bool) {
	for i := 0; i < m.picker.GetItemCount(); i++ {
		if text, secondary := m.picker.GetItemText(i); text == typ {
			return secondary, true
		}
	}
	return "", false
}

// offeredTypes returns the picker's items in display order.
func offeredTypes(m *model) []string {
	var types []string
	for i := 0; i < m.picker.GetItemCount(); i++ {
		text, _ := m.picker.GetItemText(i)
		types = append(types, text)
	}
	return types
}

func TestOpenTypePicker_EmptyRowWarning(t *testing.T) {
	const marker = `SQLite "" is not NULL; "" rows load as NULL`
	cases := []struct {
		name        string
		declared    string
		values      []string
		rejectNull  bool
		wantOffered []string
		wantWarned  []string
	}{
		{
			name:        "nullable INTEGER with empty row warns on the NULL-producing integer types",
			declared:    "INTEGER",
			values:      []string{"1", ""},
			wantOffered: []string{"text", "integer", "bigint", "smallint", "double precision", "real", "bytea"},
			wantWarned:  []string{"integer", "bigint", "smallint", "double precision", "real"},
		},
		{
			name:        "NOT NULL INTEGER with empty row offers no NULL-producing type and no warning",
			declared:    "INTEGER",
			values:      []string{"1", ""},
			rejectNull:  true,
			wantOffered: []string{"text", "bytea"},
		},
		{
			name:        "INTEGER without empty row has no warning",
			declared:    "INTEGER",
			values:      []string{"1", "2"},
			wantOffered: []string{"text", "integer", "bigint", "smallint", "double precision", "real", "jsonb", "bytea"},
		},
		{
			name:        "TEXT with empty row keeps its list and shows no warning",
			declared:    "TEXT",
			values:      []string{"abc", ""},
			wantOffered: []string{"text", "bytea"},
		},
		{
			name:        "TEXT without empty row keeps the same list",
			declared:    "TEXT",
			values:      []string{"abc", "def"},
			wantOffered: []string{"text", "bytea"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, m := newAllNullIntegerState(t)
			m.summary = withSamples(m.summary, "bikes", "is_installed", tc.values...)
			for i := range m.summary.Tables {
				for j := range m.summary.Tables[i].Columns {
					if m.summary.Tables[i].Columns[j].Column == "is_installed" {
						m.summary.Tables[i].Columns[j].RejectNull = tc.rejectNull
						m.summary.Tables[i].Columns[j].DeclaredType = tc.declared
					}
				}
			}
			m.openTypePicker("is_installed")

			if got := offeredTypes(m); !slices.Equal(got, tc.wantOffered) {
				t.Errorf("offered = %v, want %v", got, tc.wantOffered)
			}
			for _, typ := range offeredTypes(m) {
				secondary, _ := secondaryOf(m, typ)
				warned := slices.Contains(tc.wantWarned, typ)
				if warned {
					shown := tc.values[0]
					if strings.HasPrefix(typ, "double") || typ == "real" || typ == "numeric" {
						shown += ".0"
					}
					want := fmt.Sprintf(`e.g. %s; %s`, shown, marker)
					if secondary != want {
						t.Errorf("%q secondary = %q, want %q", typ, secondary, want)
					}
				} else if strings.Contains(secondary, marker) {
					t.Errorf("%q secondary %q carries the empty-row note, want none", typ, secondary)
				}
			}
		})
	}
}

func TestOnTypeSelected_EmptyRowWarningShownInStatus(t *testing.T) {
	_, path, m := newAllNullIntegerState(t)
	m.summary = withSamples(m.summary, "bikes", "is_installed", "1", "")
	m.openTypePicker("is_installed")

	m.onTypeSelected(0, "bigint", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if col := loaded.Tables["bikes"].Columns["is_installed"]; col.TargetType != "bigint" || col.Transform != "numeric_text_to_integer" {
		t.Fatalf("expected bigint/numeric_text_to_integer persisted, got (%q, %q)", col.TargetType, col.Transform)
	}
	status := m.status.GetText(false)
	if !strings.Contains(status, "confidence") {
		t.Errorf("status %q lost the column status line", status)
	}
	if !strings.Contains(status, "empty-string rows load as NULL (SQLite \"\" is not NULL)") {
		t.Errorf("status %q does not show the empty-string note", status)
	}
}

// newColumnState is a bikes config whose is_installed has the given target,
// stored transform, and declared type.
func newColumnState(t *testing.T, targetType, transform, declared string) (*review.State, string, *model) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Tables: map[string]config.TableConfig{
			"bikes": {
				ColumnOrder: []string{"bike_id", "is_installed"},
				Columns: map[string]config.ColumnConfig{
					"bike_id":      {TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
					"is_installed": {TargetType: targetType, Transform: transform, DeclaredType: declared, Confidence: 0.55, Source: "heuristic:test"},
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
	return st, path, m
}

// Confirming the current type persists the transform the samples were
// validated with, not the stored one that may not fit them.
func TestOnTypeSelected_CurrentTypePersistsValidatedTransform(t *testing.T) {
	t.Run("nullable integer with empty row: stored empty transform is replaced", func(t *testing.T) {
		_, path, m := newColumnState(t, "integer", "", "INTEGER")
		m.summary = withSamples(m.summary, "bikes", "is_installed", "1", "")
		m.openTypePicker("is_installed")
		if !slices.Contains(offeredTypes(m), "integer") {
			t.Fatalf("integer not offered, got %v", offeredTypes(m))
		}
		m.onTypeSelected(0, "integer", "", 0)
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		col := loaded.Tables["bikes"].Columns["is_installed"]
		if col.Transform != "numeric_text_to_integer" {
			t.Errorf("persisted Transform = %q, want numeric_text_to_integer", col.Transform)
		}
	})

	t.Run("REAL +Inf with float current type: derived transform is persisted", func(t *testing.T) {
		_, path, m := newColumnState(t, "double precision", "numeric_text_to_double", "REAL")
		m.summary = withSampleCells(m.summary, "bikes", "is_installed", sampleCell{value: "+Inf", isText: false})
		m.openTypePicker("is_installed")
		idx := slices.Index(offeredTypes(m), "double precision")
		if idx == -1 {
			t.Fatalf("double precision not offered, got %v", offeredTypes(m))
		}
		m.onTypeSelected(idx, "double precision", "", 0)
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		col := loaded.Tables["bikes"].Columns["is_installed"]
		if col.Transform != "" {
			t.Errorf("persisted Transform = %q, want \"\" (REAL +Inf passes through)", col.Transform)
		}
	})
}

// Re-confirming the current type is refused when the samples no longer derive
// a transform, and the stored transform stays in the config.
func TestOnTypeSelected_CurrentTypeRefusalKeepsStoredTransform(t *testing.T) {
	_, path, m := newColumnState(t, "integer", "numeric_text_to_integer", "INTEGER")
	m.summary = withSampleCells(m.summary, "bikes", "is_installed", sampleCell{value: "1,000", isText: true})
	m.openTypePicker("is_installed")
	if slices.Contains(offeredTypes(m), "integer") {
		t.Fatalf("integer offered for a sample it cannot load, got %v", offeredTypes(m))
	}

	m.onTypeSelected(0, "integer", "", 0)
	if !strings.Contains(m.lastError, "samples can't load as integer") {
		t.Errorf("refusal message %q, want it to say samples can't load as integer", m.lastError)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["is_installed"]
	if col.TargetType != "integer" || col.Transform != "numeric_text_to_integer" {
		t.Errorf("config changed to (%q, %q), want unchanged (integer, numeric_text_to_integer)", col.TargetType, col.Transform)
	}
}

// A TEXT "NULL" is a value: it persists text with no transform, and integer
// is refused rather than treated as a NULL that loads anywhere.
func TestOnTypeSelected_TextNullValuePersistsAndRefusesInteger(t *testing.T) {
	cells := []sampleCell{{value: "NULL", isText: true}}
	if transform, ok := commonTransformForType(cells, "text", "TEXT"); !ok || transform != "" {
		t.Errorf("commonTransformForType(text) = (%q, %v), want (\"\", true)", transform, ok)
	}
	if _, ok := commonTransformForType(cells, "integer", "TEXT"); ok {
		t.Errorf("commonTransformForType(integer) ok for TEXT \"NULL\", want refused")
	}

	_, path, m := newColumnState(t, "integer", "", "TEXT")
	m.summary = withSampleCells(m.summary, "bikes", "is_installed", cells...)
	m.openTypePicker("is_installed")
	if !slices.Contains(offeredTypes(m), "text") || slices.Contains(offeredTypes(m), "integer") {
		t.Fatalf("offered = %v, want text offered and integer omitted", offeredTypes(m))
	}

	// Refuse integer first: a pick rebuilds the summary, which drops the injected samples.
	m.onTypeSelected(0, "integer", "", 0)
	if !strings.Contains(m.lastError, "samples can't load as integer") {
		t.Errorf("refusal message %q, want it to say samples can't load as integer", m.lastError)
	}
	m.onTypeSelected(slices.Index(offeredTypes(m), "text"), "text", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["is_installed"]
	if col.TargetType != "text" || col.Transform != "" {
		t.Errorf("persisted (%q, %q), want (text, \"\")", col.TargetType, col.Transform)
	}
}

func TestRefusalMessage(t *testing.T) {
	cases := []struct {
		name       string
		cells      []sampleCell
		typ        string
		declared   string
		rejectNull bool
		want       string // substring the message must contain; "" means no refusal
		forbid     string // substring the message must not contain
	}{
		{"valid samples are not refused", []sampleCell{{value: "1", isText: false}, {value: "2", isText: false}}, "integer", "INTEGER", false, "", ""},
		{"disagreeing transforms", []sampleCell{{value: "2021-06-01", isText: true}, {value: "20210704", isText: true}}, "date", "TEXT", false, "sample rows need different date transforms", ""},
		{"value that does not fit", []sampleCell{{value: "1,000", isText: true}}, "integer", "INTEGER", false, "samples can't load as integer", ""},
		{"TEXT NULL is not an integer", []sampleCell{{value: "NULL", isText: true}}, "integer", "TEXT", false, "samples can't load as integer", ""},
		{"NOT NULL bigint with empty row becomes NULL", []sampleCell{{value: "1", isText: true}, {value: ""}}, "bigint", "INTEGER", true, "would become NULL on this NOT NULL column", ""},
		{"nullable date with empty row does not load", []sampleCell{{value: "2021-06-01", isText: true}, {value: ""}}, "date", "TEXT", false, "empty strings don't load as date", "become NULL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := refusalMessage("col", tc.typ, "target", tc.cells, tc.declared, tc.rejectNull)
			if tc.want == "" {
				if got != "" {
					t.Errorf("refusalMessage = %q, want no refusal", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("refusalMessage = %q, want it to contain %q", got, tc.want)
			}
			if tc.forbid != "" && strings.Contains(got, tc.forbid) {
				t.Errorf("refusalMessage = %q, must not contain %q", got, tc.forbid)
			}
		})
	}
}

// The full empty-row note follows the highlighted type and clears on a type
// without one; Esc restores the status line.
func TestOpenTypePicker_StatusFollowsHighlightedType(t *testing.T) {
	_, _, m := newAllNullIntegerState(t)
	m.summary = withSamples(m.summary, "bikes", "is_installed", "1", "")
	m.status.SetText("base status")
	m.openTypePicker("is_installed")

	if !strings.Contains(m.status.GetText(false), "is not NULL") {
		t.Errorf("status %q does not show the note for highlighted integer", m.status.GetText(false))
	}
	m.picker.SetCurrentItem(slices.Index(offeredTypes(m), "text"))
	if status := m.status.GetText(false); strings.Contains(status, "is not NULL") {
		t.Errorf("status %q still shows the note after highlighting text", status)
	}
	m.picker.SetCurrentItem(slices.Index(offeredTypes(m), "bigint"))
	if status := m.status.GetText(false); !strings.Contains(status, "empty-string rows load as NULL") {
		t.Errorf("status %q does not show the note for highlighted bigint", status)
	}
	m.pickerKeyCapture(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if status := m.status.GetText(false); status != "base status" {
		t.Errorf("status after Esc = %q, want base status", status)
	}
}

// A TEXT "1.5" column re-confirmed as a float persists numeric_text_to_double,
// which parses it into a float64; a REAL column needs no transform.
func TestOnTypeSelected_FloatCurrentTypePersistsTransformMatchingSamples(t *testing.T) {
	t.Run("TEXT 1.5 persists numeric_text_to_double", func(t *testing.T) {
		_, path, m := newColumnState(t, "double precision", "", "TEXT")
		m.summary = withSampleCells(m.summary, "bikes", "is_installed", sampleCell{value: "1.5", isText: true})
		m.openTypePicker("is_installed")
		idx := slices.Index(offeredTypes(m), "double precision")
		if idx == -1 {
			t.Fatalf("double precision not offered, got %v", offeredTypes(m))
		}
		m.onTypeSelected(idx, "double precision", "", 0)

		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := loaded.Tables["bikes"].Columns["is_installed"].Transform; got != "numeric_text_to_double" {
			t.Fatalf("persisted Transform = %q, want numeric_text_to_double", got)
		}
		v, err := copywriter.Transform("numeric_text_to_double", "1.5")
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		if f, ok := v.(float64); !ok || f != 1.5 {
			t.Errorf("Transform produced %#v, want float64(1.5)", v)
		}
	})

	t.Run("REAL 1.5 persists no transform", func(t *testing.T) {
		_, path, m := newColumnState(t, "double precision", "", "REAL")
		m.summary = withSampleCells(m.summary, "bikes", "is_installed", sampleCell{value: "1.5", isText: false})
		m.openTypePicker("is_installed")
		idx := slices.Index(offeredTypes(m), "double precision")
		if idx == -1 {
			t.Fatalf("double precision not offered, got %v", offeredTypes(m))
		}
		m.onTypeSelected(idx, "double precision", "", 0)

		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := loaded.Tables["bikes"].Columns["is_installed"].Transform; got != "" {
			t.Errorf("persisted Transform = %q, want \"\"", got)
		}
	})
}

// A failed apply restores the status line to the base text, not the note for
// the highlighted type.
func TestOnTypeSelected_ApplyErrorRestoresStatusBase(t *testing.T) {
	_, path, m := newAllNullIntegerState(t)
	m.summary = withSamples(m.summary, "bikes", "is_installed", "1", "")
	m.status.SetText("base status")
	m.openTypePicker("is_installed")
	m.picker.SetCurrentItem(slices.Index(offeredTypes(m), "bigint"))
	if !strings.Contains(m.status.GetText(false), "is not NULL") {
		t.Fatalf("status %q does not show the note for highlighted bigint", m.status.GetText(false))
	}

	// Replace the config file with a directory so the save fails.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	m.onTypeSelected(0, "bigint", "", 0)

	if !strings.HasPrefix(m.lastError, "apply decision failed") {
		t.Fatalf("expected an apply error, got %q", m.lastError)
	}
	if status := m.status.GetText(false); status != "base status" {
		t.Errorf("status after apply error = %q, want base status", status)
	}
}

// numeric on TEXT would round through float64, so it is not offered; real and
// double precision still are.
func TestValidTypesForColumn_TextNumericNotOfferedForLongDecimal(t *testing.T) {
	got := validTypesForColumn([]sampleCell{{value: "9007199254740993.0", isText: true}}, "TEXT", false)
	if containsType(got, "numeric") {
		t.Errorf("numeric offered for a TEXT decimal beyond float64 precision, got %v", got)
	}
	for _, typ := range []string{"double precision", "real"} {
		if !containsType(got, typ) {
			t.Errorf("%q not offered for a TEXT decimal, got %v", typ, got)
		}
	}
}

// A stored transform that still converts every sample is kept on re-confirm.
func TestOnTypeSelected_ReconfirmKeepsStoredTransformThatFitsSamples(t *testing.T) {
	_, path, m := newColumnState(t, "double precision", "nullif_sentinels", "TEXT")
	m.summary = withSampleCells(m.summary, "bikes", "is_installed",
		sampleCell{value: "1.5", isText: true}, sampleCell{value: "NA", isText: true})
	m.openTypePicker("is_installed")
	// Not offered (the derived transform fails on "NA"), but re-confirming the current type is still allowed.
	m.onTypeSelected(0, "double precision", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Tables["bikes"].Columns["is_installed"].Transform; got != "nullif_sentinels" {
		t.Errorf("persisted Transform = %q, want the stored nullif_sentinels kept", got)
	}
}

// A stored transform that fails the samples is replaced by the derived one.
func TestOnTypeSelected_ReconfirmReplacesStoredTransformThatFailsSamples(t *testing.T) {
	_, path, m := newColumnState(t, "double precision", "numeric_text_to_integer", "TEXT")
	m.summary = withSampleCells(m.summary, "bikes", "is_installed", sampleCell{value: "1.5", isText: true})
	m.openTypePicker("is_installed")
	idx := slices.Index(offeredTypes(m), "double precision")
	if idx == -1 {
		t.Fatalf("double precision not offered, got %v", offeredTypes(m))
	}
	m.onTypeSelected(idx, "double precision", "", 0)

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := loaded.Tables["bikes"].Columns["is_installed"].Transform; got != "numeric_text_to_double" {
		t.Errorf("persisted Transform = %q, want numeric_text_to_double", got)
	}
}
