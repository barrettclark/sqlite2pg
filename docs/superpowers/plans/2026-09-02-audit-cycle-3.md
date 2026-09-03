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
clean, `go test ./...` (11 packages) and `go test -tags integration
./...` (11 packages) all green.

### Phase A — whole-codebase fresh-eyes review — complete (2026-09-02)

Fresh `general-purpose` subagent on `model: opus`, no prior context,
read-only, `internal/` + `cmd/` + `.goreleaser.yaml` + `.github/` +
`scripts/`. Full report:
`docs/superpowers/plans/audit-cycle3-diff-review-findings.md`.

**20 findings — 1 High / 6 Medium / 13 Low.**

| # | finding | sev | area |
|---|---|---|---|
| H1 | The H3/M7 "type-switch fall-through" remediation fixed 5 of 9 sibling arms — `iso8601_to_timestamptz`, `dayfirst_to_timestamptz`, `numeric_text_to_integer/double` still `return raw, nil`. Full-table verify is a dead no-op for the most common non-midnight `DATETIME` shape (Consequence A); a rare non-string row crashes mid-COPY on a profiler-auto-approved column (Consequence B). | High | `copywriter/transform.go` |
| M1 | `matchingParen` is quote- and comment-blind, so `parseColumnCollations` truncates the column body and reports a `COLLATE NOCASE` PK as `BINARY` → whole-table false verification failure (H1's chain, different door). | Medium | `sqlitereader/collation.go` |
| M2 | The TUI type picker's audit-final-M1 fix attaches `numeric_text_to_integer` to a column whose raw values are `float64` — the transform is a no-op there and COPY still fails. M1 only partially closed. | Medium | `tui/logic.go`, `tui/typepicker.go` |
| M3 | `resolver.Decide`'s "genuine disagreement" gate only inspects the top two findings, so an agreeing top pair suppresses review for a disagreeing third. Latent (no registered heuristic pair emits identical type+transform), but the fix opens it in the same motion. | Medium | `resolver/confidence.go` |
| M4 | The release workflow runs no tests and CI never runs on tags — `git push --tags` publishes binaries + a Homebrew formula with zero verification. Plus a floating `~> v2` GoReleaser pin and a deprecated `brews:` key. | Medium | `.github/workflows/`, `.goreleaser.yaml` |
| M5 | The Homebrew formula installs a binary named `migrate`, colliding with homebrew-core's `migrate` (golang-migrate) — `brew link` fails, or the tools silently shadow each other. Needs a binary-name decision. | Medium | `.goreleaser.yaml` |
| M6 | `FKsApplied` is all-or-nothing, so an FK/index step that fails partway leaves `--resume` permanently broken (re-adds an existing constraint, aborts). The feature H2 was filed to fix, still broken for this mode. | Medium | `cmd/migrate/main.go`, `state.go` |
| L1 | `julianDayToDate` overflows int64 on intermediates; `floorDiv`'s comment claims the opposite. | Low | `copywriter/transform.go` |
| L2 | `unix_epoch_*` guard int64's range but not `time.Time`'s — a far-out-of-range epoch wraps to an arbitrary instant, verify recomputes the same wrap and reports a match. | Low | `copywriter/transform.go` |
| L3 | `dateTransformPreview`'s `int64(f)` runs on `±Inf` / past-2^63 values (the one float→int the PR #98 guard round missed). Latent. | Low | `tui/logic.go` |
| L4 | `columnListOpenParen` mis-locates the paren for a doubled-quote table name (`"foo""(bar"`), and returns the module arg list for `CREATE VIRTUAL TABLE`. | Low | `sqlitereader/collation.go` |
| L5 | `verify-all-fixtures.sh` traps `INT`/`TERM` without exiting — Ctrl-C tears down the run's state and DBs, then the script keeps running. ("Failure destroys the evidence" shape.) | Low | `scripts/verify-all-fixtures.sh` |
| L6 | The campaign script prints a `results table:` path inside `$WORK_DIR`, which the EXIT trap `rm -rf`s moments later on a default run. | Low | `scripts/verify-all-fixtures.sh` |
| L7 | `varchar` widening can produce `varchar(20000000)`, exceeding Postgres's `varchar(n) <= 10485760` cap → `CREATE TABLE` fails. No clamp, no `text` fallback. | Low | `pipeline/profile.go` |
| L8 | `.goreleaser.yaml`'s `before` hook runs `go mod tidy` against the network at release time — the published binaries can be built from a dependency set no test ran against. | Low | `.goreleaser.yaml` |
| L9 | `MaxTextLength` (singular) is dead outside its own tests — exported API with a different NUL/BLOB contract than its batched sibling and no caller to keep them honest. | Low | `sqlitereader/text_length.go` |
| L10 | The bare-invocation usage string omits `run`, a real subcommand and the primary end-to-end entry point — now also baked into the Homebrew formula's smoke test. | Low | `cmd/migrate/main.go` |
| L11 | `determineVerify`'s piped-stdin branch never prints the prompt it is reading an answer for. | Low | `cmd/migrate/postload_verify.go` |
| L12 | `epochToInt64` accepts `int64`/`int`/`float64`; `toFloat64` also accepts `float32` — the PR #98 round matched their coverage for `int` and then left `float32` in only one. Latent. | Low | `copywriter/transform.go` |
| L13 | `numericSortKey` gives two NaN `float64`s the same sort key while `exactNumericEqual` reports them unequal — the documented `sortKeyFor` invariant broken in the harmless direction; `compareColumnUnordered` compares keys only so no wrong verdict today. | Low | `pipeline/verify_load.go` |

Clean bills (re-verified against the changed code, not taken on trust):
the `verify_load.go` numeric quartet (intact but for L13's NaN edge), the
`[]byte`-in-text-column fix (#83), `isTextTargetType`/`COLLATE "C"` (H1's
own fix), `9cfec0c`'s batched VARCHAR scan (genuinely one query per
table), the M5/#84 no-transform fit gate, `readAnswerWithDeadline` /
`determineVerify` (all six scripted-stdin shapes walked, L8-audit-final
correctly fixed), `markTableCompleted` vs a mid-table COPY failure (H2's
fix), `boolean01.go`'s comment fix, `resolver.Decide`'s selection loop
(the defect is the gate's scope, M3, not the selection), the `jsonb`
picker arm + `text_to_jsonb`, and `cmd/migrate`'s config/state lifecycle
(#62's fix). Full list in the report.

### Phase B — full load-test campaign — complete (2026-09-02)

`scripts/verify-all-fixtures.sh` over 17 `testdata/fixtures/` + 27
`../more data/` + `beets_library.db` (profile-only, 1.4 GB). Full table:
`docs/superpowers/plans/audit-cycle3-campaign-results.md`.

**36 verified clean, 0 `migrate verify` failures.** No regression — every
loaded database verified with zero mismatches (incl. `employee.db` 3.92M
rows, `rt5i.db` 1.17M rows). The 8 non-passing loads are all accounted
for: 6 are the rubber-stamp script force-accepting a correctly-flagged
`needs_review` column (cycle-2 casualties, incl. the post-#69
`DisabilityCompByCounty` `FIPS code` flag now firing *deterministically*),
1 is the known `ssb-small.db` source-data FK violation, and 1 is the new
`corrupt001.db` failing **cleanly** (`error: … database disk image is
malformed`, exit 1, no panic).

New databases: `manyblobs-512.db` (PASS), `corrupt001.db` (clean failure).
Already-present sqlite.org DBs re-confirmed: `random-json.db`,
`kjvbible-u8/u16be.db`, `TPC-H-small.db`, `multilinetext.db`,
`manyblobs-4k.db` all PASS.

Phase A's H1 (Consequence A) and M1 did not fire — no corpus database has
the triggering column shape (same as cycle 2 with #60/#61). The two
purpose-built fixtures the plan calls for were **not** built in this
detection pass; they are regression coverage for the Phase E fixes and
are best authored failing-test-first alongside them.

### Phase C — property/fuzz — complete (2026-09-02)

Full write-up: `docs/superpowers/plans/audit-cycle3-fuzz-results.md`.

**Regression gate green.** All nine existing fuzz targets pass their seed
corpora and a 45 s `-fuzz` burst each (~11 M execs total), no new
failures, cycle-2's `knownIssue65Gap` / empty-name exemptions intact.
`FuzzParseColumnCollations` still shows high "new interesting" churn —
consistent with M1/L4's untested parser edges; worth a long dedicated run
once those are fixed.

New fuzz targets (`readAnswerWithDeadline`, `columnListOpenParen`, the #81
TUI preview path) deferred to Phase E — Phase A already found the bugs
they'd exercise analytically (L4, M1, M2/L3/#81); `readAnswerWithDeadline`
got a clean bill. They belong as the failing tests for those fixes.

### Phase D — performance regression check — complete (2026-09-02)

`profile` at `db35d39` vs `main` tip, 3 runs each, `employee.db` +
`beets_library.db`; `load`/`verify` on tip, `employee.db`, 2 runs. Full
write-up: `docs/superpowers/plans/audit-cycle3-performance-results.md`.

**No #55-class regression.** One minor, explained slowdown:
`employee.db` `profile` 4710 → 5248 ms median (**+11 %**, ≈ +540 ms),
attributable to two intentionally-added correctness scans (`9cfec0c`'s
per-table VARCHAR-widening scan and the #69/#84 no-transform full-table
fit check), each properly batched to one pass per *qualifying* table.
`beets_library.db` `profile` is not a clean A/B (cold vs warm page cache);
the honest read is "no catastrophic regression," not the apparent 2×
speedup. `verify` at 3.9M rows in ~14 s clean. Recommend: accept the
`profile` delta, or re-measure cold-cache if a tighter number is wanted.

### Phase E — triage complete, execution PENDING (2026-09-03)

All 20 Phase A findings filed as GitHub issues **#103–#122** (H1→#103,
M1→#104, … L13→#122; plus pre-existing **#81** for the audit-final-M2 TUI
precision bug). Dispositions per Barrett:

- **M5 (#108):** rename the binary `migrate` → `sqlite2pg` (build output,
  `.goreleaser.yaml`, `verify-all-fixtures.sh`, usage string, README,
  test harnesses). The `cmd/migrate/` package dir may move or stay.
- **M3 (#106):** document only — a code comment at the gate + a guard test
  pinning current behavior. No logic change.
- **L9 (#118), L12 (#121), L13 (#122):** fix opportunistically inside
  whichever batch already touches that file.
- **Phase D `profile` +11 %:** accept (two intentionally-added, properly
  batched correctness scans), unless a cold-cache re-measure is wanted.

**PR batches** (each: own branch off latest `main` / off the scaffolding
PR until it merges; TDD per issue — failing test → minimal fix →
real-Postgres integration test → one commit; Copilot review before merge;
`verify-all-fixtures.sh` regression run + results doc per batch;
`Closes #N` in the PR body):

| PR | scope | issues |
|---|---|---|
| **0 — scaffolding** | the Phase A–D deliverables: 5 results docs, the two new `../more data/` DBs are not checked in so nothing there; no production code | — |
| **1 — `transform.go`: finish the switch** | give `iso8601_to_timestamptz` / `dayfirst_to_timestamptz` / `numeric_text_to_integer` / `numeric_text_to_double` the explicit type-switch + erroring `default:` the 5 siblings got; JDN + epoch clamps; `float32` coverage match; new fuzz target for the timestamptz arms; purpose-built non-midnight-`DATETIME` fixture | #103 #110 #111 #121 |
| **2 — collation parser** | quote/comment-aware `matchingParen`; doubled-quote `leadingIdentifier`; no-column-list for `CREATE VIRTUAL TABLE`; delete dead `MaxTextLength`; new fuzz target + long dedicated run | #104 #113 #118 |
| **3 — TUI preview path** | attach a transform that handles the sample's real storage class; `math.IsInf`/range guard before `int64(f)`; the #81 integer-preview float64 fix; property test | #105 #112 #81 |
| **4 — `--resume` FK state** | per-constraint/index state or `IF NOT EXISTS` + skip-on-exists, so a partial FK step resumes | #109 |
| **5 — release / packaging** | binary rename → `sqlite2pg`; gate the tag/release path on the test suite; pin the GoReleaser action to an exact version; migrate off the deprecated `brews:` key; drop `go mod tidy` from the release `before` hook (+ CI tidiness check); add `run` to the usage string | #108 #107 #117 #119 |
| **6 — script + small hygiene** | `trap 'cleanup; exit 130' INT TERM` + separate EXIT trap; write the results table outside `$WORK_DIR`; clamp widened `varchar(N)` to Postgres's limit / fall back to `text`; echo the verify prompt on the piped path; M3 comment + guard test; NaN sort-key fix | #114 #115 #116 #120 #106 #122 |

**Not yet started.** No fix commits, no branches, nothing merged.
