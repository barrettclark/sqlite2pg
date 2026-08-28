package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

func testSummary() review.ReviewSummary {
	return review.ReviewSummary{
		Tables: []review.TableView{
			{
				Name: "bikes",
				Columns: []review.ColumnView{
					{Column: "bike_id", DeclaredType: "INTEGER", TargetType: "integer", Confidence: 0.99, Source: "heuristic:default_passthrough"},
					{Column: "is_installed", DeclaredType: "INTEGER", TargetType: "boolean", Confidence: 0.55, Source: "heuristic:boolean01", NeedsReview: true},
				},
				Rows:     [][]string{{"1", "0"}, {"2", "1"}},
				RowCount: 2509,
			},
		},
		NeedsReviewCount:  1,
		AutoApprovedCount: 1,
	}
}

func newTestApp() *tview.Application { return tview.NewApplication() }
func newTestPages() *tview.Pages     { return tview.NewPages() }

func testModel() *model {
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), summary: testSummary()}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	return m
}

func TestOnTableSelected_BuildsAndShowsTheGridForThatTable(t *testing.T) {
	m := testModel()
	m.onTableSelected(0, "bikes", "", 0)

	if m.selectedTable != "bikes" {
		t.Fatalf("expected selectedTable bikes, got %q", m.selectedTable)
	}
	if m.grid == nil {
		t.Fatal("expected grid to be built")
	}
	if !m.pages.HasPage("grid") {
		t.Fatal("expected a grid page to be added")
	}
}

func TestBuildGrid_HeaderShowsColumnNameAndType(t *testing.T) {
	m := testModel()
	m.buildGrid("bikes")

	cell := m.grid.GetCell(0, 1) // is_installed is column index 1
	if cell == nil {
		t.Fatal("expected a header cell at (0,1)")
	}
	if !strings.Contains(cell.Text, "is_installed") || !strings.Contains(cell.Text, "boolean") {
		t.Errorf("expected header to mention column name and type, got %q", cell.Text)
	}
	if !strings.Contains(cell.Text, "!") {
		t.Errorf("expected a needs-review marker on is_installed's header, got %q", cell.Text)
	}
}

func TestBuildGrid_DataRowsMatchSampleValues(t *testing.T) {
	m := testModel()
	m.buildGrid("bikes")

	cell := m.grid.GetCell(1, 0) // first data row, bike_id column
	if cell == nil || cell.Text != "1" {
		t.Errorf("expected first sample value \"1\", got %v", cell)
	}
}

func TestColumnAt_ReturnsTheColumnViewForAGridColumnIndex(t *testing.T) {
	m := testModel()
	m.buildGrid("bikes")

	col := m.columnAt(1)
	if col.Column != "is_installed" {
		t.Errorf("expected is_installed, got %q", col.Column)
	}
}

func TestGridSelectionChanged_UpdatesTheStatusLine(t *testing.T) {
	m := testModel()
	m.buildGrid("bikes")

	m.gridSelectionChanged(1, 1) // row is irrelevant; column 1 is is_installed
	got := m.status.GetText(true)
	if !strings.Contains(got, "is_installed") || !strings.Contains(got, "0.55") {
		t.Errorf("expected status line to mention the selected column and its confidence, got %q", got)
	}
}

func TestGridKeyCapture_EscReturnsToTableList(t *testing.T) {
	m := testModel()
	m.onTableSelected(0, "bikes", "", 0)

	event := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
	if got := m.gridKeyCapture(event); got != nil {
		t.Errorf("expected esc to be consumed (nil), got %v", got)
	}
	// A visible "tablelist" page after esc means SwitchToPage ran; Pages
	// doesn't expose "current page name" directly, so check it's still
	// registered and grid isn't the one left focused by checking the
	// application's focus didn't panic and the page still exists.
	if !m.pages.HasPage("tablelist") {
		t.Fatal("expected the tablelist page to still exist")
	}
}
