# Audit Cycle 2

**Goal:** Extend the 2026-08-30/31 audit-and-fix cycle (see
`2026-08-29-audit-and-load-test.md`) to the substantial amount of code that
landed after its own last review point, using the tooling and lessons that
cycle produced — most notably `migrate verify`, which didn't exist yet when
the original campaign's Phase 2 ran.

**Why now:** Since Phase 4's whole-diff review (which covered
`76ae27e~1..aa1f44b`), three more PRs merged to `main` with no fresh-eyes
review of the combined result: `#57` (the 16 Phase-4 fixes plus `migrate
verify` itself, built and then fixed three separate times as real dogfooding
found scan-drift, sort-key, and precision/collation bugs in it), `#58`
(issue #56, the zero-length-BLOB fix), and `#59` (post-load verification +
a README rewrite, itself needing a follow-up commit after Copilot's review
caught a masked-error bug and a CI-hang risk). That's a lot of surface area
that's only ever been reviewed by the model that wrote it, one commit at a
time — exactly the shape that produced Phase 4's 15 findings in the first
place.

**Scoping note:** default to "fix everything found," same as both prior
cycles — Barrett has approved this pattern each time it came up. Flag here
explicitly rather than re-deriving mid-cycle; say otherwise at kickoff if
the intent has changed.

## Phase A — Whole-diff review of everything since the last audited point

