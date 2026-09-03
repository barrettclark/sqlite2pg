# Audit Cycle 4

**Goal:** Fourth pass of the periodic audit-and-fix cycle (see
`2026-09-02-audit-cycle-3.md` and `audit-cycle-workflow` memory for the
phase structure). Re-review everything landed since cycle 3's Phase A
review point with the tooling the prior cycles produced.

**Why now:** Cycle 3's Phase A did a whole-codebase pass at `db35d39`.
Since then **PRs #124–#138** merged to `main`, each reviewed only by
Copilot, one PR at a time — the "combined result never fresh-reviewed"
shape the audit cycles exist for. Plus the `migrate` → `sqlite2pg` binary
rename (#130) touched ~70 files and landed *after* cycle 3's Phase A ran,
so it has had no fresh-eyes pass at all. `v0.3.0` shipped from this tree.

**Scope note:** triage-first, same as cycle 3 — every finding is filed as
a GitHub issue unconditionally, but the fix/defer disposition is decided
per-finding with Barrett after all detection phases complete.

---

## Phase A — Whole-codebase fresh-eyes review

Dispatch a fresh `general-purpose` subagent on `model: opus` — **not a
fork**, no prior context on this codebase or these plans — full read
access, **no fix authority**, findings-only.

**Scope:** all non-test files in `internal/` and `cmd/`, plus
`.goreleaser.yaml`, `.github/workflows/`, `scripts/`, `Makefile`, and
`.golangci.yml`.

**Brief the reviewer with:**

- Cycle 3's whole-codebase pass (`db35d39`, findings #103–#122, report
  `audit-cycle3-diff-review-findings.md`) covered `internal/` + `cmd/` for
  correctness ~10 days ago. Do **not** just repeat it — go deeper on the
  surfaces that changed since (PRs #124–#138):
  1. **The #103–#122 fixes themselves.** Several went 2–3 Copilot review
     rounds, one commit at a time. Look specifically for a fix quietly
     changing a contract another commit depends on. Concrete areas:
     - `internal/copywriter/transform.go` — the four `switch` arms
       finished in #124 (`iso8601_to_timestamptz`,
       `dayfirst_to_timestamptz`, `numeric_text_to_integer/double`), plus
       the JDN / epoch-range / `float32` guards. Check they're mutually
       consistent with the five arms fixed earlier and that no full-table
       verification path became a silent no-op again.
     - `internal/sqlitereader/collation.go` — the #125 rewrite:
       `skipQuoteOrComment`, `stripSQLComments`, comment-aware
       `matchingParen` / `splitTopLevelCommas` / `columnListOpenParen`,
       doubled-quote `leadingIdentifier`, `CREATE VIRTUAL TABLE` → -1.
       This parser has now been reworked in three separate cycles.
     - `internal/tui/logic.go` — the #126 `dateTransformPreview`
       magnitude guard on `int64(f)`.
     - `internal/pipeline/verify_load.go` — the #131 NaN sort-key /
       `exactNumericEqual` change, and `internal/pipeline/profile.go`'s
       #131 `varcharFinding` text-fallback past `maxPostgresVarcharLen`.
  2. **#128's foreign-key DDL change.** `foreignKeyStatement` now emits a
     single `ALTER TABLE t DROP CONSTRAINT IF EXISTS "n", ADD CONSTRAINT
     "n" FOREIGN KEY (...) ...`, and `GenerateForeignKeyIndexes` emits
     `CREATE INDEX IF NOT EXISTS`. Check: the single multi-subcommand
     `ALTER TABLE` against `foreignKeyConstraintNames`' disambiguation and
     the `--dry-run` output; that `executeLoad`'s `tx.Exec` per statement
     still behaves (no-arg → simple protocol); that `FKsApplied` being
     "just an optimization now" is actually true on every path.
  3. **The `migrate` → `sqlite2pg` rename (#130).** ~70 files, mostly
     strings/comments, no fresh-eyes pass. Verify nothing user-facing
     broke: every `errors.New`/`fmt.Errorf` message, the `run` subcommand
     still dispatched from `main.go`, flag help text, `scripts/`, the
     `homebrew_casks` block naming, README examples.
  4. **#137's `errWriter`** in `cmd/sqlite2pg/verify.go` and the explicit
     `--out` file `Close()` handling in `runVerify` (short-write latching,
     the `reportFile != nil` branches, the "wrap only for --out" split).
  5. **Release / CI surface (#130, #134).** `.goreleaser.yaml`'s
     `homebrew_casks` migration; `release.yml`'s build/vet/test gate +
     pinned `version: "2.18.0"`; `ci.yml`'s `tags: ["v*"]` trigger and
     un-filtered `pull_request`; `.github/workflows/vulncheck.yml`'s
     weekly schedule; the `go 1.26.8` bump.
  6. **`.golangci.yml` itself.** Is any `errcheck.exclude-functions`
     entry or `staticcheck` `-check` masking a real class of bug rather
     than idiomatic noise? Is the `_test.go` errcheck exclusion too broad?
- The two recurring project failure shapes, named so the reviewer knows
  what to hunt for:
  - **Same-two-functions / three-fixes / three-regressions** — the
    `verify_load.go` numeric comparison and `collation.go` parser have
    each now been reworked across multiple cycles.
  - **Two-features / one-artifact contract break** — a change to one
    feature violating an assumption another makes about a shared file /
    state artifact / struct field. Precedent: issue #62 (a verify
    *failure* still deleting the `.state.json` needed to investigate it);
    issue #128 (the FK-step crash window vs. `FKsApplied`).
- Carry forward cycle 3's **"clean bill"** (end of
  `audit-cycle3-diff-review-findings.md`) so proven-sound, unchanged
  ground is not re-tread.

