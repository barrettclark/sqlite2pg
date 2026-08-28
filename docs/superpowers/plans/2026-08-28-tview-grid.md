# Consolidated tview grid Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `internal/tui` on `tview` as one consolidated grid screen per table (real sample data and every column's type decision together), replacing the current Bubble Tea implementation's four separate screens.

**Architecture:** A `tview.Pages`-based `tview.Application` with three pages — a table list, a per-table grid (`tview.Table` wrapped in a `tview.Flex` with a status line and footer), and a centered type-picker overlay (`tview.List`) — plus a `tview.Modal` for Finish/Cancel. Pure logic (value coercion, type filtering, table lookup) lives in framework-free functions, unit-tested directly; screen wiring uses named methods registered via `SetInputCapture`/`SetSelectedFunc`/`SetSelectionChangedFunc` so each can be unit-tested by calling it directly, without a running event loop.

**Tech Stack:** Go 1.26, `github.com/rivo/tview`, `github.com/gdamore/tcell/v2`.

**Spec:** `docs/superpowers/specs/2026-08-28-tview-grid-redesign.md`

## Global Constraints

- Commits are small and atomic — one logical change per commit, in the order tasks appear below. Every task's code must compile and its tests must pass (`go build ./...`, `go vet ./...`, `go test ./...`) before moving to the next task.
- `internal/review` (the state/decision core) is untouched — `State`, `ReviewSummary`, `TableView`, `ColumnView`, `TypeOptions`, `ApplyDecision`, `Finish`, `Cancel`, `Done`, `Outcome` all keep their exact current shape. Nothing in this plan modifies `internal/review`.
- `tui.Run(ctx context.Context, st *review.State) error` keeps its exact signature and blocking contract (`cmd/migrate/main.go` calls it exactly as `tui.Run(context.Background(), st)` and switches on `st.Outcome()` afterward) — no changes to `cmd/migrate/main.go` should be needed anywhere in this plan.
- `ApplyDecision` is always called with `Transform: ""` and `Rationale: "human override via TUI"` on a human override — never carrying over a stale transform.
- `validTypesForColumn` always includes the column's current target type in its result, even if that type fails the per-value validity check — the picker must never be empty and must never force the human off their column's current assignment.
- `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`, and `github.com/charmbracelet/lipgloss` are removed from `go.mod` entirely by the end of this plan — no transitive Bubble Tea dependencies should remain after `go mod tidy`.
- Manual interactive verification via `expect`/pty against real fixtures is a required step, not optional polish — automated unit tests alone already missed a real Bubble Tea startup panic earlier in this project. Every task that changes on-screen behavior includes an interactive check.
- If a task's exact tview/tcell API call doesn't compile against the resolved dependency version, adapt the minimum necessary to compile and pass while preserving the same behavior and test expectations, and note the deviation in your report — same latitude given for Bubble Tea dependency mismatches earlier in this project.

---

### Task 1: Dependencies, pure logic, and a minimal quittable app shell

**Files:**
- Delete: `internal/tui/model.go`, `internal/tui/model_test.go`, `internal/tui/preview.go`, `internal/tui/preview_test.go`, `internal/tui/run.go`, `internal/tui/run_test.go`, `internal/tui/testhelpers_test.go`
- Create: `internal/tui/logic.go`
- Create: `internal/tui/logic_test.go`
- Create: `internal/tui/app.go`
- Create: `internal/tui/app_test.go`
- Create: `internal/tui/tablelist.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `review.State.Summary() review.ReviewSummary`, `review.ReviewSummary{Tables []review.TableView; NeedsReviewCount, AutoApprovedCount int}`, `review.TableView{Name string; Columns []review.ColumnView; Rows [][]string; RowCount int}`, `review.ColumnView{Column, DeclaredType, TargetType, Source, Rationale string; Confidence float64; Reviewed, NeedsReview bool}`, `review.TypeOptions []string`.
- Produces: `findTable(summary review.ReviewSummary, name string) review.TableView`, `columnSampleValues(tv review.TableView, columnName string) []string`, `previewValueForType(value, targetType string) (display string, valid bool)`, `validTypesForColumn(values []string, currentType string) []string` — all consumed by later tasks. `type model struct { st *review.State; summary review.ReviewSummary; app *tview.Application; pages *tview.Pages; tableList *tview.List }` and `func Run(ctx context.Context, st *review.State) error` — later tasks add fields and methods to `model` and pages to `m.pages`, but do not change `Run`'s signature or these existing field names.

- [ ] **Step 1: Delete the old Bubble Tea implementation**

```bash
cd internal/tui
git rm model.go model_test.go preview.go preview_test.go run.go run_test.go testhelpers_test.go
cd ../..
```

- [ ] **Step 2: Add tview and tcell, remove the Bubble Tea dependencies**

```bash
go get github.com/rivo/tview@latest
go get github.com/gdamore/tcell/v2@latest
go mod tidy
```

After `go mod tidy`, confirm `go.mod` no longer lists `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`, or `github.com/charmbracelet/lipgloss` (as direct or indirect requires) — nothing in the tree imports them once Step 1's deletions land:

```bash
grep -i "charmbracelet" go.mod
```

Expected: no output.

- [ ] **Step 3: Write the failing tests for the pure logic functions**

Create `internal/tui/logic_test.go`:

```go
package tui

import (
	"testing"

	"sqlite2pg/internal/review"
)

func TestFindTable_ReturnsMatchByName(t *testing.T) {
	summary := review.ReviewSummary{Tables: []review.TableView{{Name: "bikes"}, {Name: "trips"}}}
	tv := findTable(summary, "trips")
	if tv.Name != "trips" {
		t.Fatalf("expected trips, got %q", tv.Name)
	}
}

