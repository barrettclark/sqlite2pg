# TUI review wizard — design

## Context

`sqlite2pg` currently reviews ambiguous column-type decisions through a
localhost-only web UI (`internal/wizard`): a Go HTTP server plus a small
vanilla-JS frontend (`static/app.js`, `static/index.html`) that renders a
data-preview grid and lets a human override the profiler's per-column type
guesses before `migrate run`/`migrate review` proceeds to load.

This spec replaces that web UI with a terminal UI (TUI), reusing the
UI-agnostic core (`State`, `ReviewSummary`, `BuildReviewSummary`) that
already exists behind the HTTP layer, and renames the package that holds
it from `wizard` to `review`.

## Goals

- Replace the browser-based review flow with an in-terminal one, so
  reviewing a migration never requires a browser.
- Reuse the existing review-state/session logic unchanged; only the
  presentation layer is new.
- Keep the same guarantee the web wizard has today: nothing touches
  Postgres until the human explicitly confirms.

## Non-goals

- Changing the profiler/resolver heuristics that produce type suggestions
  (tracked separately under "Address type differentiation" in `TODO.md`).
- Keeping the web wizard around behind a flag. It is deleted outright —
  this is a pre-1.0 solo project with no other consumers of the web flow.

## Architecture

- `internal/wizard` is renamed to `internal/review` (package `wizard` →
  package `review`), since it's no longer specifically the "web wizard"
  — it's the shared review-session core both the old web UI and the new
  TUI build on. All existing callers (`cmd/migrate/main.go`) update their
  import path and `wizard.` references accordingly.
- New package `internal/tui`, depending on `review.State` /
  `review.ReviewSummary` / `review.BuildReviewSummary` (unchanged logic,
  moved package) and adding a `bubbletea.Model` on top.
- `internal/review` loses `handlers.go`, `server.go`, `static.go`,
  `static/`, and their tests (`handlers_test.go`, `server_test.go`,
  `static_test.go`). `state.go` and `review_model.go` (plus their tests)
  move over unchanged.
- `TYPE_OPTIONS`, currently hardcoded in `static/app.js`, moves to a Go
  var in `internal/review` (`review.TypeOptions`) so the TUI's picker and
  any future consumer share one list instead of re-duplicating it.
- New dependencies: `github.com/charmbracelet/bubbletea`,
  `github.com/charmbracelet/bubbles` (table/list components),
  `github.com/charmbracelet/lipgloss` (styling).

## Screens & navigation

1. **Table list** (entry screen) — rows: table name, column count,
   needs-review count, auto-approved count. Header shows the overall
   `N need review, M auto-approved` line, matching the web wizard's counts
   line. Keys: `↑↓`/`j k` move, `enter` drills into a table, `f`
   finish/confirm, `c`/`esc`/`q` cancel.
2. **Column detail** (per table) — scrollable list of that table's
   columns: declared type → target type, confidence, source, rationale, a
   needs-review marker. `enter` opens the type picker for the selected
   column. `esc`/`backspace` returns to the table list.
3. **Type picker** (overlay on column detail) — list of
   `review.TypeOptions`, current target pre-selected. `enter` commits via
   `State.ApplyDecision` (transform always left blank on manual override,
   matching current behavior — a stale transform is never implicitly
   carried over to a new target type). `esc` cancels the picker without
   changing anything.
4. **Confirm/cancel** — no separate screen. `f` (Finish/Confirm & Import)
   and `c` (Cancel) are always-available keys shown in a footer help bar.
   Since committing is irreversible from the TUI's perspective, `f` and
   `c` both show a one-line inline confirmation prompt (`Confirm & Import?
   y/n`) before calling `State.Finish()`/`State.Cancel()`.

## CLI integration

- `migrate run` and `migrate review` (`cmd/migrate/main.go`) stop calling
  `wizard.Listen` / `wizard.Run` / `openBrowser`. They call a new
  `tui.Run(ctx, st) error` that runs the Bubble Tea program in the current
  terminal and blocks until it exits — same shape as today's blocking
  `wizard.Run`, so the surrounding `switch st.Outcome()` logic in
  `main.go` is unchanged.
- The `--port` flag on `migrate review` (and the equivalent flag in the
  `run` subcommand) is removed — there is no listener anymore. This is a
  user-facing CLI change (flag removal), not just an addition.
- The `openBrowser` helper in `main.go` is deleted (its only caller was
  the wizard launch).

## Testing

- `internal/review`: existing `review_model_test.go` and the surviving
  parts of `state.go`'s coverage are untouched. `handlers_test.go`,
  `server_test.go`, `static_test.go` are deleted along with the code they
  test.
- `internal/tui`: models are tested by driving `Update()` with synthetic
  `tea.Msg` key events and asserting on resulting model state/view output
  — the standard Bubble Tea testing pattern, no real terminal needed.
  Cover: navigating table list → column detail → picker → back; applying
  a decision updates `State` (verified via `State.Summary()`); Finish and
  Cancel each produce the correct `Outcome`.
- Manual smoke test against a couple of `testdata/fixtures` databases
  before calling the work done, same as today's practice for the web
  wizard.

