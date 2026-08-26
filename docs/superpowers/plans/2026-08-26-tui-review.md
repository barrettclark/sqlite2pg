# TUI Review Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `sqlite2pg`'s browser-based review wizard with an in-terminal (Bubble Tea) TUI, and rename the package holding the shared review-session core from `wizard` to `review`.

**Architecture:** `internal/wizard` is renamed to `internal/review` and keeps only its UI-agnostic core (`State`, `ReviewSummary`, `BuildReviewSummary`, `DecisionRequest`, `TypeOptions`). A new `internal/tui` package implements a three-screen Bubble Tea `Model` (table list → column detail → type picker, plus an inline Finish/Cancel confirmation) driving that core. `cmd/migrate/main.go` swaps its `wizard.Listen`/`wizard.Run`/`openBrowser` calls for a single blocking `tui.Run(ctx, st) error`, and drops the now-meaningless `--port` flags. The web-specific files (`handlers.go`, `server.go`, `static.go`, `static/`) and their tests are deleted once the TUI is wired in and passing.

**Tech Stack:** Go 1.26, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles/list`, `github.com/charmbracelet/lipgloss`.

**Spec:** `docs/superpowers/specs/2026-08-26-tui-review-design.md`

## Global Constraints

- Commits are small and atomic — one logical change per commit, in the order tasks appear below. Never bundle unrelated changes into one commit.
- Every task's code must compile and its tests must pass (`go build ./...` and `go test ./...`) before moving to the next task.
- The web wizard is deleted outright, not kept behind a flag (spec non-goal).
- `internal/review.State.ApplyDecision` always leaves `Transform` blank on a human override — never carry over a stale transform from the prior heuristic guess (existing invariant, preserved unchanged).
- The review session's "nothing touches Postgres until confirmed" guarantee must hold: `tui.Run` only returns after the human explicitly finishes or cancels (or the process is killed), matching `wizard.Run`'s existing contract with `st.Outcome()`.

---

### Task 1: Rename `internal/wizard` to `internal/review`, move `DecisionRequest` into the core

**Files:**
- Rename (git mv): `internal/wizard/state.go` → `internal/review/state.go`
- Rename (git mv): `internal/wizard/review_model.go` → `internal/review/review_model.go`
- Rename (git mv): `internal/wizard/review_model_test.go` → `internal/review/review_model_test.go`
- Rename (git mv): `internal/wizard/samples.go` → `internal/review/samples.go`
- Rename (git mv): `internal/wizard/samples_test.go` → `internal/review/samples_test.go`
- Rename (git mv, unchanged content otherwise): `internal/wizard/handlers.go` stays for now (still needed by the web server until Task 7) but moves to `internal/review/handlers.go`
- Rename (git mv): `internal/wizard/server.go` → `internal/review/server.go`
- Rename (git mv): `internal/wizard/static.go` → `internal/review/static.go`
- Rename (git mv): `internal/wizard/static/` → `internal/review/static/`
- Rename (git mv): `internal/wizard/handlers_test.go` → `internal/review/handlers_test.go`
- Rename (git mv): `internal/wizard/server_test.go` → `internal/review/server_test.go`
- Rename (git mv): `internal/wizard/static_test.go` → `internal/review/static_test.go`
- Modify: `internal/review/handlers.go` (remove `DecisionRequest`, now defined in `state.go`)
- Modify: `internal/review/state.go` (add `DecisionRequest`)
- Modify: `cmd/migrate/main.go` (import path + all `wizard.` references)

**Interfaces:**
- Produces: package `review` (import path `sqlite2pg/internal/review`) exporting everything package `wizard` exported before: `State`, `NewState`, `ReviewSummary`, `TableView`, `ColumnView`, `GridData`, `TablePreview`, `BuildReviewSummary`, `Outcome`, `OutcomePending`/`OutcomeConfirmed`/`OutcomeCancelled`, `DecisionRequest`, `NewMux`, `Listen`, `Run`.

- [ ] **Step 1: Move every file from `internal/wizard/` to `internal/review/`**

```bash
mkdir -p internal/review
git mv internal/wizard/state.go internal/review/state.go
git mv internal/wizard/review_model.go internal/review/review_model.go
git mv internal/wizard/review_model_test.go internal/review/review_model_test.go
git mv internal/wizard/samples.go internal/review/samples.go
git mv internal/wizard/samples_test.go internal/review/samples_test.go
git mv internal/wizard/handlers.go internal/review/handlers.go
git mv internal/wizard/server.go internal/review/server.go
git mv internal/wizard/static.go internal/review/static.go
git mv internal/wizard/static internal/review/static
git mv internal/wizard/handlers_test.go internal/review/handlers_test.go
git mv internal/wizard/server_test.go internal/review/server_test.go
git mv internal/wizard/static_test.go internal/review/static_test.go
rmdir internal/wizard
```

- [ ] **Step 2: Change the package declaration in every moved `.go` file**

In each of the 9 `.go` files under `internal/review/` (all except `static/index.html` and `static/app.js`, which have no package declaration), change the first line from `package wizard` to `package review`. Also update `internal/review/review_model.go`'s doc comment, which currently reads:

```go
// Package wizard is the local, localhost-only web UI a human uses to
// approve or override the profiler's column-type decisions before a load
// proceeds.
package wizard
```

to:

```go
// Package review holds the review-session core shared by every review
// UI (formerly a browser-based wizard, now a terminal UI): the state
// machine that tracks a human's approve/override decisions on the
// profiler's column-type guesses before a load proceeds.
package review
```

- [ ] **Step 3: Move `DecisionRequest` from `handlers.go` into `state.go`**

It's a core domain type (the shape of one column decision), not an HTTP-specific one, and `state.go` is the file that survives Task 7's deletion of the web-specific files. In `internal/review/handlers.go`, remove:

```go
// DecisionRequest is the body of POST /api/columns/{table}/{column}/decision.
// Transform must be set explicitly alongside TargetType (empty means
// passthrough) — a stale transform from the prior heuristic guess (e.g.
// int_to_bool) is never implicitly carried over to a new target type.
type DecisionRequest struct {
	TargetType string `json:"target_type"`
	Transform  string `json:"transform"`
	Rationale  string `json:"rationale"`
}
```

In `internal/review/state.go`, add the same type (drop the `POST /api/...` reference from the doc comment since it's no longer HTTP-specific) directly above the `State` struct:

```go
// DecisionRequest is one column's reviewed decision. Transform must be set
// explicitly alongside TargetType (empty means passthrough) — a stale
// transform from the prior heuristic guess (e.g. int_to_bool) is never
// implicitly carried over to a new target type.
type DecisionRequest struct {
	TargetType string `json:"target_type"`
	Transform  string `json:"transform"`
	Rationale  string `json:"rationale"`
}

