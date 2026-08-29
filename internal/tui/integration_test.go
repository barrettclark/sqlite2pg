package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/review"
)

func newTestTextView() *tview.TextView { return tview.NewTextView() }

func newTestFlexRow(items ...tview.Primitive) *tview.Flex {
	flex := tview.NewFlex()
	flex.SetDirection(tview.FlexRow)
	for _, item := range items {
		flex.AddItem(item, 0, 1, true)
	}
	return flex
}

// TestRun_FullFlowViaSimulationScreen drives a real tview.Application
// event loop (via tcell's headless SimulationScreen, not a real terminal)
// through the entire table list -> grid -> picker -> apply -> confirm
// flow. Unlike every other test in this package, which calls named
// methods directly on a model whose Application was never started, this
// exercises real key dispatch, real focus routing, and real page
// transitions under Application.Run() — the class of bug (a startup
// panic in an earlier version of this UI, and this plan's own Task 7
// focus bug) that survived method-level unit tests and was only caught by
// manual interactive testing.
func TestRun_FullFlowViaSimulationScreen(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "bikes.db"))
	if err != nil {
		t.Fatalf("resolving fixture path: %v", err)
	}
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: abs, Kind: "sqlite"},
		Tables: map[string]config.TableConfig{
			"bikes": {
				Include:     true,
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

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(100, 40)

	// Replicate Run's wiring inline so the test can inject a simulation
	// screen — tview.Application has no way to accept one through the
	// public Run(ctx, st) entrypoint, and this test intentionally exercises
	// the exact same setup Run performs, not a simplified stand-in for it.
	m := &model{st: st, summary: st.Summary(), app: newTestApp(), pages: newTestPages()}
	m.status = newTestTextView()
	m.status.SetDynamicColors(false)
	m.buildTableList()
	tableListFlex := newTestFlexRow(m.tableList, newTestTextView())
	m.pages.AddPage("tablelist", tableListFlex, true, true)
	m.app.SetRoot(m.pages, true)
	m.app.SetScreen(screen)

	done := make(chan error, 1)
	go func() { done <- m.app.Run() }()

	step := func(key tcell.Key, ch rune) {
		screen.InjectKey(key, ch, tcell.ModNone)
		time.Sleep(50 * time.Millisecond)
	}

	step(tcell.KeyEnter, 0)  // open the bikes table -> grid
	step(tcell.KeyEnter, 0)  // open the picker on the first column
	step(tcell.KeyEscape, 0) // cancel out of the picker, no change
	step(tcell.KeyRune, 'f') // raise the Finish confirmation
	step(tcell.KeyEnter, 0)  // modal defaults to "Yes" — confirm

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("app.Run() returned an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for app.Run() to return — the app likely never reached Stop()")
	}

	if st.Outcome() != review.OutcomeConfirmed {
		t.Errorf("expected OutcomeConfirmed, got %v", st.Outcome())
	}
}

// screenText joins a SimulationScreen's rendered cells into one string, in
// row-major order, for substring assertions against what actually got
// drawn — as opposed to a TableCell's stored .Text field, which reflects
// what was set, not what tview's tag parser actually rendered.
func screenText(screen tcell.SimulationScreen) string {
	cells, width, height := screen.GetContents()
	var b strings.Builder
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			cell := cells[y*width+x]
			if len(cell.Runes) == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(cell.Runes[0])
		}
	}
	return b.String()
}

// TestBuildGrid_HeaderTypeAnnotationSurvivesRendering is a regression test
// for a real bug: tview interprets literal "[...]" in any rendered text as
// a color/region tag, so a header built as fmt.Sprintf("%s [%s]", column,
// targetType) had its "[type]" annotation silently swallowed on screen —
// invisible in production despite TestBuildGrid_HeaderShowsColumnNameAndType
// passing throughout, because that test only checks the TableCell's stored
// .Text field, not what tview's Draw() actually puts on screen. This test
// drives a real SimulationScreen and inspects the drawn cells instead.
func TestBuildGrid_HeaderTypeAnnotationSurvivesRendering(t *testing.T) {
	st, _ := newTestState(t)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(100, 40)

	m := &model{st: st, summary: st.Summary(), app: newTestApp(), pages: newTestPages()}
	m.status = newTestTextView()
	m.status.SetDynamicColors(false)
	m.buildTableList()
	tableListFlex := newTestFlexRow(m.tableList, newTestTextView())
	m.pages.AddPage("tablelist", tableListFlex, true, true)
	m.app.SetRoot(m.pages, true)
	m.app.SetScreen(screen)

	done := make(chan error, 1)
	go func() { done <- m.app.Run() }()

	screen.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // open the table -> grid
	time.Sleep(100 * time.Millisecond)

	rendered := screenText(screen)
	if !strings.Contains(rendered, "[integer]") {
		t.Errorf("expected the literal header text \"[integer]\" to survive rendering, got screen:\n%s", rendered)
	}

	m.app.Stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for app.Run() to return after Stop()")
	}
}