func TestFindTable_ReturnsZeroValueWhenNotFound(t *testing.T) {
	tv := findTable(review.ReviewSummary{}, "missing")
	if tv.Name != "" {
		t.Fatalf("expected zero-value TableView, got %+v", tv)
	}
}

func TestColumnSampleValues_ExtractsOneColumnInRowOrder(t *testing.T) {
	tv := review.TableView{
		Columns: []review.ColumnView{{Column: "a"}, {Column: "b"}},
		Rows:    [][]string{{"1", "x"}, {"2", "y"}},
	}
	got := columnSampleValues(tv, "b")
	want := []string{"x", "y"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestColumnSampleValues_ReturnsNilForUnknownColumn(t *testing.T) {
	tv := review.TableView{Columns: []review.ColumnView{{Column: "a"}}, Rows: [][]string{{"1"}}}
	if got := columnSampleValues(tv, "missing"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestPreviewValueForType_CoercesNumericValuesRatherThanJustFlagging(t *testing.T) {
	cases := []struct {
		value, targetType, wantDisplay string
		wantValid                      bool
	}{
		{"3.7", "integer", "3", true},
		{"3", "double precision", "3.0", true},
		{"3.5", "double precision", "3.5", true},
		{"-2.9", "bigint", "-2", true},
		{"not-a-number", "integer", "not-a-number", false},
		{"NULL", "integer", "NULL", true},
	}
	for _, c := range cases {
		display, valid := previewValueForType(c.value, c.targetType)
		if display != c.wantDisplay || valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) = (%q, %v), want (%q, %v)",
				c.value, c.targetType, display, valid, c.wantDisplay, c.wantValid)
		}
	}
}

func TestPreviewValueForType_ValidityForNonNumericTypes(t *testing.T) {
	cases := []struct {
		value, targetType string
		wantValid         bool
	}{
		{"1", "boolean", true},
		{"true", "boolean", true},
		{"90b141b9-c39f-4a26", "boolean", false},
		{"2024-01-02", "date", true},
		{"90b141b9-c39f-4a26", "date", false},
		{"anything at all", "text", true},
		{"NULL", "date", true},
	}
	for _, c := range cases {
		_, valid := previewValueForType(c.value, c.targetType)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) valid = %v, want %v", c.value, c.targetType, valid, c.wantValid)
		}
	}
}

func TestValidTypesForColumn_FiltersOutTypesAnySampleFails(t *testing.T) {
	// Every value is a plain non-negative integer string, so the numeric
	// and text-like types validate; boolean/date/timestamptz don't, since
	// "12"/"34" aren't boolean-shaped or date-formatted.
	values := []string{"12", "34", "0"}
	got := validTypesForColumn(values, "integer")
	want := map[string]bool{
		"integer": true, "bigint": true, "smallint": true,
		"real": true, "double precision": true, "numeric": true,
		"text": true, "jsonb": true, "bytea": true,
		"boolean": false, "date": false, "timestamptz": false,
	}
	gotSet := map[string]bool{}
	for _, typ := range got {
		gotSet[typ] = true
	}
	for typ, wantPresent := range want {
		if gotSet[typ] != wantPresent {
			t.Errorf("validTypesForColumn(%v, \"integer\") contains %q = %v, want %v", values, typ, gotSet[typ], wantPresent)
		}
	}
}