// Outcome is how a review session ended.
```

- [ ] **Step 4: Update `cmd/migrate/main.go`'s import and references**

Change the import block:

```go
	"sqlite2pg/internal/wizard"
```

to:

```go
	"sqlite2pg/internal/review"
```

Then replace every `wizard.` reference in `cmd/migrate/main.go` with `review.` — this affects `runRun` (`wizard.NewState`, `wizard.Listen`, `wizard.Run`, `wizard.OutcomeCancelled`, `wizard.OutcomeConfirmed`) and `runReview` (`wizard.NewState`, `wizard.Listen`, `wizard.Run`). Use a scoped find-and-replace, e.g.:

```bash
sed -i '' 's/\bwizard\./review./g' cmd/migrate/main.go
sed -i '' 's#"sqlite2pg/internal/wizard"#"sqlite2pg/internal/review"#' cmd/migrate/main.go
```

(On Linux, drop the `''` after `-i`.)

- [ ] **Step 5: Build and run the full test suite**

```bash
go build ./...
go test ./...
```

Expected: both succeed with no references to `internal/wizard` remaining (`grep -rn "internal/wizard" .` should return nothing outside `docs/superpowers/`).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "Rename internal/wizard to internal/review, move DecisionRequest into the core"
```

---

### Task 2: Add `review.TypeOptions`

