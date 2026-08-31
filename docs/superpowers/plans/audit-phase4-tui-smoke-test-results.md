# Phase 4 item 4 — Live pty/terminal smoke test of the TUI

**Date:** 2026-08-30
**Binary:** `go build -o /tmp/migrate-tui-check ./cmd/migrate` at commit `aa1f44b`
**Fixture:** `testdata/fixtures/bikes.db`, profiled with `--sample-size 500` into
`/tmp/bikes-tui-test.yaml` (7 columns flagged for review, 7 auto-approved).

## Harness

No pre-existing pty/expect infrastructure exists in this repo (`internal/tui/`
has extensive unit/integration tests but nothing that drives a real terminal;
`docs/superpowers/plans/2026-08-28-tview-grid.md` documents an `expect`-based
pattern used once during that feature's development, but nothing durable was
kept). `github.com/creack/pty` is present transitively in `go.sum` but unused.

Rather than write a throwaway `expect` script, this session drove the real
binary inside a **real tmux session** (`/opt/homebrew/bin/tmux new-session -d
... "/tmp/migrate-tui-check review ..."`, `send-keys`, `capture-pane -p` /
`-e -p`) — tmux renders the tview/tcell output to a real pty and lets any
point in the interaction be dumped as plain text or with SGR escape codes
intact, which is more reliable for verifying a full-screen redrawing TUI
(list selection highlighting, modal overlays) than pattern-matching a raw
`expect` stream. No files were created; the session was interactive
bash-tool commands only, so there is nothing to keep or delete beyond the
build artifact and scratch config (both cleaned up already). If this
approach is worth keeping permanently, it should become a `_test.go` file
gated by a build tag (following `internal/pipeline/integration_test.go`'s
`//go:build integration` convention) — not done here since the task treated
this as a one-off smoke test.

## What was driven, live

1. `migrate profile --sample-size 500 --out /tmp/bikes-tui-test.yaml
   testdata/fixtures/bikes.db` — non-interactive, run as a plain subprocess.
2. `migrate review /tmp/bikes-tui-test.yaml` launched inside a tmux pty
   (`-x 220 -y 50`).
3. From the table list, `n` was pressed once to enter `bikes` and land on
   the first flagged column (`is_installed`), then three more times to
   step through `is_renting` → `is_returning` → `last_reported`, confirmed
   at each step via `capture-pane -p` against the status line.
4. `Enter` opened the type picker on `last_reported`. `capture-pane -e -p`
   (raw escapes preserved) showed the `timestamptz` list entry wrapped in
   `\x1b[30m\x1b[107m` (black-on-white reverse video) — tview's selection
   highlight — confirming it was the pre-selected item, with no arrow keys
   pressed. Offered candidates were `double precision`, `real`, `numeric`,
   `date`, `timestamptz` only.
5. `Enter` again (no navigation) to reconfirm `timestamptz` as-is — the
   exact issue #18 gesture. The grid header immediately updated to
   `last_reported [timestamptz] ✓` and the status line showed
   `source human_override`.
6. Right-arrowed to `legacy_id` (19-digit values, e.g.
   `2124037125711300644`) and opened its picker. Offered candidates:
   `text`, `bigint`, `double precision`, `real` — **no `smallint`, no plain
   `integer`, no `numeric`**. `Esc` closed the picker without applying
   anything (verified this doesn't touch the config).
7. `f` → `Enter` (Yes) to finish and quit, per the footer's own
   `f: finish` binding (`internal/tui/grid.go`'s `gridKeyCapture`); the
   tmux session exited cleanly (process ended, session gone).
8. Inspected `/tmp/bikes-tui-test.yaml` on disk.
9. `migrate load --pg postgresql://localhost/... /tmp/bikes-tui-test.yaml`
   against a scratch Postgres database (the tool provisions its own
   timestamped database name, `bikes_20260830_201257`, regardless of the
   `createdb` target — expected per the tool's existing "always provisions
   a fresh database" behavior, issue #19's territory, not new).
10. `psql` spot-checks on the loaded table, then `dropdb`.

## Issue #18 — type picker clears transform on same-type reselect

**Live check: PASS.**

Resulting YAML for `last_reported` after the "open picker, confirm the
already-selected `timestamptz` with Enter, no navigation" gesture:

```yaml
last_reported:
    declared_type: INTEGER
    target_type: timestamptz
    transform: unix_epoch_seconds
    confidence: 0.85
    source: human_override
    rationale: human override via TUI
    reviewed: true
    needs_review: true
    original_suggestion:
        target_type: timestamptz
        confidence: 0.85
        source: heuristic:unix_epoch_seconds
```

`transform` is still `unix_epoch_seconds` (not cleared), `target_type` is
still `timestamptz`, and `reviewed: true` is set. This is the config the
old bug would have broken (transform cleared to `""`, breaking the COPY).

## Issue #27 — picker validity filter tested raw values instead of post-transform

**Live check: PASS** (both halves exercised against real fixture data,
not constructed inputs).

- **`timestamptz` correctly offered** for `last_reported` (a raw Unix-epoch
  integer, e.g. `1786731364`) — only possible if the filter validates the
  *post-transform* value (an actual timestamp), since the raw integer
  itself is not a valid timestamp literal. Confirmed both that it appeared
  in the list and that it was the pre-selected entry (SGR reverse-video
  around its text in the raw pty capture).
- **`smallint`/`integer` correctly NOT offered** for `legacy_id`, whose
  sampled values include 19-digit numbers (`2124037125711300644` etc,
  vastly out of both `smallint`'s and `integer`'s range) — offered
  candidates were only `text`, `bigint`, `double precision`, `real`. This
  is exactly the "genuinely out-of-range candidate does not get offered"
  half of #27's regression, exercised against real sampled data (not a
  hand-built config) from `bikes.db` itself, so both halves of #27 were
  independently exercised live — no fallback to a different fixture was
  needed.

## End-to-end load confirmation

**PASS.** `migrate load` against the config produced by the live session
loaded cleanly (`bikes: loaded 2509 row(s)`, exit 0). Postgres inspection:

```
last_reported | timestamp with time zone
```

```
 count |          min           |          max           
-------+------------------------+------------------------
  2509 | 1970-01-01 18:00:00-06 | 2026-08-14 13:16:50-05
```

SQLite source: `min(last_reported)=86400`, `max(last_reported)=1786731410`.
`86400` epoch-seconds is exactly `1970-01-02T00:00:00Z`, which the local
`psql` session (`UTC-6`) correctly displays as `1970-01-01 18:00:00-06` —
confirms the `unix_epoch_seconds` transform that issue #18's fix preserved
actually executed correctly at load time, not just that the config looked
right on disk. `legacy_id` loaded as `bigint` with full 19-digit precision
intact (`2124037125711300644` round-tripped exactly), confirming the type
the live picker offered (and that #27 correctly excluded `smallint`/
`integer` for) is the one that actually landed in Postgres.

## New issues found

None. No new bug surfaced during this smoke test; both #18 and #27's fixes
held under live, unscripted terminal interaction driving the actual
resolver-produced draft config for a real fixture, not a hand-built one.

## Cleanup performed

- `dropdb bikes_20260830_201257`
- `rm /tmp/bikes-tui-test.yaml /tmp/bikes-tui-test.yaml.unresolved_report.yaml /tmp/migrate-tui-check`
- No throwaway harness files were created (tmux driven directly via bash
  tool calls), so nothing else to remove.