**Output:** findings-only report to
`docs/superpowers/plans/audit-cycle4-diff-review-findings.md`, one finding
per item (file:line, concrete triggering input, observable wrong
behaviour, severity), plus an explicit clean-bill section. Every finding
filed as a GitHub issue before any Phase E fix work starts.

---

## Phase B — Load-test campaign

**Regression gate.** Re-run `scripts/verify-all-fixtures.sh` against the
full local set (`testdata/fixtures/` + `../more data/` +
`beets_library.db`). Confirm no regression: clean-database count does not
drop below cycle 3's 36, and the `migrate verify` failure count stays 0.

**New purpose-built fixtures** (deferred from cycle 3 — findings #60/#61
and audit-final #77 never fired in the campaign because no corpus
database has the triggering column shape). Build both, check into
`testdata/fixtures/` with golden-test coverage, add to the campaign list:

- A DB with a `VARCHAR(N)`-declared **text primary key** and at least two
  `VARCHAR` columns of differing N, plus a TEXT-PK-→-`integer`-via-transform
  case. Exercises #60 (`verify` PK-ordered comparison vs. a transformed
  PK) and #77 (`isTextTargetType` / `COLLATE "C"` on `varchar(N)`).
- A DB with a `jsonb`/geojson-shaped text column. Exercises #61 (raw
  source text vs. Postgres-canonicalized `jsonb`).

Run the full campaign against a build of `main` with the new fixtures
present; both must verify **clean** (the fixes are in — this is
regression coverage confirming `verify` no longer false-fails those
shapes). Write-up to `docs/superpowers/plans/audit-cycle4-campaign-results.md`.

---

## Phase C — Property/fuzz tests

**Regression gate.** Run every existing fuzz target's seed corpus
(`go test -run Fuzz`) plus a 45 s `-fuzz` burst each: the 9 from cycle 2
and the 3 from cycle 3 (`FuzzTransformArmsNeverSilentlyPassThrough`,
`FuzzColumnCollationsRoundTripWithNoise`,
`FuzzMatchingParenNeverCountsQuotedOrCommentedParens`). Confirm no new
failures and that every checked-in corpus repro still passes.