Range: `aa1f44b..main` (everything landed by PRs #57, #58, #59 — the 16
Phase-4 fixes, `migrate verify`'s full build plus its three self-fix
rounds, issue #56's fix, post-load verification, and the README rewrite).
Same shape as the original Phase 4 review: dispatch a fresh model with no
prior context on this diff, `internal/` and `cmd/` only, looking
specifically for the cross-commit interaction pattern this project keeps
hitting — a fix in one commit quietly changing a contract another commit
(often written by a *different* dispatch, hours apart) depends on. Two
concrete precedents worth naming in the dispatch brief so the reviewer
knows exactly what shape to look for: issue #52's config-cleanup change
almost broke `migrate verify`'s own state-file dependency before anyone
noticed; the numeric-comparison logic in `verify_load.go` was independently
broken three separate times by three separate "fixes" to the same two
functions before Copilot's review caught the third one.

No fix authority — findings only, written to
`docs/superpowers/plans/audit-cycle2-diff-review-findings.md`, filed as
GitHub issues before any fix work starts (same paper-trail practice as
both prior cycles).

## Phase B — Full load-test campaign, now using `migrate verify` directly

Re-run the same 41-database local set (`testdata/fixtures/`, `../more
data/`, `beets_library.db`) — but this time every database gets a genuine
`migrate verify` pass as its correctness check, not the ad hoc
hex-dump/MD5/row-count tricks earlier campaigns had to invent per
database now that a real tool exists for this. This both re-confirms
nothing in the last cycle's 40-odd commits regressed and stress-tests
`verify` itself against real-world scale and shape one more time.

**New this cycle:** turn the campaign into a checked-in script
(`scripts/verify-all-fixtures.sh` or similar) rather than re-deriving
dispatch instructions from scratch — profile → mark-reviewed → load →
verify → dropdb, looped over a database list, with the same per-database
result table this plan's prior campaigns produced. Worth the investment
now that this is the third time the same campaign shape has been built
from a prompt; a script makes future cycles (and Barrett running it
directly, standalone) cheaper.

## Phase C — Property/fuzz tests for the comparison-logic hot spots

The `verify_load.go` numeric-comparison and collation-detection code took
three real-world dogfooding rounds to get right. A property-based test
generating random `int64`/`float64` pairs (including ones that collide
under naive float conversion) against `valuesMatch`/`sortKeyFor`, and
random SQLite `COLLATE` declarations against the collation-detection path,
would very likely have caught the precision bug in one run instead of a
full campaign cycle. Use Go's built-in fuzzing (`go test -fuzz`) — this
project's Go version supports it natively, no new dependency.

Same treatment for two other areas that have produced dogfooding-only
bugs before: `copywriter` transforms (especially the numeric/date ones —
`numeric_text_to_integer`, `iso8601_to_date`, the epoch-scale transforms)
and DDL identifier quoting/collision handling (`internal/ddl/identifiers.go`
has absorbed three separate fixes for the same "two similar-looking
strings collide after truncation" shape).

Scope this to the fuzz harnesses themselves plus whatever they find —
findings get the same triage-and-fix treatment as everything else, not a
separate track.

## Phase D — Performance regression check, as standard practice now

Time `migrate profile`/`load`/`verify` against the two largest local
databases (`employee.db`, `beets_library.db`) before and after this
cycle's changes, same method as issue #55's discovery. Make this a
standing item in every future cycle's plan template, not a one-off —
issue #55 (full-table verification's O(columns×rows) scan) only got
caught because this step existed once; there's no reason to assume the
next performance regression will be found by luck again.

## Phase E — Triage and fix

Same process as both prior cycles: file every finding as a GitHub issue
before fixing, TDD cycle (failing test → minimal fix → real Postgres
re-verification) per issue, one commit per fix, feature branch + worktree
isolation, PR against `main`. **New this cycle: run Copilot's automated
review on every fix-batch PR before considering it done**, not as an
afterthought — it found real, independently-confirmed bugs on both PRs
it was tried against this cycle (a silent-precision-loss regression in a
fix I'd already marked verified, and a CI-hang risk plus an
error-masking bug in the post-load-verify feature). Treat a Copilot
finding with the same rigor as any other review finding: verify against
the actual code before acting, don't blindly implement.

## Results

Worktree: `.claude/worktrees/audit-cycle-2`, branch `worktree-audit-cycle-2`.
Baseline before any work: `go build ./...` clean, `go test ./...` all 11
packages passing.

### Phase A — whole-diff review (`aa1f44b..main`, 33 commits) — complete (2026-09-01)

Dispatched a fresh `general-purpose` subagent on `model: opus`, no prior
context, `internal/` + `cmd/` only, no fix authority. Full report:
`docs/superpowers/plans/audit-cycle2-diff-review-findings.md`.

**9 findings — 3 High / 4 Medium / 2 Low.** All filed as GitHub issues
before any fix work (paper-trail practice, both prior cycles):

| # | Finding | Sev | Issue |
|---|---|---|---|
| 1 | `verify`'s PK-ordered comparison mass-false-fails when a PK column's type was changed by a transform (TEXT PK → `integer` via `numeric_text`, TEXT PK → `uuid`) | High | [#60](https://github.com/barrettclark/sqlite2pg/issues/60) |
| 2 | `verify` compares raw source text against Postgres's canonicalized `jsonb` output → every `jsonb`/geojson column false-fails | High | [#61](https://github.com/barrettclark/sqlite2pg/issues/61) |
| 3 | A post-load verification *failure* still deletes the config + `.state.json` — the exact artifacts needed to investigate it (`nil` hard-coded into `cleanupConfigAfterLoad`) | High | [#62](https://github.com/barrettclark/sqlite2pg/issues/62) |
| 4 | Sub-microsecond timestamps from `excel_serial_to_timestamptz` / RFC3339Nano can never match Postgres's µs resolution | Medium | [#63](https://github.com/barrettclark/sqlite2pg/issues/63) |
| 5 | TUI derives a date/timestamp transform from a *single* sample while validating the type against *all* samples → mid-load abort, or a silent wrong-transform | Medium | [#64](https://github.com/barrettclark/sqlite2pg/issues/64) |
| 6 | `sortKeyFor` vs `valuesMatch` disagree for cross-type numerics at the top of int64's range → same data passes with a PK, fails without one | Medium | [#65](https://github.com/barrettclark/sqlite2pg/issues/65) |
| 7 | `determineVerify` silently ignores a piped `y` answer (CI-hang fix can't tell "answer waiting" from "never written") | Medium | [#66](https://github.com/barrettclark/sqlite2pg/issues/66) |
| 8 | `compareColumnUnordered` panics (not errors) if row counts differ mid-verify (concurrent write); ordered path already handles it | Low | [#67](https://github.com/barrettclark/sqlite2pg/issues/67) |
| 9 | FK index names disambiguated against each other but not against table names, which share `pg_class` | Low | [#68](https://github.com/barrettclark/sqlite2pg/issues/68) |

**Explicit clean bills** (categories checked, nothing found): the
numeric-comparison hot spot (all three historical `verify_load.go`
regressions genuinely fixed — only Finding 6's narrow int64-boundary
crack remains), FK-index vs. table identifier disambiguation *agreement*
(the gap in #9 is cross-namespace, not within either pass),
`ddl.PostgresTableNames` call-site coverage, the `TableSource` goroutine
lifecycle, the zero-length-BLOB fix (#56) vs. verify, the batched
full-table verification (`311fb25`, no vacuous tests), the `resolver.Decide`
hundredths rework, the `ColumnCollations` parser, and `ReadForeignKeys`
graceful degradation (`03fa321`).

Findings 1, 2 and 4 are the same root shape: **`verify` doesn't model
every transform / type decision the load path can make**, so it reports
FAIL on correct loads — and it's now on by default via `--verify`.
Finding 3 is the cleanest cross-commit contract break (same
two-features-one-artifact shape as issue #52).

Fixes deferred to Phase E.

### Phase B — *pending*
### Phase C — *pending*
### Phase D — *pending*
### Phase E — *pending*
