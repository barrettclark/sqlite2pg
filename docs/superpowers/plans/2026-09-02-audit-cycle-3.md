# Audit Cycle 3

**Goal:** Third pass of the periodic audit-and-fix cycle (see
`2026-09-01-audit-cycle-2.md` for the phase structure this formalized, and
`audit-final-whole-codebase-findings.md` for the whole-codebase pass that
followed it). Re-review everything landed since the last audited point with
the tooling and lessons the prior cycles produced.

**Why now:** The last fresh-eyes review point is `db35d39` (the audit-final
whole-codebase pass, findings #77–#94). Since then **63 commits across 8
PRs** merged to `main` with no fresh-eyes review of the combined result:

- **PRs #95–#101** — the #77–#94 fix work. Several of these went through
  5–12 rounds of Copilot review and were built up one commit at a time
  (`audit-final-transform-fixes` alone is ~12 commits of NaN/Inf/overflow
  guards layered onto the same handful of `copywriter` transforms). That is
  exactly the "same two functions, three fixes, three regressions" shape
  this cycle exists to catch.
- **PRs #100–#101** — `readAnswerWithDeadline` reworked twice
  (`e35649b`, `8c1f0b7`, `7a19a37`) to base `gotAnswer` purely on bytes
  read; `columnListOpenParen` changed to recognize `CREATE VIRTUAL TABLE`
  (`7215a13`, `73e6324`); `resolver.Decide` changed to only force review on
  a genuine disagreement (`9d399a1`).
- **PR #102** — Homebrew/goreleaser release automation
  (`.goreleaser.yaml`, a Homebrew formula publish step). New
  release/CI surface that no prior audit cycle has ever reviewed.

**Scoping note — this cycle changes the fix-scope default.** Both prior
cycles ran "fix everything found," approved each time. This cycle runs
**triage-first**: every finding is still filed as a GitHub issue
unconditionally (paper trail), but the fix/defer disposition is decided
per-finding with Barrett after all detection phases complete, not assumed.
Flagging here rather than re-deriving mid-cycle.

**Also in scope:** open issue
[#81](https://github.com/barrettclark/sqlite2pg/issues/81) (M2 — the TUI
integer preview routes through `float64`, reintroducing issue #15's
precision loss in the review UI). Filed and confirmed during audit-final,
deferred at the time; folded into this cycle's Phase E triage.

---

## Phase A — Whole-codebase fresh-eyes review

Not diff-scoped. Dispatch a fresh `general-purpose` subagent on
`model: opus` — **not a fork**, no prior context on this codebase or these
plans — with full read access, **no fix authority**, findings-only.

**Scope:**

- All non-test files in `internal/` and `cmd/`.
- **Plus** `.goreleaser.yaml`, `.github/` workflows, and `scripts/` — the
  release/CI/automation surface. No prior cycle has reviewed any of it;
  PR #102 added the largest piece one day ago.

**Brief the reviewer with:**

- The audit-final pass (`db35d39`, findings #77–#94) already swept
  `internal/` + `cmd/` for correctness one day ago. Do **not** just repeat
  it — go deeper on the surfaces that changed since:
  1. **The #77–#94 fixes themselves.** Many landed across 5–12 Copilot
     review rounds, one commit at a time, reviewed only by the model that
     wrote each commit. Look specifically for a fix in one commit quietly
     changing a contract another commit (often a different dispatch, hours
     apart) depends on.
  2. **PR #100–#101 changes:** `readAnswerWithDeadline` /
     `determineVerify` (byte-driven `gotAnswer`, empty-EOF handling),
     `columnListOpenParen` / `parseColumnCollations` (CREATE VIRTUAL TABLE
     recognition, first-paren location), `resolver.Decide` (forces review
     only on genuine disagreement — check the near-tie / agreement path
     didn't lose a guard).
  3. **PR #102 release surface:** `.goreleaser.yaml`, the Homebrew formula
     publish step, any token/permission scope in the workflow.
- The two recurring project failure shapes, named so the reviewer knows
  exactly what to hunt for:
  - **Same-two-functions / three-fixes / three-regressions** — the
    `verify_load.go` numeric-comparison logic was independently broken
    three times by three separate "fixes"; `internal/ddl/identifiers.go`
    absorbed three fixes for the same truncation-collision shape.
  - **Two-features / one-artifact contract break** — issue #52's
    config-cleanup change almost broke `migrate verify`'s own state-file
    dependency; issue #62 (audit-cycle-2) was a verification *failure*
    still deleting the `.state.json` needed to investigate it.
- Carry forward audit-final's **"clean bill"** section (the end of
  `audit-final-whole-codebase-findings.md`) so proven-sound ground —
  `TableSource` goroutine lifecycle, the batched multi-column verify scan,
  `canonicalJSON`'s `big.Rat` path, `normalizeBlobValue`, the FK grouping
  in `schema.go`, `deriveDatabaseName`, `infer_foreign_keys.go` — is not
  re-tread unless it changed since `db35d39`.

**Output:** findings-only report to
`docs/superpowers/plans/audit-cycle3-diff-review-findings.md`, one finding
per item (file, line, concrete triggering input, observable wrong
behavior, severity), plus an explicit clean-bill section for coverage.
Every finding filed as a GitHub issue before any Phase E fix work starts.

---

## Phase B — Load-test campaign

**Regression gate.** Re-run `scripts/verify-all-fixtures.sh` against the
existing full local set (17 `testdata/fixtures/` + 25 `../more data/` +
`beets_library.db`). Confirm no regression: the clean-database count does
not drop below the cycle-2 baseline (35 verified clean) and the
`migrate verify` failure count stays 0.

**New purpose-built fixtures** — cycle 2 noted that findings #60/#61
(verify false-FAIL on a transformed PK / on a `jsonb` column) never fired
in the campaign because no database in the corpus has the triggering
column shape. Build them:

- A DB with a `VARCHAR(N)`-declared **text primary key** and at least two
  `VARCHAR` columns of differing N, plus a TEXT-PK-→-`integer`-via-transform
  case. Exercises #60 and audit-final's #77 (`isTextTargetType` /
  `COLLATE "C"` drop).
- A DB with a `jsonb`/geojson-shaped text column. Exercises #61
  (raw source text vs. Postgres-canonicalized `jsonb`).

Check both into `testdata/fixtures/` with golden-test coverage, and add
them to the campaign DB list.

**Curated pull from sqlite.org/test-dbs**
(`https://sqlite.org/test-dbs/dir?ci=tip`) — only files not already
present locally. Download into `../more data/` (not checked in), add to
the campaign list:

| File | Why |
|---|---|
| `TPC-H-small.db` | A schema shape (wide fact/dimension tables, `DECIMAL` money columns) not in the current corpus |
| `random-json.db` | Exercises the `jsonb` comparison path at volume |
| `kjvbible-u8.db` + `kjvbible-u16be.db` | UTF-8 vs UTF-16BE text encoding — an encoding edge the corpus doesn't cover |
| `manyblobs-4k.db` + `manyblobs-512.db` | BLOB volume / `bytea` handling under many-row load |
| `multilinetext.db` | Embedded newlines through COPY's text protocol |
| `corrupt/` set | Graceful-degradation behavior on malformed input — should fail cleanly, not panic |

Record the same per-database results table the prior campaigns produced;
write it to `docs/superpowers/plans/audit-cycle3-campaign-results.md`.
Distinguish genuine bugs from the review gate working correctly (the
"rubber-stamp casualty" pattern — see `verify-all-fixtures-script`
memory).

---

## Phase C — Property/fuzz tests

**Regression gate.** Re-run the four existing fuzz files
(`internal/pipeline/verify_load_fuzz_test.go`,
`internal/sqlitereader/collation_fuzz_test.go`,
`internal/copywriter/transform_fuzz_test.go`,
`internal/ddl/identifiers_fuzz_test.go`) — they carry checked-in corpus
repros for #65 and #70; confirm they still pass.

**New targets** for code that changed since cycle 2:

- `readAnswerWithDeadline` — the byte-driven `gotAnswer` logic
  (PR #101). Fuzz scripted-stdin inputs (empty line, immediate EOF,
  partial line, whitespace-only, multi-line) against the
  "answer given" vs "no answer" classification.
- `columnListOpenParen` / `parseColumnCollations` — fuzz `CREATE TABLE`
  / `CREATE VIRTUAL TABLE` statement text, including a table literally
  named with a paren (audit-final's L5), for correct first-paren
  location.
- The #81 TUI integer-preview path — property test that a large
  `int64` (`|n| > 2^53`) renders in the review UI without the `float64`
  round-trip changing its digits.

Findings triaged with everything else in Phase E, not a separate track.

---

## Phase D — Performance regression check

Standing item. Time `migrate profile` / `load` / `verify` against
`employee.db` (228 MB) and `beets_library.db` (1.4 GB), at `db35d39` vs
`main` tip, 3 runs each for `profile`, 2 each for `load`/`verify`, median
reported. Method per issue #55's original discovery.

Watch specifically:

- `9cfec0c` — "batch VARCHAR-widening length checks into one scan per
  table." Same class of change as #55's regression (a per-column scan);
  confirm it batched rather than multiplied.
- The `resolver.Decide` churn (`9d399a1`) — confirm no added per-column
  work in the hot path.

Write-up to `docs/superpowers/plans/audit-cycle3-performance-results.md`.

---

## Phase E — Triage and fix (decide per-finding)

1. **File every finding as a GitHub issue** — unconditional, before any
   fix work, same paper-trail practice as all prior cycles.
2. **Consolidated triage.** Present the full findings list (Phase A–D
   results **plus issue #81**) with a recommended **fix / defer**
   disposition and rationale per item. Decide with Barrett which land
   this cycle. This is the deliberate departure from cycles 1–2's
   "fix everything found."
3. **For each finding that lands:** TDD cycle — failing test → minimal
   fix → real-Postgres integration test (`go test -tags integration`) →
   one commit per issue. Feature branch + worktree isolation.
4. **PR batching.** Group fixes by theme/severity (confirm the exact
   grouping with Barrett once the findings are in), scaffolding-only
   changes (docs, new fixtures, fuzz files) merging first as their own
   PR so each fix PR's diff is purely the fix. Avoids the
   same-two-functions-fixed-three-times pattern.
5. **Copilot review on every fix-batch PR** before merge — it found
   real, independently-confirmed bugs on every batch it was run against
   in cycles 1–2. Treat its findings with the same
   verify-against-the-code rigor as any review comment; don't blindly
   implement.
6. **Real-Postgres regression check after each fix batch.** Rebuild
   `bin/migrate`, re-run `scripts/verify-all-fixtures.sh` against the new
   build, confirm the clean count doesn't drop and verify failures stay
   0. Save the results table to
   `docs/superpowers/plans/audit-cycle3-<batch>-regression.md` and link
   it from the PR body. Use `Closes #N` / `Fixes #N` in every PR body
   (see `pr-issue-closing-keywords` memory).

Postgres setup for this work: see `postgres-path-setup` memory
(`/opt/homebrew/opt/postgresql@18/bin` on `PATH`).

---

## Results

Worktree: `.claude/worktrees/audit-cycle-3`, branch
`worktree-audit-cycle-3`. Baseline before any work: `go build ./...`
clean, `go test ./...` and `go test -tags integration ./...` all green.

*(Phase results appended here as the cycle runs.)*