**Dedicated long run** on `FuzzParseColumnCollations` (≥ 2 min) — it
still showed high "new interesting" churn in cycle 3 after the #125
parser rework, so exercise the rebuilt scanner hard.

New targets only for whatever Phase A finds (same triage-and-fix
treatment as everything else).

Write-up to `docs/superpowers/plans/audit-cycle4-fuzz-results.md`.

---

## Phase D — Performance regression check

Standing item. Time `sqlite2pg profile` / `load` / `verify` against
`employee.db` (228 MB) and `beets_library.db` (1.4 GB), `db35d39` vs
`main` tip, 3 runs each for `profile`, 2 each for `load`/`verify`, median
reported. Method per issue #55's original discovery.

Watch specifically:

- Cycle 3 accepted a **+11 % `profile` regression on `employee.db`**
  (`4710 → 5248 ms`) attributed to the #69/#84 no-transform full-table
  fit check and `9cfec0c`'s VARCHAR-widening scan. Re-measure — confirm
  it hasn't grown further.
- The #128 FK DDL change (DROP + ADD as two subcommands per FK, plus
  `CREATE INDEX IF NOT EXISTS`) — negligible expected, confirm.
- The #131 `varcharFinding` change and the `errWriter` wrap on `verify
  --out`.

Write-up to `docs/superpowers/plans/audit-cycle4-performance-results.md`.

---

## Phase E — Triage and fix (triage-first)

1. **File every finding as a GitHub issue** — unconditional, before any
   fix work.
2. **Consolidated triage.** Present the full findings list (Phase A–D)
   with a recommended **fix / defer** disposition and rationale per item.
   Decide with Barrett what lands this cycle.
3. **For each finding that lands:** TDD cycle — failing test → minimal
   fix → real-Postgres integration test (`go test -tags integration`) →
   one commit per issue. Feature branch per batch.
4. **PR batching.** Group by theme/severity (confirm grouping once
   findings are in). Scaffolding-only changes (docs, new fixtures, fuzz
   files) merge first as their own PR.
5. **Copilot review on every fix-batch PR** before merge. Treat its
   findings with verify-against-the-code rigor — don't blindly implement,
   and push back when its stated mechanism is wrong (cycle 3 had several
   of those).
6. **Regression check per batch.** Rebuild, re-run
   `scripts/verify-all-fixtures.sh`, `make lint`, `make vulncheck`;
   confirm the clean count doesn't drop and verify failures stay 0. Save
   the results table to
   `docs/superpowers/plans/audit-cycle4-<batch>-regression.md` and link
   it from the PR body. Use `Closes #N` in every PR body.

---

## Cycle 3 closeout (folded in here)

Cycle 3's plan doc (`2026-09-02-audit-cycle-3.md`) Phase E section was
never updated with outcomes. For the record:

- All 20 Phase A findings (#103–#122) fixed and merged across PRs
  #124–#131, plus issue #81 (closed — already fixed on `main` by commit
  `5522180`).
- Follow-ups filed during the cycle: **#128** (FK-step crash window —
  fixed in #132, making the FK DDL idempotent), **#129** (`brews:` →
  `homebrew_casks:` — done in #130), **#136** (`verify --out` write
  errors — fixed in #137).
- Non-cycle work that rode along: the Makefile + `gofmt` CI gate (#133),
  golangci-lint + govulncheck + `make cover` (#134, which also caught a
  live `golang.org/x/text` CVE and a stale CI Go pin), the README Install
  section (#138), and the `v0.3.0` release.

---

## Results

Working branch off `main`; baseline before any work: `go build ./...`,
`go test ./...`, `go test -tags integration ./...`, `make lint`, and
`make vulncheck` all green.

*(Phase results appended here as the cycle runs.)*
