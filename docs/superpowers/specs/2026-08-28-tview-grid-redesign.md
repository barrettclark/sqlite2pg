# Consolidated tview grid — design

## Context

`sqlite2pg`'s terminal review UI (`internal/tui`, built on Bubble Tea) was
built incrementally across several sessions: a table list, a separate
column-detail list, a separate type-picker screen, and a separate
data-preview screen, each added on top of the last. After three rounds of
feedback, the core complaint held: reviewing a column's type decision
requires navigating away from the data that decision is about, making it
hard to judge whether a proposed type is actually right.

This spec replaces that screen set with one consolidated grid per table —
closer to the original web wizard's design, where the data and the type
decision for every column were visible together on one screen — built on
`tview` instead of Bubble Tea, since tview's `Table`, `List`, and `Modal`
primitives are a more natural fit for "a spreadsheet-like grid with a
dropdown per cell" than Bubble Tea's Elm-style model, which required
hand-rolled string rendering for the grid and the split-pane picker.

## Goals

- One screen per table shows real sample data and every column's type
  decision together — no navigating away to see data while deciding a type.
- Editing a column's type only offers types the sampled data can actually
  be loaded as (filtered), so an obviously-wrong choice is never
  selectable in the first place.
- Preserve every existing guarantee: nothing touches Postgres until the
  human explicitly finishes; `Transform` is always cleared to `""` on a
  human override; `tui.Run(ctx, st) error`'s signature and blocking
  contract are unchanged, so `cmd/migrate/main.go` needs no changes beyond
  what already calls it.

## Non-goals

- Changing `internal/review`'s state/decision core (`State`,
  `ApplyDecision`, `ReviewSummary`, `TypeOptions`, the data-preview grid
  sampling) — all of it is reused unchanged.
- Changing the profiler/resolver heuristics that produce the initial type
  suggestions (tracked separately in `TODO.md`).
- Supporting non-interactive/scripted use of `migrate review` — it remains
  interactive-only, same as the Bubble Tea version.

## Architecture

`internal/tui` is rebuilt on `github.com/rivo/tview` (and its underlying
`github.com/gdamore/tcell/v2`), replacing `github.com/charmbracelet/bubbletea`,
`github.com/charmbracelet/bubbles`, and `github.com/charmbracelet/lipgloss`
entirely — none of the three remain as dependencies once this ships.

Screens become primitives inside one `tview.Application`, wired together
with `Application.SetRoot`/`SetFocus` swaps rather than Bubble Tea's
`screen` enum + `Update()` dispatch:

- **Table list** (`tablelist.go`) — a `tview.List` of tables, each item
  showing the table name and its needs-review/auto-approved counts
  (same content as today's table-list screen). Selecting a table (Enter)
  builds and switches to that table's grid screen.
- **Grid screen** (`grid.go`) — a `tview.Table` populated from the
  table's `review.TableView` (`Columns` for headers, `Rows` for sample
  data), with column-only selection (`SetSelectable(false, true)`) so
  moving left/right selects an entire column rather than a single cell.
  Row 0 (headers) is fixed via `SetFixed(1, 0)` so it stays visible while
  scrolling. Each header cell renders as `columnName [targetType]`, with
  a `!` suffix when `NeedsReview` and a `✓` when `Reviewed` (same markers
  as the Bubble Tea column list). The grid is wrapped in a `tview.Flex`
  with two more rows below it: a status line showing the selected
  column's confidence/source/rationale (replacing the old column-detail
  screen's content — this is the only place that information lives now),
  and a footer of key hints. `esc`/`q`/`c` on this screen raise the
  Cancel confirmation; `v` from the table list still isn't needed since
  data is always visible now — the `v` keybinding and the standalone
  preview screen are removed entirely, superseded by the grid itself.
- **Type picker** (`typepicker.go`) — opened by Enter on the grid,
  a centered `tview.List` (not attached under the header — precisely
  anchoring a dropdown under an arbitrary `tview.Table` column is fragile
  across terminal widths and was explicitly descoped during design)
  titled `Edit type: <column>`, listing only the types
  `validTypesForColumn` returns. `enter` applies the selection (calls
  `ApplyDecision`, matching the exact same `Transform: ""`,
  `Rationale: "human override via TUI"` values as today), `esc` closes
  the picker with no change.