func TestValidTypesForColumn_AlwaysIncludesCurrentTypeEvenIfInvalid(t *testing.T) {
	values := []string{"not-a-number-at-all"}
	got := validTypesForColumn(values, "integer")
	found := false
	for _, typ := range got {
		if typ == "integer" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected currentType %q always included, got %v", "integer", got)
	}
	for _, typ := range got {
		if typ != "integer" && typ != "text" && typ != "jsonb" && typ != "bytea" {
			t.Errorf("unexpected type %q included for a non-numeric, non-date-like string", typ)
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

```bash
go test ./internal/tui/... -v
```

Expected: FAIL to compile — `findTable`, `columnSampleValues`, `previewValueForType`, `validTypesForColumn` undefined (the package currently has no `.go` files at all after Step 1's deletions, so this also fails with "no Go files in internal/tui" until Step 5 below adds `logic.go`).

- [ ] **Step 5: Implement `internal/tui/logic.go`**

```go
// Package tui is the terminal UI a human uses to approve or override the
// profiler's column-type decisions before a load proceeds: one screen per
// table shows real sample data and every column's type decision together,
// so a proposed type is always judged next to the data it describes.
package tui

import (
	"strconv"
	"strings"
	"time"

	"sqlite2pg/internal/review"
)

// dateLayouts are the formats previewValueForType accepts for
// "date"/"timestamptz" — matching what a plain COPY (no transform) would
// need to parse, not every format Postgres itself understands.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// findTable returns name's TableView from summary, or a zero-value
// TableView if not found.
func findTable(summary review.ReviewSummary, name string) review.TableView {
	for _, t := range summary.Tables {
		if t.Name == name {
			return t
		}
	}
	return review.TableView{}
}

// columnSampleValues extracts one column's sample values (in row order)
// from tv's preview grid, for display and validity checking.
func columnSampleValues(tv review.TableView, columnName string) []string {
	idx := -1
	for i, c := range tv.Columns {
		if c.Column == columnName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}
	values := make([]string, 0, len(tv.Rows))
	for _, row := range tv.Rows {
		if idx < len(row) {
			values = append(values, row[idx])
		}
	}
	return values
}

// previewValueForType returns what value would look like under targetType:
// for numeric target types, the actual coerced number (truncated for
// integer types, decimal-formatted for floating-point types) rather than a
// bare valid/invalid flag, so a human can see e.g. what "3.7" becomes under
// "integer" or what "3" becomes under "double precision". For non-numeric
// target types it falls back to a validity check — whether the raw text
// would parse as that Postgres type with no transform applied — since
// there's no meaningful "conversion" to preview for e.g. a UUID string
// under "boolean". "NULL" (the preview grid's placeholder for a nil value)
// always displays as-is and is always valid, since NULL is valid for any
// nullable column.
func previewValueForType(value, targetType string) (display string, valid bool) {
	if value == "NULL" {
		return value, true
	}
	switch targetType {
	case "integer", "bigint", "smallint":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, false
		}
		return strconv.FormatInt(int64(f), 10), true
	case "real", "double precision", "numeric":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, false
		}
		formatted := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(formatted, ".") {
			formatted += ".0"
		}
		return formatted, true
	case "boolean":
		switch strings.ToLower(value) {
		case "0", "1", "true", "false", "t", "f":
			return value, true
		}
		return value, false
	case "date", "timestamptz":
		for _, layout := range dateLayouts {
			if _, err := time.Parse(layout, value); err == nil {
				return value, true
			}
		}
		return value, false
	default:
		// text, jsonb, bytea: any string is valid, displayed as-is.
		return value, true
	}
}

// validTypesForColumn returns the subset of review.TypeOptions that every
// one of values would load successfully as (per previewValueForType),
// always including currentType even if it fails that check — so the type
// picker is never empty and never forces a human off their column's
// current assignment.
func validTypesForColumn(values []string, currentType string) []string {
	var result []string
	for _, t := range review.TypeOptions {
		ok := true
		for _, v := range values {
			if _, valueValid := previewValueForType(v, t); !valueValid {
				ok = false
				break
			}
		}
		if ok || t == currentType {
			result = append(result, t)
		}
	}
	return result
}
```

- [ ] **Step 6: Run the logic tests to verify they pass**

```bash
go test ./internal/tui/... -run "TestFindTable|TestColumnSampleValues|TestPreviewValueForType|TestValidTypesForColumn" -v
```

Expected: PASS (this still won't build the whole package yet — `app.go`/`tablelist.go` are added next so `Run` exists again for `cmd/migrate/main.go` to compile against).

- [ ] **Step 7: Write the failing tests for the app shell's quit handling**

Create `internal/tui/app_test.go`:

```go
package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestTableListKeyCapture_QConsumesTheKey(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected q to be consumed (nil), got %v", got)
	}
}

func TestTableListKeyCapture_CtrlCConsumesTheKey(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected ctrl+c to be consumed (nil), got %v", got)
	}
}

func TestTableListKeyCapture_OtherKeysPassThrough(t *testing.T) {
	m := &model{app: tview.NewApplication()}
	event := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != event {
		t.Errorf("expected the down arrow to pass through unchanged, got %v", got)
	}
}
```

- [ ] **Step 8: Run the tests to verify they fail**

```bash
go test ./internal/tui/... -run TestTableListKeyCapture -v
```

Expected: FAIL to compile — `model`, `tableListKeyCapture` undefined.

- [ ] **Step 9: Implement `internal/tui/app.go`**

```go
package tui

import (
	"context"

	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// model holds everything every screen reads and mutates: the review
// session, its current summary, and the tview primitives wired together
// as pages. Unlike the previous Bubble Tea implementation's immutable
// Model value threaded through Update(), tview's widgets are long-lived
// and mutated in place, so screens are methods on *model that update
// these fields and widgets directly.
type model struct {
	st      *review.State
	summary review.ReviewSummary

	app   *tview.Application
	pages *tview.Pages

	tableList *tview.List
}

// Run drives the review TUI against st in the current terminal, blocking
// until the human finishes (review.OutcomeConfirmed) or cancels
// (review.OutcomeCancelled) — check st.Outcome() after Run returns nil to
// see which. Mirrors the exact blocking contract the previous Bubble Tea
// Run had: nothing touches Postgres until the human commits.
func Run(ctx context.Context, st *review.State) error {
	m := &model{
		st:      st,
		summary: st.Summary(),
		app:     tview.NewApplication(),
		pages:   tview.NewPages(),
	}

	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)
	m.app.SetRoot(m.pages, true)

	go func() {
		<-ctx.Done()
		m.app.Stop()
	}()

	return m.app.Run()
}
```

- [ ] **Step 10: Implement `internal/tui/tablelist.go`**

```go
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// buildTableList (re)builds m.tableList from m.summary: one row per table,
// showing its needs-review/auto-approved counts.
func (m *model) buildTableList() {
	list := m.tableList
	if list == nil {
		list = tview.NewList()
		list.ShowSecondaryText(true)
		list.SetBorder(true)
		list.SetInputCapture(m.tableListKeyCapture)
	} else {
		list.Clear()
	}
	for _, t := range m.summary.Tables {
		needs, auto := 0, 0
		for _, c := range t.Columns {
			if c.NeedsReview {
				needs++
			} else {
				auto++
			}
		}
		description := fmt.Sprintf("%d column(s) — %d need review, %d auto-approved", len(t.Columns), needs, auto)
		list.AddItem(t.Name, description, 0, nil)
	}
	title := fmt.Sprintf(" %d table(s) — %d column(s) need review, %d auto-approved ",
		len(m.summary.Tables), m.summary.NeedsReviewCount, m.summary.AutoApprovedCount)
	list.SetTitle(title)
	m.tableList = list
}

// tableListKeyCapture handles keys the table list itself doesn't know
// about. This is a temporary quit-only version: it stops the application
// without recording a Finish/Cancel outcome. Task 5 replaces this with a
// version that raises the Finish/Cancel confirmation modal instead.
func (m *model) tableListKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	switch {
	case event.Key() == tcell.KeyCtrlC:
		m.app.Stop()
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'q':
		m.app.Stop()
		return nil
	}
	return event
}
```

- [ ] **Step 11: Run the tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, all tests from Steps 3 and 7.

- [ ] **Step 12: Build and test the whole repo**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all succeed — `cmd/migrate/main.go` still compiles unchanged against the new `tui.Run`.

- [ ] **Step 13: Commit**

```bash
git add -A
git commit -m "Rebuild internal/tui on tview: pure logic and a minimal quittable shell"
```

---

### Task 2: Grid screen

**Files:**
- Create: `internal/tui/grid.go`
- Create: `internal/tui/grid_test.go`
- Modify: `internal/tui/app.go` (add fields to `model`)
- Modify: `internal/tui/tablelist.go` (wire table selection to the grid)

**Interfaces:**
- Consumes: `findTable`, `review.TableView`, `review.ColumnView` (Task 1). `m.pages`, `m.app`, `m.summary`, `m.st` (Task 1's `model`).
- Produces: `model.grid *tview.Table`, `model.status *tview.TextView`, `model.selectedTable string`, `model.buildGrid(tableName string)`, `model.onTableSelected(index int, name, secondaryText string, shortcut rune)`, `model.gridSelectionChanged(row, column int)`, `model.gridKeyCapture(event *tcell.EventKey) *tcell.EventKey`, `model.columnAt(column int) review.ColumnView` — all consumed by Task 3 (opening the picker on the selected column) and Task 4 (refreshing the grid after a decision).

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/grid_test.go`:

```go
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

func testModel() *model {
	m := &model{app: tview.NewApplication(), pages: tview.NewPages(), summary: testSummary()}
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
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/tui/... -run "TestOnTableSelected|TestBuildGrid|TestColumnAt|TestGridSelectionChanged|TestGridKeyCapture" -v
```

Expected: FAIL to compile — `model.grid`, `model.status`, `model.selectedTable`, `buildGrid`, `onTableSelected`, `columnAt`, `gridSelectionChanged`, `gridKeyCapture` undefined.

- [ ] **Step 3: Add the new fields to `model` in `internal/tui/app.go`**

```go
type model struct {
	st      *review.State
	summary review.ReviewSummary

	app   *tview.Application
	pages *tview.Pages

	tableList *tview.List

	selectedTable string
	grid          *tview.Table
	status        *tview.TextView
}
```

- [ ] **Step 4: Implement `internal/tui/grid.go`**

```go
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// buildGrid (re)builds m.grid from tableName's current TableView: a header
// row of "column [type]" cells (with a needs-review "!" or reviewed "✓"
// marker), and a data row per sample row. Column-only selection is
// enabled so moving left/right selects an entire column, matching the
// picker's per-column editing model.
func (m *model) buildGrid(tableName string) {
	tv := findTable(m.summary, tableName)

	grid := m.grid
	if grid == nil {
		grid = tview.NewTable()
		grid.SetSelectable(false, true)
		grid.SetFixed(1, 0)
		grid.SetBorder(true)
		grid.SetSelectionChangedFunc(m.gridSelectionChanged)
		grid.SetSelectedFunc(m.gridColumnSelected)
		grid.SetInputCapture(m.gridKeyCapture)
	} else {
		grid.Clear()
	}

	for col, c := range tv.Columns {
		marker := ""
		if c.Reviewed {
			marker = " ✓"
		} else if c.NeedsReview {
			marker = " !"
		}
		text := fmt.Sprintf("%s [%s]%s", c.Column, c.TargetType, marker)
		cell := tview.NewTableCell(text)
		cell.SetSelectable(true)
		grid.SetCell(0, col, cell)
	}
	for row, dataRow := range tv.Rows {
		for col, value := range dataRow {
			cell := tview.NewTableCell(value)
			cell.SetSelectable(false)
			grid.SetCell(row+1, col, cell)
		}
	}
	grid.Select(0, 0)
	grid.SetTitle(fmt.Sprintf(" %s — %d rows ", tv.Name, tv.RowCount))
	m.grid = grid

	if m.status == nil {
		m.status = tview.NewTextView()
		m.status.SetDynamicColors(false)
	}
	m.gridSelectionChanged(0, 0)
}

// columnAt returns the ColumnView for the grid's column-th column in
// m.selectedTable's current summary.
func (m *model) columnAt(column int) review.ColumnView {
	tv := findTable(m.summary, m.selectedTable)
	if column < 0 || column >= len(tv.Columns) {
		return review.ColumnView{}
	}
	return tv.Columns[column]
}

// gridSelectionChanged updates the status line with the newly selected
// column's confidence/source/rationale — this is the only place that
// information is shown now, replacing the old standalone column-detail
// screen.
func (m *model) gridSelectionChanged(row, column int) {
	col := m.columnAt(column)
	m.status.SetText(fmt.Sprintf("%s: confidence %.2f, source %s — %s",
		col.Column, col.Confidence, col.Source, col.Rationale))
}

// gridColumnSelected is called when Enter is pressed on the grid. Task 3
// wires this to open the type picker; for now it's a no-op placeholder
// that Task 3 replaces entirely.
func (m *model) gridColumnSelected(row, column int) {
}

// gridKeyCapture handles keys the grid itself doesn't know about: esc
// returns to the table list. Task 5 adds f/c/q for the Finish/Cancel
// confirmation to this same function.
func (m *model) gridKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEscape {
		m.pages.SwitchToPage("tablelist")
		m.app.SetFocus(m.tableList)
		return nil
	}
	return event
}
```

- [ ] **Step 5: Wire table selection in `internal/tui/tablelist.go`**

Add this method to `internal/tui/tablelist.go`, and register it in `buildTableList`'s `list == nil` branch:

```go
	if list == nil {
		list = tview.NewList()
		list.ShowSecondaryText(true)
		list.SetBorder(true)
		list.SetInputCapture(m.tableListKeyCapture)
		list.SetSelectedFunc(m.onTableSelected)
	} else {
```

(This replaces the `list == nil` branch's body from Task 1 — add the new `SetSelectedFunc` line alongside the three already there.)

```go
// onTableSelected builds tableName's grid (if not already built) and
// switches to it.
func (m *model) onTableSelected(index int, tableName, secondaryText string, shortcut rune) {
	m.selectedTable = tableName
	m.buildGrid(tableName)

	flex := tview.NewFlex()
	flex.SetDirection(tview.FlexRow)
	flex.AddItem(m.grid, 0, 1, true)
	flex.AddItem(m.status, 1, 0, false)

	if m.pages.HasPage("grid") {
		m.pages.RemovePage("grid")
	}
	m.pages.AddPage("grid", flex, true, true)
	m.pages.SwitchToPage("grid")
	m.app.SetFocus(m.grid)
}
```

Add the `"github.com/rivo/tview"` import already present in `tablelist.go` from Task 1 covers `tview.NewList`/`tview.NewFlex` — no new import needed.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, all tests from Task 1 and Task 2.

- [ ] **Step 7: Build and test the whole repo**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all succeed.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "Add the consolidated grid screen: real data and column types together"
```

---

### Task 3: Type picker overlay

**Files:**
- Create: `internal/tui/typepicker.go`
- Create: `internal/tui/typepicker_test.go`
- Modify: `internal/tui/grid.go` (`gridColumnSelected` now opens the picker instead of being a no-op)

**Interfaces:**
- Consumes: `validTypesForColumn`, `columnSampleValues`, `findTable` (Task 1). `model.grid`, `model.pages`, `model.app`, `model.selectedTable`, `model.columnAt` (Task 2).
- Produces: `model.picker *tview.List`, `model.pickerColumn string`, `model.openTypePicker(column string)`, `model.pickerKeyCapture(event *tcell.EventKey) *tcell.EventKey`, `model.onTypeSelected(index int, typeName, secondaryText string, shortcut rune)` — Task 4 replaces the body of `onTypeSelected` to actually apply the decision (this task's version only closes the picker, matching the "no-op placeholder Task N replaces" pattern already used for `gridColumnSelected` in Task 2).

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/typepicker_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/tui/... -run "TestOpenTypePicker|TestGridColumnSelected_OpensThePicker|TestPickerKeyCapture" -v
```

Expected: FAIL to compile — `model.picker`, `model.pickerColumn`, `openTypePicker`, `pickerKeyCapture` undefined.

- [ ] **Step 3: Add the new fields to `model` in `internal/tui/app.go`**

```go
type model struct {
	st      *review.State
	summary review.ReviewSummary

	app   *tview.Application
	pages *tview.Pages

	tableList *tview.List

	selectedTable string
	grid          *tview.Table
	status        *tview.TextView

	picker       *tview.List
	pickerColumn string
}
```

- [ ] **Step 4: Implement `internal/tui/typepicker.go`**

```go
package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"sqlite2pg/internal/review"
)

// openTypePicker opens a centered list of the types columnName's sample
// values actually validate as (per validTypesForColumn), with the
// column's current target type pre-selected.
func (m *model) openTypePicker(columnName string) {
	m.pickerColumn = columnName
	tv := findTable(m.summary, m.selectedTable)
	col := columnByName(tv, columnName)
	values := columnSampleValues(tv, columnName)
	types := validTypesForColumn(values, col.TargetType)

	list := tview.NewList()
	list.ShowSecondaryText(false)
	list.SetBorder(true)
	list.SetTitle(fmt.Sprintf(" Edit type: %s ", columnName))
	list.SetInputCapture(m.pickerKeyCapture)
	list.SetSelectedFunc(m.onTypeSelected)
	for i, t := range types {
		list.AddItem(t, "", 0, nil)
		if t == col.TargetType {
			list.SetCurrentItem(i)
		}
	}
	m.picker = list

	overlay := centered(list, 40, len(types)+2)
	if m.pages.HasPage("picker") {
		m.pages.RemovePage("picker")
	}
	m.pages.AddPage("picker", overlay, true, true)
	m.app.SetFocus(list)
}

// columnByName returns tv's ColumnView matching columnName, or a
// zero-value ColumnView if not found. Unlike Task 2's columnAt (which
// looks up by grid column index against m.selectedTable), this looks up
// by column name against an explicit TableView — the picker knows which
// column it's editing by name, not by the grid's current index.
func columnByName(tv review.TableView, columnName string) review.ColumnView {
	for _, c := range tv.Columns {
		if c.Column == columnName {
			return c
		}
	}
	return review.ColumnView{}
}

// centered wraps p in a Flex that centers it at width x height within the
// screen — the standard tview pattern for a modal-style overlay, since
// tview has no built-in "centered box" primitive.
func centered(p tview.Primitive, width, height int) tview.Primitive {
	row := tview.NewFlex()
	row.SetDirection(tview.FlexRow)
	row.AddItem(nil, 0, 1, false)
	row.AddItem(p, height, 1, true)
	row.AddItem(nil, 0, 1, false)

	col := tview.NewFlex()
	col.AddItem(nil, 0, 1, false)
	col.AddItem(row, width, 1, true)
	col.AddItem(nil, 0, 1, false)
	return col
}

// onTypeSelected closes the picker. Task 4 replaces this body to apply
// the selected type via State.ApplyDecision.
func (m *model) onTypeSelected(index int, typeName, secondaryText string, shortcut rune) {
	m.closePicker()
}

// closePicker removes the picker page and returns focus to the grid.
func (m *model) closePicker() {
	m.pages.RemovePage("picker")
	m.app.SetFocus(m.grid)
}

// pickerKeyCapture handles keys the picker list itself doesn't know
// about: esc closes it without applying anything.
func (m *model) pickerKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEscape {
		m.closePicker()
		return nil
	}
	return event
}
```

- [ ] **Step 5: Replace `gridColumnSelected`'s body in `internal/tui/grid.go`**

```go
// gridColumnSelected is called when Enter is pressed on the grid: it opens
// the type picker for the currently selected column.
func (m *model) gridColumnSelected(row, column int) {
	m.openTypePicker(m.columnAt(column).Column)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, all tests from Tasks 1-3.

- [ ] **Step 7: Build and test the whole repo**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all succeed.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "Add the type picker overlay, filtered to types the sample data validates as"
```

---

### Task 4: Wire the picker to ApplyDecision

**Files:**
- Modify: `internal/tui/typepicker.go` (`onTypeSelected`'s body)
- Modify: `internal/tui/typepicker_test.go`

**Interfaces:**
- Consumes: `review.State.ApplyDecision(table, column string, req review.DecisionRequest) error`, `review.DecisionRequest{TargetType, Transform, Rationale string}` (from `internal/review`, unchanged). `model.buildGrid`, `model.grid.Select` (Task 2).
- Produces: nothing new — this task only changes `onTypeSelected`'s behavior.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/typepicker_test.go`. This test needs a real `review.State` (not just the plain `review.ReviewSummary` fixture `testModel()` uses) since it must verify `ApplyDecision` actually persisted — add this test helper and test:

```go
func newTestState(t *testing.T) (*review.State, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := &config.MigrationConfig{
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
```

Add `"path/filepath"`, `"strings"`, `"github.com/rivo/tview"` (this test
calls `tview.NewApplication()`/`tview.NewPages()` directly, unlike Task 3's
tests in this same file, which only ever touch `*tview.List` values
returned by `testModel()`'s helpers), `"sqlite2pg/internal/config"`, and
`"sqlite2pg/internal/review"` to `internal/tui/typepicker_test.go`'s
import block.

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/tui/... -run TestOnTypeSelected_AppliesTheDecision -v
```

Expected: FAIL — the grid header still shows "boolean", `ApplyDecision` was never called (current `onTypeSelected` only closes the picker).

- [ ] **Step 3: Replace `onTypeSelected`'s body in `internal/tui/typepicker.go`**

```go
// onTypeSelected applies typeName as m.pickerColumn's new target type,
// refreshes the grid and status line, and closes the picker. Transform is
// always cleared and Rationale always set to the fixed string below — a
// stale transform from the prior heuristic guess is never implicitly
// carried over to a new target type.
func (m *model) onTypeSelected(index int, typeName, secondaryText string, shortcut rune) {
	err := m.st.ApplyDecision(m.selectedTable, m.pickerColumn, review.DecisionRequest{
		TargetType: typeName,
		Transform:  "",
		Rationale:  "human override via TUI",
	})
	if err != nil {
		m.status.SetText(fmt.Sprintf("error: %s", err))
		m.closePicker()
		return
	}

	_, selectedColumn := m.grid.GetSelection()
	m.summary = m.st.Summary()
	m.buildGrid(m.selectedTable)
	m.grid.Select(0, selectedColumn)
	m.gridSelectionChanged(0, selectedColumn)
	m.closePicker()
}
```

Add `"sqlite2pg/internal/review"` to `internal/tui/typepicker.go`'s import block.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, all tests from Tasks 1-4.

- [ ] **Step 5: Build and test the whole repo**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all succeed.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "Wire the type picker to State.ApplyDecision, refreshing the grid in place"
```

---

### Task 5: Finish/Cancel confirmation modal

**Files:**
- Create: `internal/tui/confirm.go`
- Create: `internal/tui/confirm_test.go`
- Modify: `internal/tui/tablelist.go` (`tableListKeyCapture` now opens the modal instead of quitting directly)
- Modify: `internal/tui/grid.go` (`gridKeyCapture` gains f/c/q handling)

**Interfaces:**
- Consumes: `review.State.Finish() error`, `review.State.Cancel()` (unchanged). `model.app`, `model.pages` (Task 1).
- Produces: `model.showConfirm(finish bool)`, consumed by both `tableListKeyCapture` and `gridKeyCapture`'s rewritten bodies.

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/confirm_test.go`:

```go
package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"sqlite2pg/internal/review"
)

func TestShowConfirm_FinishTrueCallsFinishOnYes(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	m.showConfirm(true)
	m.confirmDone(0, "Yes") // button index 0 is "Yes" per AddButtons order below

	if st.Outcome() != review.OutcomeConfirmed {
		t.Errorf("expected OutcomeConfirmed, got %v", st.Outcome())
	}
	select {
	case <-st.Done():
	default:
		t.Error("expected Done() to be closed")
	}
}

func TestShowConfirm_CancelTrueCallsCancelOnYes(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	m.showConfirm(false)
	m.confirmDone(0, "Yes")

	if st.Outcome() != review.OutcomeCancelled {
		t.Errorf("expected OutcomeCancelled, got %v", st.Outcome())
	}
}

func TestConfirmDone_NoClosesWithoutChangingOutcome(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	m.showConfirm(true)
	m.confirmDone(1, "No")

	if st.Outcome() != review.OutcomePending {
		t.Errorf("expected OutcomePending after declining, got %v", st.Outcome())
	}
	if m.pages.HasPage("confirm") {
		t.Error("expected the confirm page to be removed after declining")
	}
}

func TestTableListKeyCapture_FRaisesFinishConfirmation(t *testing.T) {
	st, _ := newTestState(t)
	m := &model{app: newTestApp(), pages: newTestPages(), st: st, summary: st.Summary()}
	m.buildTableList()
	m.pages.AddPage("tablelist", m.tableList, true, true)

	event := tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone)
	if got := m.tableListKeyCapture(event); got != nil {
		t.Errorf("expected f to be consumed (nil), got %v", got)
	}
	if !m.pages.HasPage("confirm") {
		t.Error("expected a confirm page to be shown")
	}
}
```

`newTestApp`/`newTestPages` are tiny helpers (add them to `internal/tui/grid_test.go` next to `testModel`, since several test files now need bare `*tview.Application`/`*tview.Pages` values without the full `testModel()` fixture's summary):

```go
func newTestApp() *tview.Application { return tview.NewApplication() }
func newTestPages() *tview.Pages     { return tview.NewPages() }
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/tui/... -run "TestShowConfirm|TestConfirmDone|TestTableListKeyCapture_FRaises" -v
```

Expected: FAIL to compile — `showConfirm`, `confirmDone` undefined.

- [ ] **Step 3: Implement `internal/tui/confirm.go`**

```go
package tui

import (
	"fmt"

	"github.com/rivo/tview"
)

// confirmState records which action ("finish" or "cancel") the
// currently-shown confirm modal is for, so confirmDone knows what "Yes"
// means. It's overwritten each time showConfirm is called; there's no
// zero-value/unset state to track since a modal is only ever shown right
// after showConfirm sets this.
type confirmState struct {
	finish bool
}

// showConfirm raises the Finish ("finish is true) or Cancel confirmation
// modal over whichever screen is currently active.
func (m *model) showConfirm(finish bool) {
	m.pendingConfirm = confirmState{finish: finish}

	verb := "Cancel"
	if finish {
		verb = "Confirm & Import"
	}
	modal := tview.NewModal()
	modal.SetText(fmt.Sprintf("%s?", verb))
	modal.AddButtons([]string{"Yes", "No"})
	modal.SetDoneFunc(m.confirmDone)

	if m.pages.HasPage("confirm") {
		m.pages.RemovePage("confirm")
	}
	m.pages.AddPage("confirm", modal, true, true)
	m.app.SetFocus(modal)
}

// confirmDone handles the modal's button press: "Yes" (button index 0)
// commits the pending action and stops the application; anything else
// closes the modal without side effects.
func (m *model) confirmDone(buttonIndex int, buttonLabel string) {
	if buttonLabel != "Yes" {
		m.pages.RemovePage("confirm")
		return
	}
	if m.pendingConfirm.finish {
		if err := m.st.Finish(); err != nil {
			m.status.SetText(fmt.Sprintf("error: %s", err))
			m.pages.RemovePage("confirm")
			return
		}
	} else {
		m.st.Cancel()
	}
	m.app.Stop()
}
```

Add `pendingConfirm confirmState` to `model` in `internal/tui/app.go`:

```go
type model struct {
	st      *review.State
	summary review.ReviewSummary

	app   *tview.Application
	pages *tview.Pages

	tableList *tview.List

	selectedTable string
	grid          *tview.Table
	status        *tview.TextView

	picker       *tview.List
	pickerColumn string

	pendingConfirm confirmState
}
```

- [ ] **Step 4: Replace `tableListKeyCapture`'s body in `internal/tui/tablelist.go`**

```go
// tableListKeyCapture handles keys the table list itself doesn't know
// about: f raises the Finish confirmation, c/q raise the Cancel
// confirmation.
func (m *model) tableListKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	switch {
	case event.Key() == tcell.KeyRune && event.Rune() == 'f':
		m.showConfirm(true)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'c':
		m.showConfirm(false)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'q':
		m.showConfirm(false)
		return nil
	}
	return event
}
```

(This replaces Task 1's version — the `KeyCtrlC` case is intentionally dropped: ctrl+c should still raise the confirmation like every other cancel path now that one exists, not bypass it, so `q` and ctrl+c both no longer immediately quit.)

- [ ] **Step 5: Add f/c/q handling to `gridKeyCapture`'s body in `internal/tui/grid.go`**

```go
// gridKeyCapture handles keys the grid itself doesn't know about: esc
// returns to the table list; f raises the Finish confirmation; c/q raise
// the Cancel confirmation.
func (m *model) gridKeyCapture(event *tcell.EventKey) *tcell.EventKey {
	switch {
	case event.Key() == tcell.KeyEscape:
		m.pages.SwitchToPage("tablelist")
		m.app.SetFocus(m.tableList)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'f':
		m.showConfirm(true)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'c':
		m.showConfirm(false)
		return nil
	case event.Key() == tcell.KeyRune && event.Rune() == 'q':
		m.showConfirm(false)
		return nil
	}
	return event
}
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, all tests from Tasks 1-5.

- [ ] **Step 7: Build and test the whole repo**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all succeed.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "Add the Finish/Cancel confirmation modal, replacing immediate quit"
```

---

### Task 6: Dependency cleanup and whole-repo verification

**Files:**
- Modify: `go.mod`, `go.sum` (if `go mod tidy` finds anything left over)

**Interfaces:**
- None — this task only verifies and tidies; it doesn't add or change any function signature.

- [ ] **Step 1: Confirm no Bubble Tea imports remain anywhere in the tree**

```bash
grep -rn "charmbracelet" --include="*.go" .
```

Expected: no output (Task 1 already removed every file that imported them; this is a final confirmation after four more tasks' worth of edits).

- [ ] **Step 2: Run `go mod tidy` and confirm it's a no-op or only removes stale entries**

```bash
go mod tidy
git status --short go.mod go.sum
```

Expected: either no changes, or only removals (no new additions — everything needed was already added in Task 1).

- [ ] **Step 3: Confirm `cmd/migrate/main.go` needed no changes**

```bash
git log --oneline -- cmd/migrate/main.go
```

Expected: no new commits touching this file across Tasks 1-5 — the plan's Global Constraints required `tui.Run`'s signature to stay exactly `Run(ctx context.Context, st *review.State) error`, and this confirms it held.

- [ ] **Step 4: Build, vet, and test the whole repo**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all succeed, 11 packages.

- [ ] **Step 5: Commit (only if Step 2 produced changes)**

```bash
git add go.mod go.sum
git commit -m "go mod tidy: drop stale Bubble Tea transitive dependencies"
```

If Step 2 produced no changes, skip this commit — there's nothing to commit.

---

### Task 7: README update and interactive smoke test

**Files:**
- Modify: `README.md`

**Interfaces:**
- None — documentation and manual verification only.

- [ ] **Step 1: Update `README.md`'s description of the review flow**

Replace:

```
This profiles the source, then opens an in-terminal review screen showing
every column's best-guess mapping: drill into a table, select a column, and
choose a target type from a list to override it. Press `v` from the table
list or column detail to see a scrollable grid of real sample rows for that
table. Press `f` then `y` to finish and import — generates the DDL and
streams every table into Postgres via COPY; press `c` then `y` to cancel —
nothing touches Postgres and the draft config is deleted.
```

with:

```
This profiles the source, then opens an in-terminal review screen: pick a
table, then see every column's real sample data and its type decision
together in one grid — select a column and press enter to change its type
from a list filtered to only the types the sampled data actually validates
as. Press `f` to finish and import — generates the DDL and streams every
table into Postgres via COPY; press `c` to cancel — nothing touches
Postgres and the draft config is deleted. Either raises a Yes/No
confirmation before doing anything irreversible.
```

Replace the file-tree entry:

```
  tui/                 terminal review UI (Bubble Tea)
```

with:

```
  tui/                 terminal review UI (tview): table list, per-table
                        data+type grid, filtered type picker
```

- [ ] **Step 2: Verify no stale references remain**

```bash
grep -n "Bubble Tea\|column detail\|drill into" README.md
```

Expected: no matches.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "Update README for the consolidated tview grid"
```

- [ ] **Step 4: Interactive smoke test against real fixtures**

Build the binary and drive it with `expect` over a real pty (the pattern used successfully throughout this project — pattern-matching each screen transition, not blind sleeps, and a real terminal size so `tview`'s layout has room to render):

```bash
go build -o /tmp/migrate-tview-smoke ./cmd/migrate
rm -f testdata/fixtures/bikes.db.migration.yaml testdata/fixtures/bikes.db.migration.yaml.unresolved_report.yaml
/tmp/migrate-tview-smoke profile testdata/fixtures/bikes.db
```

Write an `expect` script (adapt the exact pattern strings to whatever text `tview` actually renders — its `Modal`/`List`/`Table` styling differs from the hand-rolled Bubble Tea footer text this project's earlier smoke tests matched against):

```tcl
#!/usr/bin/expect -f
set timeout 8
set stty_init "rows 40 cols 130"
spawn /tmp/migrate-tview-smoke review testdata/fixtures/bikes.db.migration.yaml
expect -re "table\\(s\\)"
send "\r"
expect -re "bike_id"
send "\r"
expect -re "Edit type"
send "\r"
expect -re "bike_id"
send "\033"
expect -re "table\\(s\\)"
send "f"
expect -re "Confirm"
send "y"
expect eof
```

Run it, confirm exit code 0 and no panic, and confirm the resulting config has every column marked `reviewed: true`:

```bash
grep -c "reviewed: true" testdata/fixtures/bikes.db.migration.yaml
```

Clean up afterward: `rm testdata/fixtures/bikes.db.migration.yaml testdata/fixtures/bikes.db.migration.yaml.unresolved_report.yaml`.

Also test the type-picker's filtering interactively against a real
ambiguous column (e.g. `station_id`, a UUID-like TEXT column) — confirm
`integer`/`bigint`/`smallint`/`boolean`/`date`/`timestamptz` do **not**
appear in its picker list, per `validTypesForColumn`'s filtering.

No commit for this step — it's verification, not a code change. If the
smoke test reveals a bug, fix it as a new, separate commit (with its own
test) rather than amending a prior task's commit.
