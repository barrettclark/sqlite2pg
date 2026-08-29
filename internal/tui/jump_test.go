package tui

import (
	"testing"

	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// multiTableSummary builds a two-table summary where each table has one
// auto-approved column and one flagged-for-review column, so
// jumpToFlagged has more than one table to cross.
func multiTableSummary() review.ReviewSummary {
	return review.ReviewSummary{
		Tables: []review.TableView{
			{
				Name: "albums",
				Columns: []review.ColumnView{
					{Column: "AlbumId", TargetType: "integer", Confidence: 0.99},
					{Column: "ArtistId", TargetType: "boolean", Confidence: 0.55, NeedsReview: true},
				},
				Rows: [][]string{{"1", "0"}},
			},
			{
				Name: "tracks",
				Columns: []review.ColumnView{
					{Column: "TrackId", TargetType: "integer", Confidence: 0.99},
					{Column: "Flag", TargetType: "boolean", Confidence: 0.55, NeedsReview: true},
				},
				Rows: [][]string{{"1", "1"}},
			},
		},
		NeedsReviewCount:  2,
		AutoApprovedCount: 2,
	}
}

func multiTableTestModel() *model {
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), summary: multiTableSummary()}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	return m
}

func TestJumpToFlagged_MovesToTheFlaggedColumnInTheCurrentTable(t *testing.T) {
	m := multiTableTestModel()
	m.onTableSelected(0, "albums", "", 0)
	m.grid.Select(0, 0) // start on AlbumId, which isn't flagged

	m.jumpToFlagged(true)

	_, col := m.grid.GetSelection()
	if m.columnAt(col).Column != "ArtistId" {
		t.Errorf("expected to land on ArtistId, got %q", m.columnAt(col).Column)
	}
}

func TestJumpToFlagged_CrossesIntoAnotherTable(t *testing.T) {
	m := multiTableTestModel()
	m.onTableSelected(0, "albums", "", 0)
	m.grid.Select(0, 1) // already on ArtistId, the only flagged column in albums

	m.jumpToFlagged(true)

	if m.selectedTable != "tracks" {
		t.Fatalf("expected to switch to tracks, got %q", m.selectedTable)
	}
	_, col := m.grid.GetSelection()
	if m.columnAt(col).Column != "Flag" {
		t.Errorf("expected to land on Flag, got %q", m.columnAt(col).Column)
	}
}

func TestJumpToFlagged_WrapsAroundAcrossTables(t *testing.T) {
	m := multiTableTestModel()
	m.onTableSelected(0, "tracks", "", 0)
	m.grid.Select(0, 1) // Flag, the last flagged column overall

	m.jumpToFlagged(true)

	if m.selectedTable != "albums" {
		t.Fatalf("expected to wrap around to albums, got %q", m.selectedTable)
	}
	_, col := m.grid.GetSelection()
	if m.columnAt(col).Column != "ArtistId" {
		t.Errorf("expected to land on ArtistId, got %q", m.columnAt(col).Column)
	}
}

func TestJumpToFlagged_BackwardStepsToThePreviousFlaggedColumn(t *testing.T) {
	m := multiTableTestModel()
	m.onTableSelected(0, "tracks", "", 0)
	m.grid.Select(0, 1) // Flag

	m.jumpToFlagged(false)

	if m.selectedTable != "albums" {
		t.Fatalf("expected to move back to albums, got %q", m.selectedTable)
	}
	_, col := m.grid.GetSelection()
	if m.columnAt(col).Column != "ArtistId" {
		t.Errorf("expected to land on ArtistId, got %q", m.columnAt(col).Column)
	}
}

func TestJumpToFlagged_StillReachesAColumnAlreadyReviewed(t *testing.T) {
	// A jump target that's already been reviewed must stay reachable —
	// NeedsReview never changes once a human resolves a column, so
	// flaggedColumns (and therefore this jump) still includes it.
	summary := multiTableSummary()
	summary.Tables[0].Columns[1].Reviewed = true // ArtistId already reviewed

	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "tracks", "", 0)
	m.grid.Select(0, 1) // Flag

	m.jumpToFlagged(true)

	if m.selectedTable != "albums" {
		t.Fatalf("expected to reach albums (ArtistId is reviewed but still flagged), got %q", m.selectedTable)
	}
	_, col := m.grid.GetSelection()
	if m.columnAt(col).Column != "ArtistId" {
		t.Errorf("expected to land on ArtistId, got %q", m.columnAt(col).Column)
	}
}

func TestJumpToFlagged_NoFlaggedColumnsUpdatesStatusWithoutPanicking(t *testing.T) {
	summary := review.ReviewSummary{Tables: []review.TableView{
		{Name: "clean", Columns: []review.ColumnView{{Column: "id", TargetType: "integer", Confidence: 0.99}}},
	}}
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), summary: summary}
	m.status = tview.NewTextView()
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.onTableSelected(0, "clean", "", 0)

	m.jumpToFlagged(true)

	if got := m.status.GetText(true); got != "no columns need review" {
		t.Errorf("expected the no-flagged-columns message, got %q", got)
	}
}

func TestColumnIndex_FindsTheColumnAcrossTables(t *testing.T) {
	summary := multiTableSummary()
	if idx := columnIndex(summary, "tracks", "Flag"); idx != 1 {
		t.Errorf("expected index 1, got %d", idx)
	}
	if idx := columnIndex(summary, "tracks", "missing"); idx != -1 {
		t.Errorf("expected -1 for a missing column, got %d", idx)
	}
}