**Files:**
- Modify: `internal/review/review_model.go` (add `TypeOptions` var)
- Test: `internal/review/review_model_test.go` (add test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `review.TypeOptions []string` — the ordered list of Postgres types a human can pick when overriding a column's target type. Consumed by `internal/tui` starting in Task 3.

- [ ] **Step 1: Write the failing test**

Add to `internal/review/review_model_test.go`:

```go
func TestTypeOptions_ContainsTheCommonPostgresTypes(t *testing.T) {
	want := []string{
		"text", "integer", "bigint", "smallint", "boolean",
		"double precision", "real", "numeric",
		"date", "timestamptz", "jsonb", "bytea",
	}
	if len(TypeOptions) != len(want) {
		t.Fatalf("expected %d type options, got %d: %v", len(want), len(TypeOptions), TypeOptions)
	}
	for i, w := range want {
		if TypeOptions[i] != w {
			t.Errorf("TypeOptions[%d] = %q, want %q", i, TypeOptions[i], w)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/review/... -run TestTypeOptions -v
```

Expected: FAIL — `undefined: TypeOptions`.

- [ ] **Step 3: Add `TypeOptions` to `internal/review/review_model.go`**

Add near the top of the file, above `ColumnView`:

```go
// TypeOptions is the ordered list of target Postgres types a human can pick
// from when overriding a column's decision. Kept as a plain, small, curated
// list rather than every Postgres type — these are the ones the profiler's
// heuristics and DDL generator actually understand.
var TypeOptions = []string{
	"text", "integer", "bigint", "smallint", "boolean",
	"double precision", "real", "numeric",
	"date", "timestamptz", "jsonb", "bytea",
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/review/... -run TestTypeOptions -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/review/review_model.go internal/review/review_model_test.go
git commit -m "Add review.TypeOptions, the shared list of pickable target types"
```

---

### Task 3: `internal/tui` scaffold — table list and column detail screens

**Files:**
- Create: `internal/tui/model.go`
- Create: `internal/tui/model_test.go`
- Create: `internal/tui/testhelpers_test.go`

**Interfaces:**
- Consumes: `review.State` (`Summary() review.ReviewSummary`), `review.ReviewSummary` (`Tables []review.TableView`, `NeedsReviewCount`, `AutoApprovedCount`), `review.TableView` (`Name string`, `Columns []review.ColumnView`), `review.ColumnView` (`Column`, `DeclaredType`, `TargetType`, `Confidence`, `Source`, `Rationale`, `NeedsReview`).
- Produces: `tui.New(st *review.State, width, height int) Model` and `Model` implementing `tea.Model` (`Init() tea.Cmd`, `Update(tea.Msg) (tea.Model, tea.Cmd)`, `View() string`). Later tasks add to this same `Model`.

- [ ] **Step 1: Add the Bubble Tea dependencies**

```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/charmbracelet/lipgloss@latest
go mod tidy
```

- [ ] **Step 2: Write the test helper**

Create `internal/tui/testhelpers_test.go`:

```go
package tui

import (
	"path/filepath"
	"testing"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/review"
)

// newTestState builds a review.State over a two-column, one-table config —
// one column below the review threshold, one above it — matching the
// fixture internal/review's own tests use, so behavior stays comparable.
func newTestState(t *testing.T) *review.State {
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
	return st
}
```

- [ ] **Step 3: Write the failing tests**

Create `internal/tui/model_test.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they fail**

```bash
go test ./internal/tui/... -v
```

Expected: FAIL to compile — `internal/tui` package doesn't exist yet.

- [ ] **Step 5: Implement `internal/tui/model.go`**

```go
// Package tui is the terminal UI a human uses to approve or override the
// profiler's column-type decisions before a load proceeds. It replaces the
// old browser-based wizard: the same review.State/review.ReviewSummary data,
// presented as an in-terminal flow (table list -> column detail -> type
// picker, with an inline Finish/Cancel confirmation) instead of a page in a
// browser.
package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"sqlite2pg/internal/review"
)

type screen int

const (
	screenTableList screen = iota
	screenColumnDetail
)

// footerLines is how much vertical space the key-hint footer takes, so the
// embedded list.Model is sized to leave room for it.
const footerLines = 2

// Model is the Bubble Tea model driving the review TUI.
type Model struct {
	st      *review.State
	summary review.ReviewSummary

	screen screen

	tableList  list.Model
	columnList list.Model

	selectedTable string

	width, height int

	err error
}

// New builds the initial Model for st, sized to width x height (the
// terminal's current dimensions; Update resizes it on tea.WindowSizeMsg).
func New(st *review.State, width, height int) Model {
	m := Model{st: st, summary: st.Summary(), width: width, height: height}
	m.tableList = newTableList(m.summary, width, height-footerLines)
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tableList.SetSize(msg.Width, msg.Height-footerLines)
		m.columnList.SetSize(msg.Width, msg.Height-footerLines)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenTableList:
		return m.handleTableListKey(msg)
	case screenColumnDetail:
		return m.handleColumnDetailKey(msg)
	}
	return m, nil
}

func (m Model) handleTableListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if item, ok := m.tableList.SelectedItem().(tableItem); ok {
			m.selectedTable = item.name
			tv := findTable(m.summary, item.name)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
			m.screen = screenColumnDetail
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.tableList, cmd = m.tableList.Update(msg)
	return m, cmd
}

func (m Model) handleColumnDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.screen = screenTableList
		return m, nil
	}
	var cmd tea.Cmd
	m.columnList, cmd = m.columnList.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	switch m.screen {
	case screenColumnDetail:
		return m.columnList.View() + "\nesc: back to tables\n"
	default:
		return m.tableList.View() + "\nenter: open table\n"
	}
}

func findTable(summary review.ReviewSummary, name string) review.TableView {
	for _, t := range summary.Tables {
		if t.Name == name {
			return t
		}
	}
	return review.TableView{}
}

// --- list items --------------------------------------------------------