- **Confirm/Cancel** — a `tview.Modal` (tview's built-in y/n primitive),
  shown over whichever screen was active. Replaces the Bubble Tea
  version's hand-rolled `"Confirm & Import? y/n"` text line.

`internal/review` is untouched: `State`, `ReviewSummary`, `TableView`,
`ColumnView`, `TypeOptions`, `ApplyDecision`, `Finish`, `Cancel`, `Done`,
`Outcome` all keep their exact current shape. `tui.Run(ctx, st) error`
keeps its exact signature — it now constructs a `tview.Application`
instead of a Bubble Tea `Model`, but the blocking contract (`Run` returns
once the human finishes or cancels) is identical, so `cmd/migrate/main.go`
requires zero changes.

## Type filtering

A new pure function, `validTypesForColumn(values []string, currentType
string) []string`, in `typepicker.go`:

```go
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

It reuses `previewValueForType` (ported unchanged from the Bubble Tea
version's `internal/tui/preview.go`) to check every sampled value against
every candidate type, keeping a type only if *all* sampled values would
load successfully as that type — matching real COPY behavior, where a
single bad row fails the whole load. Per the design conversation's
resolved edge case, `currentType` is always included even if it fails
that check, so the picker is never empty and never forces the human off
their starting point.

## Data flow

1. `tui.Run` builds the `tview.Application` and the table-list primitive
   from `st.Summary()`, and calls `app.Run()` (blocking).
2. Selecting a table builds that table's grid screen from
   `findTable(summary, name)` (ported unchanged) and swaps it in via
   `SetRoot`.
3. Moving the column selection (tview's `SetSelectionChangedFunc`)
   updates the status line from the newly selected column's
   `ColumnView`.
4. Enter on a column: compute `columnSampleValues(tv, col)` (ported
   unchanged) and `validTypesForColumn(...)`, show the centered list.
5. Enter on a type in the picker: call `st.ApplyDecision(table, column,
   review.DecisionRequest{TargetType: t, Transform: "", Rationale: "human
   override via TUI"})`. On success, re-fetch `st.Summary()`, rebuild the
   grid's header row and status line in place (column selection index is
   unchanged, since the grid itself isn't rebuilt from scratch — only
   header cell text and the status line are updated), and close the
   picker. On error, show it in the status line instead of applying
   anything.
6. `f`/`c`/`esc`/`q` on the grid or table list open the `tview.Modal`;
   confirming calls `Finish()`/`Cancel()` then `app.Stop()`.

## Testing

- Pure logic gets plain unit tests with no tview involved:
  `validTypesForColumn`, `previewValueForType` (ported, tests ported),
  `columnSampleValues` (ported, tests ported), grid-row/header-string
  building.
- Screen-level behavior is tested by calling each primitive's
  `InputHandler() func(*tcell.EventKey, func(tview.Primitive))` directly
  with synthetic `tcell.EventKey` values — no real terminal needed, the
  same "drive it without a TTY" approach the Bubble Tea tests used against
  `Update()`. Covers: column selection moving on arrow keys, Enter opening
  the picker with the correctly filtered list, selecting a type calling
  `ApplyDecision` and updating the header, `esc` closing the picker
  without change, and the confirm modal's y/n behavior.
- Interactive `expect`/pty smoke tests (as used throughout this session)
  remain how the real rendered screen gets verified end-to-end against
  actual fixtures — automated tests alone already missed one real Bubble
  Tea startup panic earlier in this project, so this practice continues
  rather than being treated as optional.

## Migration notes

- `go.mod` drops `github.com/charmbracelet/bubbletea`,
  `github.com/charmbracelet/bubbles`, and `github.com/charmbracelet/lipgloss`
  (direct requires) and adds `github.com/rivo/tview` and
  `github.com/gdamore/tcell/v2`. `go mod tidy` after the swap should leave
  no Bubble Tea transitive dependencies behind.
- Every file in the current `internal/tui` (`model.go`, `model_test.go`,
  `preview.go`, `preview_test.go`, `run.go`, `run_test.go`,
  `testhelpers_test.go`) is replaced — this is a full rewrite of the
  package's implementation, not an incremental patch, though the package's
  external contract (`tui.Run(ctx, st) error`) does not change.
- The standalone data-preview screen (`v` keybinding, `screenPreview`,
  `renderPreview`) is removed — its purpose is now served by the grid
  always showing data, so it would be a redundant, disconnected way to see
  the same information the grid already shows inline.
