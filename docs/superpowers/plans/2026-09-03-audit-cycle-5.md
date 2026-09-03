# Audit Cycle 5

**Goal:** Fifth pass of the periodic audit-and-fix cycle (see
`2026-09-02-audit-cycle-3.md`, `2026-09-03-audit-cycle-4.md`, and the
`audit-cycle-workflow` memory for the phase structure). Re-review
everything landed since cycle 4's Phase A review point.

**Why now:** Cycle 4's Phase A did a whole-codebase pass at `db35d39`
(HEAD `84e6e2d`). Since then **PRs #150–#154** merged to `main` — the
eleven cycle-4 fixes (#139–#149) plus two Copilot follow-up rounds — each
landed one PR at a time, Copilot-only. That is the "combined result never
fresh-reviewed" shape the audit cycles exist for. `v0.3.1` shipped from
this tree. Whole-codebase again per Barrett, concentrated on the diff
`d0cb219..HEAD`.

**Scope note:** triage-first, same as cycles 3 and 4 — every finding is
filed as a GitHub issue unconditionally, and the fix/defer disposition is
decided per-finding with Barrett after all detection phases complete.

---

## Phase A — Whole-codebase fresh-eyes review

Dispatch a fresh `general-purpose` subagent on `model: opus` — **not a
fork**, no prior context on this codebase or these plans — full read
access, **no fix authority**, findings-only.

**Scope:** all non-test files in `internal/` and `cmd/`, plus
`.goreleaser.yaml`, `.github/workflows/`, `scripts/`, `Makefile`, and
`.golangci.yml`.

**Brief the reviewer with:**

- Cycles 3 and 4 both did whole-codebase correctness passes (the most
  recent, cycle 4, ~hours ago at `db35d39`; report
  `audit-cycle4-diff-review-findings.md` with its clean-bill section).
  Carry both clean bills forward — do not re-tread proven-sound,
  unchanged ground. Concentrate on `d0cb219..HEAD` (PRs #150–#154):
  1. **`internal/pipeline/verify_transform.go` — `fitsTargetType` /
     `fitsTemporalRange` (#140 / M2).** New: a `time.Time` result is now
     range-checked against `pgDateMaxYear` (5874897), `pgTimestampMaxYear`
     (294276), `pgTemporalMinYear` (-4713). Check the bounds are right,
     that the `switch` on `targetType` covers exactly the temporal target
     spellings the pipeline actually produces (`date`, `timestamptz` — is
     bare `timestamp` reachable?), and that routing an out-of-range value
     to review (rather than erroring) is consistent with how the
     `unix_epoch_*` arms behave after PR #124. Confirm no in-range but
     semantically-wrong value (the stray-`1.7e9`-in-a-`realdate`-column
     case) is silently newly-*accepted* that wasn't before.
  2. **`internal/sqlitereader/collation.go` — `maskParensAndStringLiterals`
     + `precededByCollateKeyword` (#145 / L3, plus the Copilot round).**
     This parser has now been reworked in four cycles. Walk
     `maskParensAndStringLiterals` against: a `COLLATE` inside a nested
     paren at depth ≥ 2; a string `DEFAULT` whose literal contains an
     unbalanced `(` or `)`; `COLLATE "NOCASE"` vs `COLLATE 'NOCASE'` vs
     `COLLATE [NOCASE]` vs `COLLATE \`NOCASE\`` at top level; a doubled
     `''` inside a masked literal; `precededByCollateKeyword` when
     `COLLATE` is itself the very start of `rest` (no preceding byte), and
     when a comment sat between `COLLATE` and the quote before
     `stripSQLComments` ran. Does masking a paren group to spaces ever
     change a byte offset another part of `parseColumnCollations` relies
     on? (It operates on `rest`, a fresh string — but confirm.)
  3. **`cmd/sqlite2pg/verify.go` — `verifyOutcome` (#144 / L2) and
     `cmd/sqlite2pg/postload_verify.go`'s `errWriter` wrap (#146 / L4,
     plus the Copilot round).** Two report paths now, with subtly
     different ordering logic. Check they cannot disagree on exit code
     for the same underlying condition (verification FAILED + report
     write error): `verifyOutcome` appends the write error to the FAILED
     message; the post-load path returns the FAILED error and never
     surfaces the write error at all. Is that divergence acceptable, or
     is it the "two similar paths silently disagree" shape
     `verifyLoadedTables`' own doc comment warns about? Also: `ew.err`
     latched by the *first* failing write — does the post-load path's
     new pre-"passed" check correctly cover a failure that happened
     during `verifyLoadedTables`' own `fmt.Fprint*` calls, not just the
     summary line?
  4. **`cmd/sqlite2pg/main.go` — the FK step is now unconditional
     (#142 / M4).** `readState`/`st` was removed from that block. Confirm
     nothing else in `executeLoad` read `st` from that call; that
     `markForeignKeysApplied` still runs exactly once on success; that a
     `--resume` which now *always* re-runs the FK transaction can't
     deadlock or fail against a table that a concurrent/previous run
     already fully constrained (idempotent DDL — but re-verify the
     `DROP CONSTRAINT IF EXISTS ... , ADD` + `CREATE INDEX IF NOT EXISTS`
     really are no-ops the second time under one transaction). Check the
     `--dry-run` path still prints the FK statements (it never gated on
     `FKsApplied`, but confirm).
  5. **`internal/pipeline/verify_load.go` — `normalizeNarrowNumeric`
     (#148 / L6).** Applied at the top of `sortKeyFor` and `valuesMatch`.
     Check it is applied on *every* path into a value comparison (is
     there a third caller — `compareColumnUnordered`, a direct
     `exactNumericEqual`?), and that widening `float32`→`float64` at the
     `valuesMatch` entry doesn't change the verdict for a
     `float32`-vs-`float32` pair that used to hit the `%v` fallback and
     match.
  6. **`internal/pipeline/profile.go` — `fallbackTargetNeedsStorageCheck`
     is now `!isTextTargetType(target)` (#149 / L7).** Confirm the two
     call sites (`fallbackSampleMismatch`, the decide_column gate) both
     still behave for the `text`/`varchar(N)` targets that now return
     `false`, and that nothing passed a target string this helper used
     to return `false` for and now returns `true` for in a way that
     forces an expensive full-table scan on a column that doesn't need
     one.
  7. **Release / CI surface (#147 / L5).** `release.yml` now runs
     `gofmt`, `golangci-lint@v2.13.2`, `govulncheck@v1.7.0`, and the
     `go mod tidy` + `git diff` check before goreleaser. Confirm the
     versions match `ci.yml` exactly, that a failure in any of them
     actually blocks the goreleaser step (sequential steps, no
     `continue-on-error`), and that dropping the standalone `go vet` step
     lost no coverage (golangci-lint's `govet` linter — is it enabled in
     `.golangci.yml`?).
  8. **README (`#141` / M3 + the verify-accuracy pass).** Re-read the
     `verify` section against `internal/pipeline/verify_load.go`'s
     `VerifyTable` doc and `primaryKeyOrderingIsSafe`: is "transform-free
     and BINARY-collated primary key ⇒ ordered path, else multiset" now
     stated correctly, and does every remaining flag/positional example
     parse under Go's `flag` (flags before positionals)?
- The two recurring project failure shapes:
  - **Same-two-functions / N-fixes / N-regressions** — `verify_load.go`'s
    numeric comparison quartet and `collation.go`'s parser have each now
    been reworked across four cycles.
  - **Two-features / one-artifact contract break** — precedents: #62,
    #128, #142.

**Output:** findings-only report to
`docs/superpowers/plans/audit-cycle5-diff-review-findings.md`, one finding
per item (file:line, concrete triggering input, observable wrong
behaviour, severity), plus an explicit clean-bill section. Every finding
filed as a GitHub issue before any Phase E fix work starts.

---

## Phase B — Load-test campaign

**Regression gate.** Re-run `scripts/verify-all-fixtures.sh` against the
full local set (`testdata/fixtures/` + `../more data/` +
`beets_library.db`). Confirm no regression: verified-clean count stays at
cycle 4's **38**, and the `sqlite2pg verify` failure count stays 0. The
two cycle-4 fixtures (`sample-varchar-pk`, `sample-geojson`) must still
PASS. The 8 known non-passing (rubber-stamp casualties + `ssb-small` FK
violation + `corrupt001`) must be unchanged.

New purpose-built fixtures only if Phase A finds something that needs
one. Write-up to
`docs/superpowers/plans/audit-cycle5-campaign-results.md`.

---

## Phase C — Property/fuzz tests

**Regression gate.** Run every existing fuzz target's seed corpus
(`go test -run Fuzz ./...`) plus a 45 s `-fuzz` burst each — the 12
targets (9 from cycle 2, 3 from cycle 3). Confirm no new failures and
every checked-in corpus repro still passes.

**Dedicated long run** on `FuzzParseColumnCollations` (≥ 2 min) — the
`collation.go` parser changed again this cycle (#145 + Copilot round), so
exercise the rebuilt scanner hard.

New targets only for whatever Phase A finds. Write-up to
`docs/superpowers/plans/audit-cycle5-fuzz-results.md`.

---

## Phase D — Performance regression check

Standing item. Time `sqlite2pg profile` / `load` / `verify` against
`employee.db` (228 MB) and `beets_library.db` (1.4 GB), `db35d39` vs
`main` tip, 3 runs each for `profile`, 2 each for `load`/`verify`, median
reported (interleaved warm-cache A/B for `beets` — cycle 4's first pass
was polluted by cold-cache drift).

Watch specifically:

- The #148 `normalizeNarrowNumeric` call at the top of every
  `valuesMatch` / `sortKeyFor` — one type switch per compared value on
  the `verify` hot path. Expected negligible; confirm on `employee.db`'s
  3.9 M rows.
- The #140 `fitsTemporalRange` check — only runs for a `time.Time`
  result, so `employee.db` (no temporal transforms) won't show it;
  note if any campaign fixture with a date transform regresses.
- The #142 unconditional FK step on a plain (non-resume) `load` — it
  already ran there; confirm no change.

Write-up to
`docs/superpowers/plans/audit-cycle5-performance-results.md`.

---

## Phase E — Triage and fix (triage-first)

1. **File every finding as a GitHub issue** — unconditional, before any
   fix work.
2. **Consolidated triage.** Present the full findings list (Phase A–D)
   with a recommended **fix / defer** disposition and rationale per item.
   Decide with Barrett what lands this cycle.
3. **For each finding that lands:** TDD cycle — failing test → minimal
   fix → real-Postgres integration test (`go test -tags integration`)
   where the change touches a load/verify path → one commit per issue.
   Feature branch per batch.
4. **PR batching.** Group by theme/severity. Scaffolding-only changes
   (this plan doc, result docs, any new fixtures/fuzz files) merge first
   as their own PR.
5. **Copilot review on every fix-batch PR** before merge. Verify its
   findings against the code — don't blindly implement, push back when
   its stated mechanism is wrong.
6. **Regression check per batch.** Rebuild, re-run
   `scripts/verify-all-fixtures.sh`, `make lint`, `make vulncheck`;
   confirm the clean count holds and verify failures stay 0. Save the
   results table to
   `docs/superpowers/plans/audit-cycle5-<batch>-regression.md` and link
   it from the PR body. Use `Closes #N` in every PR body.

---

## Results

Baseline before any work: `go build ./...`, `go test ./...`,
`go test -tags integration ./...`, `make lint`, `make vulncheck` all
green at `f212de1` (`v0.3.1`).

*(Phase results appended here as the cycle runs.)*