type tableItem struct {
	name         string
	needsReview  int
	autoApproved int
	total        int
}

func (i tableItem) Title() string { return i.name }
func (i tableItem) Description() string {
	return fmt.Sprintf("%d column(s) — %d need review, %d auto-approved", i.total, i.needsReview, i.autoApproved)
}
func (i tableItem) FilterValue() string { return i.name }

func newTableList(summary review.ReviewSummary, width, height int) list.Model {
	items := make([]list.Item, 0, len(summary.Tables))
	for _, t := range summary.Tables {
		needs, auto := 0, 0
		for _, c := range t.Columns {
			if c.NeedsReview {
				needs++
			} else {
				auto++
			}
		}
		items = append(items, tableItem{name: t.Name, needsReview: needs, autoApproved: auto, total: len(t.Columns)})
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = fmt.Sprintf("%d table(s) — %d column(s) need review, %d auto-approved", len(summary.Tables), summary.NeedsReviewCount, summary.AutoApprovedCount)
	return l
}

type columnItem struct {
	col review.ColumnView
}

func (i columnItem) Title() string {
	marker := "  "
	if i.col.NeedsReview {
		marker = "! "
	}
	return fmt.Sprintf("%s%s: %s -> %s", marker, i.col.Column, i.col.DeclaredType, i.col.TargetType)
}
func (i columnItem) Description() string {
	return fmt.Sprintf("confidence %.2f, source %s — %s", i.col.Confidence, i.col.Source, i.col.Rationale)
}
func (i columnItem) FilterValue() string { return i.col.Column }

func newColumnList(tv review.TableView, width, height int) list.Model {
	items := make([]list.Item, 0, len(tv.Columns))
	for _, c := range tv.Columns {
		items = append(items, columnItem{col: c})
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = tv.Name
	return l
}
```

- [ ] **Step 6: Run tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/tui/
git commit -m "Add internal/tui: table list and column detail screens"
```

---

### Task 4: Type picker screen, wired to `State.ApplyDecision`

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `review.TypeOptions []string` (Task 2), `review.State.ApplyDecision(table, column string, req review.DecisionRequest) error`.
- Produces: adds `screenTypePicker` to the `Model`'s screen state machine; no new exported API.

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/model_test.go`:

```go
func TestModel_EnterOnColumnOpensTypePicker(t *testing.T) {
	m := New(newTestState(t), 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker for bike_id (first column)
	m = updated.(Model)

	if m.screen != screenTypePicker {
		t.Fatalf("expected screenTypePicker, got %v", m.screen)
	}
	if m.selectedColumn != "bike_id" {
		t.Fatalf("expected selectedColumn %q, got %q", "bike_id", m.selectedColumn)
	}
}

func TestModel_PickerEscCancelsWithoutChangingAnything(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	if m.screen != screenColumnDetail {
		t.Fatalf("expected screenColumnDetail, got %v", m.screen)
	}
	if st.Summary().Tables[0].Columns[0].Source != "heuristic:default_passthrough" {
		t.Fatalf("expected source unchanged by esc, got %q", st.Summary().Tables[0].Columns[0].Source)
	}
}

func TestModel_PickerEnterCommitsDecisionAndReturnsToColumnDetail(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into bikes
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open picker for bike_id (currently "integer", TypeOptions[1])
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // move to TypeOptions[2] ("bigint")
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit
	m = updated.(Model)

	if m.screen != screenColumnDetail {
		t.Fatalf("expected screenColumnDetail after commit, got %v", m.screen)
	}
	col := st.Summary().Tables[0].Columns[0]
	if col.Column != "bike_id" {
		t.Fatalf("expected bike_id in position 0, got %q", col.Column)
	}
	if col.TargetType != "bigint" {
		t.Fatalf("expected TargetType %q, got %q", "bigint", col.TargetType)
	}
	if col.Source != "human_override" {
		t.Fatalf("expected source %q, got %q", "human_override", col.Source)
	}
	if col.Transform != "" {
		t.Fatalf("expected Transform cleared on override, got %q", col.Transform)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tui/... -run TestModel_.*Picker -v
```

Expected: FAIL to compile — `screenTypePicker` / `selectedColumn` undefined.

- [ ] **Step 3: Extend `internal/tui/model.go`**

Add `screenTypePicker` to the `screen` enum:

```go
const (
	screenTableList screen = iota
	screenColumnDetail
	screenTypePicker
)
```

Add `typeList list.Model` and `selectedColumn string` fields to `Model`:

```go
type Model struct {
	st      *review.State
	summary review.ReviewSummary

	screen screen

	tableList  list.Model
	columnList list.Model
	typeList   list.Model

	selectedTable  string
	selectedColumn string

	width, height int

	err error
}
```

Update `handleColumnDetailKey` to open the picker on `enter` instead of forwarding it to the list:

```go
func (m Model) handleColumnDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.screen = screenTableList
		return m, nil
	case "enter":
		if item, ok := m.columnList.SelectedItem().(columnItem); ok {
			m.selectedColumn = item.col.Column
			m.typeList = newTypeList(item.col.TargetType, m.width, m.height-footerLines)
			m.screen = screenTypePicker
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.columnList, cmd = m.columnList.Update(msg)
	return m, cmd
}
```

Add `handleKey`'s dispatch case and the new handler:

```go
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.screen {
	case screenTableList:
		return m.handleTableListKey(msg)
	case screenColumnDetail:
		return m.handleColumnDetailKey(msg)
	case screenTypePicker:
		return m.handleTypePickerKey(msg)
	}
	return m, nil
}

func (m Model) handleTypePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenColumnDetail
		return m, nil
	case "enter":
		if item, ok := m.typeList.SelectedItem().(typeItem); ok {
			if err := m.st.ApplyDecision(m.selectedTable, m.selectedColumn, review.DecisionRequest{
				TargetType: string(item),
				Transform:  "",
			}); err != nil {
				m.err = err
				return m, nil
			}
			m.summary = m.st.Summary()
			tv := findTable(m.summary, m.selectedTable)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
		}
		m.screen = screenColumnDetail
		return m, nil
	}
	var cmd tea.Cmd
	m.typeList, cmd = m.typeList.Update(msg)
	return m, cmd
}
```

Update `View()` to render the picker:

```go
func (m Model) View() string {
	switch m.screen {
	case screenColumnDetail:
		return m.columnList.View() + "\nesc: back to tables  enter: change type\n"
	case screenTypePicker:
		return m.typeList.View() + "\nenter: select  esc: cancel\n"
	default:
		return m.tableList.View() + "\nenter: open table\n"
	}
}
```

Add the `typeItem` type and `newTypeList` constructor next to the other list items:

```go
type typeItem string

func (i typeItem) Title() string       { return string(i) }
func (i typeItem) Description() string { return "" }
func (i typeItem) FilterValue() string { return string(i) }

func newTypeList(current string, width, height int) list.Model {
	items := make([]list.Item, len(review.TypeOptions))
	selected := 0
	for idx, t := range review.TypeOptions {
		items[idx] = typeItem(t)
		if t == current {
			selected = idx
		}
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = "select target type"
	l.Select(selected)
	return l
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, including the three new picker tests and the Task 3 tests still passing.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "Add type picker screen, wired to review.State.ApplyDecision"
```

---

### Task 5: Finish/Cancel confirmation and `tui.Run` entrypoint

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`
- Create: `internal/tui/run.go`
- Create: `internal/tui/run_test.go` (compile-only smoke check — see Step 6)

**Interfaces:**
- Consumes: `review.State.Finish() error`, `review.State.Cancel()`, `review.State.Done() <-chan struct{}`, `review.State.Outcome() review.Outcome`.
- Produces: `tui.Run(ctx context.Context, st *review.State) error` — blocks until the human finishes or cancels (or the program otherwise exits), same calling contract `cmd/migrate/main.go` used against `wizard.Run`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/tui/model_test.go`:

```go
func TestModel_FinishConfirmationRequiresY(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	if !m.confirming || m.confirmAction != confirmFinish {
		t.Fatalf("expected finish confirmation pending, got confirming=%v action=%v", m.confirming, m.confirmAction)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.Quit, got %T", cmd())
	}
	select {
	case <-st.Done():
	default:
		t.Fatal("expected State.Done() to be closed")
	}
	if st.Outcome() != review.OutcomeConfirmed {
		t.Fatalf("expected OutcomeConfirmed, got %v", st.Outcome())
	}
}

func TestModel_CancelConfirmationRequiresY(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.Quit, got %T", cmd())
	}
	if st.Outcome() != review.OutcomeCancelled {
		t.Fatalf("expected OutcomeCancelled, got %v", st.Outcome())
	}
}

func TestModel_DecliningConfirmationReturnsToTableList(t *testing.T) {
	st := newTestState(t)
	m := New(st, 80, 24)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = updated.(Model)

	if m.confirming {
		t.Fatal("expected confirming to be cleared")
	}
	if m.screen != screenTableList {
		t.Fatalf("expected screenTableList, got %v", m.screen)
	}
	select {
	case <-st.Done():
		t.Fatal("expected State.Done() to still be open")
	default:
	}
}
```

Add the `review` import to `internal/tui/model_test.go`'s import block: `"sqlite2pg/internal/review"`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tui/... -run "Finish|Cancel|Declining" -v
```

Expected: FAIL to compile — `confirming`, `confirmAction`, `confirmFinish` undefined.

- [ ] **Step 3: Extend `internal/tui/model.go`**

Add a `confirmAction` type and constants near the `screen` type:

```go
type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmFinish
	confirmCancel
)
```

Add `confirming bool` and `confirmAction confirmAction` fields to `Model`:

```go
type Model struct {
	st      *review.State
	summary review.ReviewSummary

	screen screen

	tableList  list.Model
	columnList list.Model
	typeList   list.Model

	selectedTable  string
	selectedColumn string

	confirming    bool
	confirmAction confirmAction

	width, height int

	err error
}
```

Update `handleTableListKey` to intercept `f`/`c`/confirmation keys before falling through to list navigation:

```go
func (m Model) handleTableListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirming {
		return m.handleConfirmKey(msg)
	}
	switch msg.String() {
	case "f":
		m.confirming = true
		m.confirmAction = confirmFinish
		return m, nil
	case "c":
		m.confirming = true
		m.confirmAction = confirmCancel
		return m, nil
	case "enter":
		if item, ok := m.tableList.SelectedItem().(tableItem); ok {
			m.selectedTable = item.name
			tv := findTable(m.summary, item.name)
			m.columnList = newColumnList(tv, m.width, m.height-footerLines)
			m.screen = screenColumnDetail
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.tableList, cmd = m.tableList.Update(msg)
	return m, cmd
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "y" {
		m.confirming = false
		return m, nil
	}
	if m.confirmAction == confirmFinish {
		if err := m.st.Finish(); err != nil {
			m.err = err
			m.confirming = false
			return m, nil
		}
	} else {
		m.st.Cancel()
	}
	return m, tea.Quit
}
```

Update `View()` to render the confirmation prompt over the table list:

```go
func (m Model) View() string {
	switch m.screen {
	case screenColumnDetail:
		return m.columnList.View() + "\nesc: back to tables  enter: change type\n"
	case screenTypePicker:
		return m.typeList.View() + "\nenter: select  esc: cancel\n"
	default:
		body := m.tableList.View() + "\nenter: open table  f: finish  c: cancel\n"
		if m.confirming {
			verb := "Confirm & Import"
			if m.confirmAction == confirmCancel {
				verb = "Cancel"
			}
			body += fmt.Sprintf("%s? y/n\n", verb)
		}
		return body
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/tui/... -v
```

Expected: PASS, all tests from Tasks 3-5.

- [ ] **Step 5: Implement `internal/tui/run.go`**

```go
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"sqlite2pg/internal/review"
)

// defaultWidth and defaultHeight size the Model before Bubble Tea's first
// tea.WindowSizeMsg arrives with the terminal's actual dimensions.
const (
	defaultWidth  = 80
	defaultHeight = 24
)

// Run drives the review TUI against st in the current terminal, blocking
// until the human finishes (review.OutcomeConfirmed) or cancels
// (review.OutcomeCancelled) — check st.Outcome() after Run returns nil to
// see which. It mirrors the blocking contract review.Run (the old HTTP
// server entrypoint) had: nothing touches Postgres until the human commits.
func Run(ctx context.Context, st *review.State) error {
	m := New(st, defaultWidth, defaultHeight)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
```

- [ ] **Step 6: Add a compile-only smoke test for `Run`'s signature**

Create `internal/tui/run_test.go`:

```go
package tui

import (
	"context"

	"sqlite2pg/internal/review"
)

// runSignature is a compile-time check that Run's signature stays
// Run(ctx, st) error — cmd/migrate/main.go depends on it exactly. Running
// Run itself requires a real terminal, so it's exercised via the manual
// smoke test in the plan instead of an automated test.
var _ func(context.Context, *review.State) error = Run
```

- [ ] **Step 7: Run tests to verify everything still passes**

```bash
go test ./internal/tui/... -v
go build ./...
```

Expected: PASS / success.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/
git commit -m "Add Finish/Cancel confirmation and the tui.Run entrypoint"
```

---

### Task 6: Wire `cmd/migrate/main.go` to the TUI, drop `--port`

**Files:**
- Modify: `cmd/migrate/main.go`

**Interfaces:**
- Consumes: `tui.Run(ctx context.Context, st *review.State) error` (Task 5), `review.NewState(path string, threshold float64) (*review.State, error)` (already renamed in Task 1).

- [ ] **Step 1: Update imports**

Replace:

```go
	"os/exec"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/pipeline"
	_ "sqlite2pg/internal/profiler/heuristics"
	"sqlite2pg/internal/resolver"
	"sqlite2pg/internal/review"
```

with:

```go
	"time"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/pipeline"
	_ "sqlite2pg/internal/profiler/heuristics"
	"sqlite2pg/internal/resolver"
	"sqlite2pg/internal/review"
	"sqlite2pg/internal/tui"
```

(`os/exec` and `runtime` are dropped — their only use was `openBrowser`, deleted in Step 4 below.)

- [ ] **Step 2: Update `runRun`**

Replace:

```go
	pgURL := fs.String("pg", "", "Postgres server URL, e.g. postgres://user@localhost:5432/?sslmode=disable (required; no database name — a fresh one is created per run)")
	sampleSize := fs.Int("sample-size", 500, "rows to sample per column")
	threshold := fs.Float64("threshold", 0.9, "confidence below which a column is highlighted as needing review")
	port := fs.Int("port", 0, "port to bind for the review wizard (0 = pick a free port)")
	keepConfig := fs.Bool("keep-config", false, "keep the generated <source>.migration.yaml after the run instead of deleting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate run --pg url [--sample-size N] [--threshold F] [--port P] <source.db>")
	}
```

with:

```go
	pgURL := fs.String("pg", "", "Postgres server URL, e.g. postgres://user@localhost:5432/?sslmode=disable (required; no database name — a fresh one is created per run)")
	sampleSize := fs.Int("sample-size", 500, "rows to sample per column")
	threshold := fs.Float64("threshold", 0.9, "confidence below which a column is highlighted as needing review")
	keepConfig := fs.Bool("keep-config", false, "keep the generated <source>.migration.yaml after the run instead of deleting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate run --pg url [--sample-size N] [--threshold F] <source.db>")
	}
```

Then replace:

```go
	st, err := review.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	ln, err := review.Listen(*port)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	fmt.Printf("review at %s — Confirm & Import to load, Cancel to abort\n", url)
	openBrowser(url)

	if err := review.Run(context.Background(), ln, st); err != nil {
		return err
	}
```

with:

```go
	st, err := review.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	fmt.Println("opening review — f: finish & import, c: cancel")
	if err := tui.Run(context.Background(), st); err != nil {
		return err
	}
```

- [ ] **Step 3: Update `runReview`**

Replace:

```go
func runReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	threshold := fs.Float64("threshold", 0.9, "confidence below which a column is highlighted as needing review")
	port := fs.Int("port", 0, "port to bind (0 = pick a free port)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate review [--threshold F] <config.migration.yaml>")
	}
	configPath := fs.Arg(0)

	st, err := review.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	ln, err := review.Listen(*port)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	fmt.Printf("review server listening at %s (waiting for Finish Review)\n", url)
	openBrowser(url)

	return review.Run(context.Background(), ln, st)
}
```

with:

```go
func runReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	threshold := fs.Float64("threshold", 0.9, "confidence below which a column is highlighted as needing review")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate review [--threshold F] <config.migration.yaml>")
	}
	configPath := fs.Arg(0)

	st, err := review.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	return tui.Run(context.Background(), st)
}
```

- [ ] **Step 4: Delete the `openBrowser` function**

Remove:

```go
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
```

- [ ] **Step 5: Build and run the full test suite**

```bash
go build ./...
go test ./...
```

Expected: both succeed. `grep -n "port\|openBrowser" cmd/migrate/main.go` should return nothing.

- [ ] **Step 6: Manually verify the CLI still runs end-to-end**

```bash
go run ./cmd/migrate profile testdata/fixtures/bikes.db
go run ./cmd/migrate review testdata/fixtures/bikes.db.migration.yaml
```

Expected: the second command opens the TUI table list in your terminal (not a browser). Navigate into the table, open the picker on a needs-review column, press `esc`, then `f`, then `y` to finish; the command should exit 0. Clean up the generated file afterward: `rm testdata/fixtures/bikes.db.migration.yaml`.

- [ ] **Step 7: Commit**

```bash
git add cmd/migrate/main.go
git commit -m "Wire migrate run/review to the TUI, drop the --port flag"
```

---

### Task 7: Delete the web-specific files

**Files:**
- Delete: `internal/review/handlers.go`, `internal/review/handlers_test.go`
- Delete: `internal/review/server.go`, `internal/review/server_test.go`
- Delete: `internal/review/static.go`, `internal/review/static_test.go`, `internal/review/static/`

**Interfaces:**
- Consumes: nothing (this task only removes code).
- Produces: nothing new — `internal/review` after this task exports only `State`, `NewState`, `ReviewSummary`, `TableView`, `ColumnView`, `GridData`, `TablePreview`, `BuildReviewSummary`, `TypeOptions`, `DecisionRequest`, `Outcome` and its constants.

- [ ] **Step 1: Confirm nothing outside `internal/review` still references the doomed files' exports**

```bash
grep -rn "review\.NewMux\|review\.Listen\|review\.Run\b" --include="*.go" .
```

Expected: no matches (Task 6 already removed the only call sites).

- [ ] **Step 2: Delete the files**

```bash
git rm internal/review/handlers.go internal/review/handlers_test.go
git rm internal/review/server.go internal/review/server_test.go
git rm internal/review/static.go internal/review/static_test.go
git rm -r internal/review/static
```

- [ ] **Step 3: Build and run the full test suite**

```bash
go build ./...
go test ./...
```

Expected: both succeed.

- [ ] **Step 4: Commit**

```bash
git commit -m "Delete the web review wizard's HTTP server, handlers, and static assets"
```

---

### Task 8: Manual smoke test and README update

**Files:**
- Modify: `README.md`

**Interfaces:**
- None — documentation only.

- [ ] **Step 1: Manual smoke test against two more fixtures**

```bash
go run ./cmd/migrate profile testdata/fixtures/chinook.db
go run ./cmd/migrate review testdata/fixtures/chinook.db.migration.yaml
```

Navigate every screen at least once (table list → column detail → type picker → esc back → f → y finish). Confirm the process exits 0 and `chinook.db.migration.yaml`'s `reviewed: true` fields got set. Then:

```bash
go run ./cmd/migrate profile testdata/fixtures/sample-types.sqlite
go run ./cmd/migrate review testdata/fixtures/sample-types.sqlite.migration.yaml
```

This fixture is chosen because its name suggests varied/ambiguous types, exercising the picker against more than one target type. Confirm the `c` (cancel) path too: run review again, press `c`, then `y` — confirm it exits 0 and prints something indicating cancellation happened (check `cmd/migrate/main.go`'s `runRun`/`runReview` output around `OutcomeCancelled` if unsure what to expect for `runReview`, since `runReview` has no cancel-specific message — that's fine, only `runRun` prints one).

Clean up: `rm testdata/fixtures/chinook.db.migration.yaml testdata/fixtures/sample-types.sqlite.migration.yaml`.

- [ ] **Step 2: Update `README.md`**

Replace (line 5):

```
interactive web wizard for reviewing ambiguous type decisions.
```

with:

```
interactive terminal wizard for reviewing ambiguous type decisions.
```

Replace (lines 38-42):

```
This profiles the source, opens a browser at the review wizard showing every
column's best-guess mapping (editable inline), and waits. Click **Confirm &
Import** to generate the DDL and stream every table into Postgres via COPY;
click **Cancel** to abort — nothing touches Postgres and the draft config is
deleted. The wizard binds to `127.0.0.1` only.
```

with:

```
This profiles the source, then opens an in-terminal review screen showing
every column's best-guess mapping (editable inline), and waits. Press `f`
then `y` to finish and import — generates the DDL and streams every table
into Postgres via COPY; press `c` then `y` to cancel — nothing touches
Postgres and the draft config is deleted.
```

Replace (line 49):

```
migrate review   <config.yaml>  # open a local web wizard to approve/override ambiguous columns
```

with:

```
migrate review   <config.yaml>  # open the terminal review UI to approve/override ambiguous columns
```

Replace (lines 53-54):

```
- **`run`** is `profile` + `review` + `load` collapsed into one command, with
  a Confirm/Cancel gate in the wizard controlling whether `load` runs at all.
