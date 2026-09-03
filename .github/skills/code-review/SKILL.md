---
name: code-review
description: Repository-specific review guidance for sqlite2pg pull requests. Use this on every code review to hunt for the two failure shapes this project keeps hitting, to know which files are fragile, and to verify runtime claims instead of asserting them.
---

# Reviewing sqlite2pg

`sqlite2pg` migrates SQLite databases into PostgreSQL: it profiles a
source DB, lets a human review ambiguous column-type decisions in a
terminal UI, loads via `COPY`, then verifies the Postgres copy row-for-row
against the source. A wrong type decision or a broken transform corrupts
data or aborts a load, so review correctness matters more than style here.

## Hunt for these two failure shapes first

The periodic audit cycles exist because these two keep recurring. Look for
them specifically before anything else.

### 1. A fix that quietly breaks a contract another part depends on

Several core files have been reworked across many PRs, one Copilot round
at a time. A change that looks locally correct can violate an invariant a
different function relies on.

- **`internal/pipeline/verify_load.go` — the numeric-comparison quartet**
  (`exactNumericEqual`, `crossTypeNumericEqual`, `numericSortKey`,
  `sortKeyFor`, `valuesMatch`). Invariant: *two values `valuesMatch`
  considers equal must produce the same `sortKeyFor` key.* It has broken
  four times (int-vs-float64 key prefixes, NaN, `[]byte`/string namespace,
  plain `int`/`float32`). Any edit here: re-derive that invariant by hand
  for every type pair the pipeline can produce.
- **`internal/sqlitereader/collation.go` — the CREATE TABLE parser**
  (`maskParensAndStringLiterals`, `precededByCollateKeyword`,
  `skipQuoteOrComment`, `stripSQLComments`, `matchingParen`,
  `splitTopLevelCommas`, `columnListOpenParen`, `leadingIdentifier`).
  Reworked in five cycles. It must never *under*-report a non-BINARY
  collation as BINARY — that sends a mis-ordered table down the streaming
  PK-ordered verify path and produces false mismatches. Over-reporting is
  the fail-safe direction. Check every quote style (`"…"`, `` `…` ``,
  `[…]`, `'…'`), SQLite's double-quoted-string misfeature in `DEFAULT`,
  nested parens, comments between tokens, doubled `''`.
- **`internal/copywriter/transform.go` — the `Transform` switch arms.**
  Every arm that can receive a non-string value needs an explicit
  `int64`/`int`/`float64` case and an *erroring* `default` — a bare
  `raw.(string)` check that falls through to `return raw, nil` makes
  full-table verification a silent no-op. All arms that produce a
  `time.Time` must reject a result outside PostgreSQL's storable range.
- **`internal/tui/logic.go` — `previewValueForType` and the type picker.**
  The picker only sees display strings (`fmt.Sprintf("%v", rawValue)`),
  not raw values. A "human override is final" — nothing re-checks a TUI
  decision against the full table — so a type the picker marks valid that
  then fails at `COPY` is a real bug. Distinguishing "a float64 the driver
  returned, rendered by `%v`" from "a string that looks the same" requires
  the column's SQLite affinity (`ColumnView.DeclaredType`), not the
  display string.

### 2. Two features sharing one artifact, where a change to one breaks an
### assumption the other makes

Precedents:

- **#62** — a `verify` *failure* still deleted the `.state.json` a human
  needs to investigate it.
- **#128 / #142** — the foreign-key step gated on a one-shot `FKsApplied`
  bool while the FK statement set is re-derived from a mutable config
  every run, so a table included between a completed load and a `--resume`
  silently got no foreign keys.
- **#146 / #158** — closing a report-write-latch asymmetry on one of the
  two verify report paths (`verify --out` vs `load --verify` vs
  `verify > file`) while leaving it open on another.

When a PR touches a state file, a config field, a shared writer, or a
struct both the load and the verify path read, trace *both* consumers.

## Verify runtime claims — don't assert them

This repo's bugs often hinge on exact behavior. If a review comment (or
the code's own comment) depends on one of these, check it, and say what
you checked:

- **PostgreSQL limits.** `date` stores to 5874897 AD, `timestamp` /
  `timestamptz` to 294276 AD, both back to Julian day 0 = 4714-11-24 BC
  (Go astronomical year `-4712` = 4713 BC; Go year `-4713` = 4714 BC and
  is effectively unstorable). `varchar(n)` caps at 10485760. `NaN = NaN`
  is true in Postgres.
- **Go `flag` parsing.** `flag.Parse` stops at the first argument that
  does not start with `-`; flags after a positional are never parsed.
- **`fmt` verb output.** `%v` of a `float64` past 1e6 is scientific
  notation (`"1e+06"`, `"1.712345678e+09"`); `%v` of an `int64` never is.
- **SQLite grammar.** Affinity rules
  (sqlite.org/datatype3.html#determination_of_column_affinity); the
  double-quoted-string misfeature (`"..."` is accepted as a string
  literal when it is not a valid identifier); `COLLATE` accepts a name in
  any of the four quote styles.

You can build (`go build ./...`), run tests (`go test ./...`), and run
`sqlite3` / `psql` to confirm.

## Project conventions

- **Comments** are terse — one or two lines, the *why* not the *what*.
  Rationale belongs in the PR description, not the code. Don't ask for
  more comments unless the code genuinely can't be followed without them.
- **Tests.** Unit tests run with `go test ./...`. Tier-3 tests that need a
  real Postgres are behind `//go:build integration` and run with
  `go test -tags integration ./...` (`PGURL` env). A load/verify-path fix
  should carry an integration test.
- **PR bodies** use `Closes #N` / `Fixes #N` so the issue auto-closes.
- **The release** (`.github/workflows/release.yml`) must build the tagged
  tree verbatim — no mutating `go mod tidy`, no `before` hooks that touch
  the network (issue #117). The tidiness check that writes `go.mod` lives
  in CI; the release job uses `go mod tidy -diff`.

## What not to flag

The surrounding code is deliberately consistent. Do not raise naming,
formatting, import ordering, or "consider extracting a helper" comments
when the code matches its neighbours — `gofmt` and `golangci-lint`
(config in `.golangci.yml`) already gate style in CI. Spend the review on
correctness, the two failure shapes above, and anything that would fail at
`COPY` or produce a wrong `verify` verdict.