```

with:

```
- **`run`** is `profile` + `review` + `load` collapsed into one command, with
  a Confirm/Cancel gate in the review screen controlling whether `load` runs
  at all.
```

Replace (lines 63-68):

```
- **`review`** starts an HTTP server bound to `127.0.0.1` only, opens your
  browser, and blocks until you click "Confirm & Import" or "Cancel". Every
  approve/override click writes straight through to the config file on disk
  — closing the tab never loses progress. (For standalone `review`, "Confirm
  & Import" just finishes the review and unblocks the CLI — the actual load
  is a separate `migrate load` step; only `run` loads immediately after.)
```

with:

```
- **`review`** opens the terminal review UI directly in your current
  session and blocks until you finish or cancel. Every approve/override
  commits straight through to the config file on disk — quitting the
  terminal never loses progress made before that point. (For standalone
  `review`, finishing just ends the review and unblocks the CLI — the
  actual load is a separate `migrate load` step; only `run` loads
  immediately after.)
```

Replace (line 137):

```
  wizard/               localhost-only web review UI (embedded vanilla JS frontend)
```

with:

```
  review/               review-session core (state machine, decisions)
  tui/                  terminal review UI (Bubble Tea)
```

- [ ] **Step 3: Verify no stale references remain**

```bash
grep -n "wizard\|browser\|--port" README.md
```

Expected: no matches.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "Update README for the terminal review UI"
```
