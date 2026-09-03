# Audit cycle 3 — Phase A fresh-eyes correctness review

Read-only pass over every non-test file in `internal/` and `cmd/`, plus
`.goreleaser.yaml`, `.github/workflows/`, and `scripts/` — the release/CI
surface no prior cycle has reviewed. Nothing was modified.

Concentrated on the 63 commits merged since `db35d39` (PRs #95–#102): the
#77–#94 fixes themselves, PR #100–#101's `readAnswerWithDeadline` /
`columnListOpenParen` / `resolver.Decide` rework, and PR #102's release
automation. Audit-final's clean bill was carried forward — see the coverage
section at the end for which parts were re-verified against the new code and
which were taken on trust as unchanged.

Findings are ordered by severity. Each names the concrete triggering input
and the observable wrong behavior.

---

## High

### H1. The H3/M7 "type-switch fall-through" remediation fixed 5 of 9 sibling transform arms — `iso8601_to_timestamptz`, `dayfirst_to_timestamptz`, `numeric_text_to_integer` and `numeric_text_to_double` still `return raw, nil`

`internal/copywriter/transform.go:174-178`, `:338-342`, `:355-359`, `:376-380`

```go
case "iso8601_to_timestamptz":
	s, ok := raw.(string)
	if !ok {
		return raw, nil          // <-- non-string passes straight through
	}
```

PR #97/#98's transform round rewrote `strip_commas`, `strip_commas_float`,
`text_to_jsonb`, `nullif_sentinels` and `iso8601_to_date` into full type
switches with an explicit `default: return nil, fmt.Errorf(...)` arm, each
carrying a comment that names the defect class precisely ("the `raw.(string)`
type assertion's `!ok` branch is a silent pass-through in a transform whose
paired heuristic does not guarantee string-only values"). Four sibling arms in
the same `switch` were left exactly as audit-final found them.

The most consequential is `iso8601_to_timestamptz`, the **direct sibling of
the arm H3 was filed against** — both are assigned by the same heuristic,
`ISO8601.Evaluate` (`internal/profiler/heuristics/iso8601.go:22-75`), which
picks `iso8601_to_date` when every sample is midnight and
`iso8601_to_timestamptz` otherwise. H3's own analysis established that
`modernc.org/sqlite` scans a `DATE`/`DATETIME`/`TIMESTAMP`-declared column
straight into `time.Time`, and `ISO8601.Evaluate` explicitly handles that at
`iso8601.go:30-37`. So:

- **Consequence A (dead guard, always).** For any `DATETIME`-declared column
  with a non-midnight sample — the overwhelmingly common timestamp shape —
  the winning decision is `timestamptz` + `iso8601_to_timestamptz`, every
  streamed row arrives as `time.Time`, and the transform returns `raw, nil`
  for every one of them. `verifyTransformAgainstFullTable`
  (`internal/pipeline/verify_transform.go:107-115`) sees no error, and
  `fitsTargetType` (`:205-212`) has no opinion on a `time.Time`. The
  full-table check therefore **cannot ever fail** for this column — the exact
  "the transform can never fail, so full-table verification is a silent
  no-op" defect issues #22 and #42 were filed for, and that the brief named as
  the shape to hunt for. Issue #13's whole guard is dead for the most common
  timestamp column in the corpus.

- **Consequence B (load-time failure on a promised-safe column).**
  `ISO8601.Evaluate` `continue`s past a sample that is neither `string` nor
  `time.Time` (`iso8601.go:38-41`) rather than disqualifying the column — the
  same tolerance that made `text_to_jsonb`/`strip_commas` reachable with
  non-string values. So a TEXT column of ISO strings with one `int64` or BLOB
  row outside the 500-row sample gets `iso8601_to_timestamptz`, that row
  passes through raw, full-table verification reports the column clean, and
  pgx then fails mid-COPY trying to encode an `int64`/`[]byte` into
  `timestamptz`.

The same two consequences apply to `dayfirst_to_timestamptz` (`:376-380`) and
to `numeric_text_to_integer`/`numeric_text_to_double` (`:338-342`, `:355-359`).
`numeric_text_to_double` is the worst of that pair: `NumericText` suggests
`double precision`, an unsampled `[]byte` row passes through raw, and
`fitsTargetType`'s `asInt64` (`verify_transform.go:224-232`) has no `[]byte`
case, so nothing catches it before COPY. `numeric_text_to_integer` is
half-covered — an `int64` fall-through is at least range-checked by
`fitsTargetType` — but a `float64` fall-through is not (`asInt64` has no
`float64` case), which is precisely the gap M7 documented for `strip_commas`
and that `strip_commas` was then fixed for while this arm was not.

Trigger (Consequence A, guaranteed): any SQLite table with a `DATETIME`- or
`TIMESTAMP`-declared column whose sampled values are not all midnight. Trigger
(Consequence B): the same column shapes M7 already established as reachable —
a TEXT column with a rare non-string storage-class row.

This is the project's named *same-two-functions / three-fixes* shape in its
clearest form: one commit hardened `iso8601_to_date` and left the arm 15 lines
above it, assigned by the same heuristic, untouched.

Severity: **High** (a pre-flight correctness guard that the codebase's own
comments claim is active is provably inert for the most common timestamp
column shape, plus a reachable mid-COPY failure on a column the profiler
already auto-approved).

---

## Medium

### M1. `matchingParen` is quote- and comment-blind, so `parseColumnCollations` can truncate the column body and silently report a `COLLATE NOCASE` primary key as `BINARY`

`internal/sqlitereader/collation.go:158-172`, reached from `:96-100`

```go
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
```

PR #101's L5 fix (`columnListOpenParen`) corrected where the column-definition
list *starts*. Nothing corrected where it *ends*. `splitTopLevelCommas`
(`:179-210`) carefully tracks all four quoting styles; `matchingParen`, called
immediately before it on the same text, tracks none of them and has no
handling for SQLite's `--` line comments (which `sqlite_master.sql` preserves
verbatim from the original DDL).

Two concrete triggers, both producing the same wrong result:

```sql
-- unbalanced paren inside a string literal
CREATE TABLE t (note TEXT DEFAULT ')', id TEXT PRIMARY KEY COLLATE NOCASE);
-- unbalanced paren inside a line comment
CREATE TABLE t (
  note TEXT,          -- free-form (see the import notes
  id   TEXT PRIMARY KEY COLLATE NOCASE
);
```

In the first, `matchingParen` closes on the `)` inside `')'`, so
`body == "note TEXT DEFAULT '"` and `id`'s `COLLATE NOCASE` is never seen. In
the second, depth never returns to 0, `close` falls back to `len(createSQL)`,
and `splitTopLevelCommas` then folds `id`'s definition into the comment's part
whose leading identifier is `--`, which `ColumnCollations`'s known-column
filter (`:57-61`) discards. Either way `ColumnCollations` reports `id` as
`BINARY`.

Consequence chain: `primaryKeyOrderingIsSafe`
(`internal/pipeline/verify_load.go:251-268`) sees BINARY + no transform and
returns true → `verifyTableOrdered` runs and emits `ORDER BY "id" COLLATE "C"`
→ Postgres walks byte order while SQLite walks NOCASE order → the two sides
compare different rows by position and **every column of the table
mismatches**. This is H1's exact consequence chain, reached through a
different door; it is also the failure the `ColumnCollations` mechanism exists
solely to prevent.

Severity: **Medium** (whole-table false verification failure, but requires the
conjunction of an unbalanced paren in a string/comment *and* a NOCASE/RTRIM
primary key).

### M2. The TUI type picker's M1 fix attaches `numeric_text_to_integer` to a column whose raw values are `float64`, where that transform is a no-op — the COPY still fails

`internal/tui/logic.go:213-243`, consumed by `commonTransformForType`
(`:369-389`) and `onTypeSelected` (`internal/tui/typepicker.go:120-146`)

Audit-final's M1 said the picker "promises a type the real COPY rejects" for
`integer`/`bigint`/`smallint`. The fix routes the preview through
`copywriter.Transform("numeric_text_to_integer", value)` and attaches that
transform. But `previewValueForType` only ever receives the **display string**
from `review.formatSampleValue`, and `numeric_text_to_integer` is
string-only — its non-string arm is the pass-through H1 above documents.

Trigger: a `NUMERIC`/`INTEGER`-declared column that SQLite stores with REAL
storage class for whole-number values (1.0, 2.0, 3.0 — ordinary SQLite dynamic
typing, and the exact mixed-storage shape `fallbackTypeFor`'s doc comment at
`internal/pipeline/profile.go:327-331` says it sees in practice). `%v` renders
`float64(3)` as `"3"`, so the preview validates, displays `3`, and reports
`transform == "numeric_text_to_integer"`. At COPY time the raw value is
`float64(3)`, `raw.(string)` fails, the transform returns it unchanged, and
pgx cannot binary-encode a `float64` into `int4`/`int8`.

The behavior is no worse than before the fix (which attached `""`), but the
fix's own comment asserts the stronger property that is now false: "Any type
this validates for must always carry the transform that actually makes it
work." `strip_commas` was given `int64`/`int`/`float64` arms in the same
review round for exactly this reason; `numeric_text_to_integer` was not.

Severity: **Medium** (the tool's flagship review interaction still hands the
user a type whose load fails; M1 is only partially closed).

### M3. `resolver.Decide`'s "genuine disagreement" gate only inspects the top two findings, so an agreeing top pair now suppresses review for a genuinely disagreeing third

`internal/resolver/confidence.go:99-117`

```go
if gap <= confidenceHundredths(disagreementMargin) &&
	(best.SuggestedType != secondBest.SuggestedType || best.TransformExpr != secondBest.TransformExpr) {
	return best, true
}
```

`secondBest` is selected purely by confidence (`:84-91`), with no preference
for a finding that actually disagrees with `best`. Before `9d399a1`, a gap of
≤ 0.02 to *any* second-place finding forced review. Now, if the top two agree
on `(SuggestedType, TransformExpr)`, the whole gate is skipped — including for
a third finding sitting inside the margin that disagrees outright.

Trigger: findings `{A: boolean/int_to_bool 0.90, B: boolean/int_to_bool 0.90,
C: integer/numeric_text_to_integer 0.89}`. `best = A`, `secondBest = B`, they
agree → auto-approve at 0.90, with C's genuine disagreement never considered.
Under the old code this forced review.

This is latent today for the same reason L1 was: no currently-registered
heuristic pair emits identical `(SuggestedType, TransformExpr)`, so the
agreeing-top-two branch is unreachable. But that is exactly the condition the
fix was written to prepare for — the moment it becomes reachable, the fix
opens this hole in the same motion.

Severity: **Medium** (latent; the fix for a latent over-escalation introduced a
latent under-escalation in the same function, and under-escalation is the
worse direction for a data-integrity tool).

### M4. The release workflow runs no tests, and CI never runs on tags — a `v*` tag publishes binaries and a Homebrew formula with zero verification

`.github/workflows/release.yml:3-6`, `.github/workflows/ci.yml:3-7`

CI triggers on `push: branches: [main]` and `pull_request`. Release triggers on
`push: tags: ["v*"]`. Tag pushes match neither CI trigger, and the release job
runs `goreleaser release --clean` with no `go test`/`go vet` step of its own.

Trigger: `git tag v0.2.0 && git push --tags` on any commit — including one
never merged through a PR, or a commit whose tests were green months ago
against different dependency versions. GoReleaser builds, publishes a GitHub
release, and pushes a Homebrew formula to `barrettclark/homebrew-tap`, all
without executing a single test.

Compounding this, `.github/workflows/release.yml:26` pins the GoReleaser
action to `version: "~> v2"` — a floating constraint that resolves to the
newest v2 at run time — while `.goreleaser.yaml:47` uses the `brews:` block,
which GoReleaser v2 deprecated in favor of `homebrew_casks`. A deprecated key's
removal in a later v2 release breaks the release pipeline with no change to
this repository, and the first sign of it will be a failed (or, worse,
formula-less) release.

Severity: **Medium** (a broken build reaches users' `brew install`; the
floating action version makes the failure mode time-dependent rather than
reproducible).

### M5. The Homebrew formula installs a binary named `migrate`, which collides with homebrew-core's own `migrate` formula

`.goreleaser.yaml:47-59`

```yaml
brews:
  - name: sqlite2pg
    install: |
      bin.install "migrate"
```

The formula is `sqlite2pg` but the executable it links into the Homebrew prefix
is `bin/migrate`. homebrew-core ships a formula literally named `migrate`
(golang-migrate) that links `bin/migrate` to the same path.

Trigger: a user with golang-migrate installed runs `brew install
barrettclark/tap/sqlite2pg`. Homebrew's link step fails with `Could not
symlink bin/migrate — Target /opt/homebrew/bin/migrate already exists`,
leaving the formula installed but unlinked. In the reverse order, the newly
linked `migrate` silently shadows whichever tool the user reaches for. Nothing
in the repo warns about this, and the formula's own `test` block invokes
`#{bin}/migrate` — which passes in the sandbox and so never surfaces the
conflict.

Severity: **Medium** (packaging defect that breaks installation for a
plausible fraction of the target audience; also the reason the binary name
deserves a decision rather than an inheritance from `cmd/migrate`).

### M6. `FKsApplied` is all-or-nothing, so a foreign-key step that fails partway leaves `--resume` permanently broken

`cmd/migrate/main.go:663-693`, `cmd/migrate/state.go:18-28`

The FK/index step runs every `GenerateForeignKeyConstraints` statement in a
loop, then sets `FKsApplied` once at the end. There is no per-constraint state,
and the emitted DDL is a bare `ALTER TABLE ... ADD CONSTRAINT` /
`CREATE INDEX` with no `IF NOT EXISTS`.

Trigger: a load where every table COPY succeeds and the FK loop adds three of
five constraints before failing (an FK violation from inferred keys, a lock
timeout, a dropped connection). State on disk: all tables in `Completed`,
`FKsApplied: false`. Every subsequent `migrate load --resume` skips all tables,
re-enters the `!st.FKsApplied` branch, and aborts on the *first* constraint
with `constraint "..." for relation "..." already exists`. The run can never be
completed with `--resume`.

`loadState.FKsApplied`'s own doc comment claims this flag makes the step
"idempotent across separate `migrate load --resume` invocations"; it is
idempotent only across the all-or-nothing boundary, which is the one case that
does not need it. This is the two-features/one-artifact shape: the per-table
resume contract (each unit of work individually recorded) and the FK step
(recorded as one unit) disagree about what the `.state.json` is for.

Severity: **Medium** (`--resume`, the feature H2 was filed to make functional,
is still non-functional for this failure mode).

---

## Low

### L1. `julianDayToDate` overflows int64 on intermediates, and its comment asserts the opposite

`internal/copywriter/transform.go:728-751`, guarded at `:317-325`

The transform's guard rejects only NaN/±Inf and values outside int64's range,
then calls `julianDayToDate(int64(jd))`. Inside, `p := jdn + 68569` overflows
for `jdn` near `MaxInt64`, and `4*p`, `146097*q`, `4000*(r+1)` and `1461*s`
overflow for `|p|` past roughly 2^61. `floorDiv`'s doc comment claims it "keeps
every intermediate exact for the full int64 range", which is not true of the
multiplications feeding it.

Trigger: a JDN-suggested column (all 500 samples inside
`[1721425.5, 2816787.5]`, per `julian_day.go`) with one unsampled row of e.g.
`5e18`. The value clears the range guard, `julianDayToDate` produces garbage
`year`/`month`/`day`, `time.Date` normalizes it into an arbitrary instant, and
neither the transform nor `fitsTargetType` reports anything — the same
silent-wrong-timestamp class M3 was filed for and `excelSerialToTime` was
given `clampDaysToInt` to avoid. `julianDayToDate` got no equivalent clamp in
the same round.

Severity: **Low** (`JulianDay.Evaluate` requires *every* sample in range, so
reachability is much narrower than M3's 50% threshold).

### L2. `unix_epoch_*` guard int64's range but not `time.Time`'s, so a far-out-of-range epoch still wraps to an arbitrary instant

`internal/copywriter/transform.go:141-145`, `:156-160`, `:168-172`

The PR #98 round added `math.IsNaN(sec) || sec < -2^63 || sec >= 2^63` before
`time.Unix(int64(sec), nanos)`. `time.Time` stores seconds since year 1 in an
int64 of its own, so `time.Unix(9e18, 0)` wraps rather than erroring. The
sibling `excelSerialToTime` was given `maxPlausibleExcelDays` clamping in the
same round precisely because "AddDate itself silently wraps/overflows"; the
epoch transforms stop one level short.

Trigger: `UnixEpoch.Evaluate` tolerates a minority of out-of-range values (only
50% must land in `[946684800, 2051222400]`), so a `_at`-suffixed INTEGER column
mixing epoch seconds with a large garbage/sentinel value produces a wrapped
`time.Time` with no error. `migrate verify` recomputes the same wrapped value
on both sides and reports a match.

Severity: **Low** (needs an implausibly large value in a mixed column; Postgres
will usually reject the resulting timestamp at COPY, making it loud rather than
silent).

### L3. `dateTransformPreview`'s `int64(f)` is the one float→int conversion in the codebase the PR #98 guard round did not reach

`internal/tui/logic.go:96-98`

```go
if f, err := strconv.ParseFloat(value, 64); err == nil && targetType == "timestamptz" && f == math.Trunc(f) {
	n := int64(f)
```

The L6 fix swapped `ParseInt` for `ParseFloat`, which now also accepts `"Inf"`
and any magnitude. `math.Trunc(±Inf) == ±Inf`, so `int64(f)` runs on `±Inf`
and on values past 2^63 — implementation-dependent per the Go spec, the exact
thing the same review round added explicit guards for in six places in
`transform.go`. No observable bug today (the saturated result falls outside
every epoch window on the platforms this targets), but it is a latent
platform-dependent branch in the picker's validity decision.

Severity: **Low**.

### L4. `columnListOpenParen` still mis-locates the paren for a doubled-quote table name, and returns the module argument list for `CREATE VIRTUAL TABLE`

`internal/sqlitereader/collation.go:141-153`

- SQLite spells an embedded `"` in a quoted identifier by doubling it. A table
  named `foo"(bar` is stored as `CREATE TABLE "foo""(bar" (...)`.
  `leadingIdentifier` (`:222-231`) stops at the *first* inner `"`, returning
  name `foo` and remainder `"(bar" (...)`, so `IndexByte(rest, '(')` finds the
  paren inside the name again — L5 reproduced for a doubled-quote name. Every
  column silently keeps BINARY, feeding the same consequence chain as M1.
- For `CREATE VIRTUAL TABLE docs USING fts5(title, body)`, the function returns
  the `fts5(` paren, so `parseColumnCollations` parses the module's argument
  list as a column-definition list. Harmless today (module arguments carry no
  `COLLATE` clause and the known-column filter discards the rest), but the
  function's contract says "the column-definition list", and a virtual table
  has none.

Severity: **Low** (both are obscure; the first is a genuine correctness hole,
the second a contract/documentation mismatch).

### L5. `scripts/verify-all-fixtures.sh` traps `INT`/`TERM` without exiting, so Ctrl-C tears down the run's state and then keeps running

`scripts/verify-all-fixtures.sh:112-118`

```bash
cleanup() {
    for db in "${CREATED_DBS[@]:-}"; do ... dropdb --if-exists "$db" ...; done
    if [ "$CLEAN_WORK" = "1" ]; then rm -rf "$WORK_DIR"; fi
}
trap cleanup EXIT INT TERM
```

`cleanup` does not `exit`. In bash, a trapped `SIGINT` runs the handler and then
**resumes** the interrupted script.

Trigger: Ctrl-C during a long campaign run. Every provisioned database is
dropped and `$WORK_DIR` (holding every config, log and verify report) is
`rm -rf`'d — and then the loop continues to the next database, writing configs
and appending to `$RESULTS_MD` inside a directory that no longer exists, so
every subsequent `emit_row` silently fails and the run must be Ctrl-C'd
repeatedly. The EXIT trap then re-runs `cleanup` a second time. Should be
`trap 'cleanup; exit 130' INT TERM` with a separate EXIT trap.

Severity: **Low** (tooling only, but it destroys exactly the evidence a
campaign operator interrupts a run to inspect — the same "a failure deletes
the artifact needed to investigate it" shape as issue #62).

### L6. The campaign script prints a path to a results file it is about to delete

`scripts/verify-all-fixtures.sh:283-287`. `cat "$RESULTS_MD"` runs first (so
the content is seen), but the final `echo "results table: $RESULTS_MD"` names a
file inside `$WORK_DIR`, which the EXIT trap `rm -rf`'s moments later unless
`KEEP_WORK`/`WORK_DIR` was set. The printed path is always dangling on a
default run.

Severity: **Low**.

### L7. `varchar` widening can exceed Postgres's own `varchar` length limit

`internal/pipeline/profile.go:199-206` and `:400-424`. `varcharFinding` now
widens the suggested `varchar(N)` to the table's actual longest value from
`MaxTextLengths`. Postgres caps `varchar(n)` at `n <= 10485760`; SQLite has no
such limit. A `VARCHAR(255)`-declared column holding one 20 MB value yields
`SuggestedType: "varchar(20000000)"`, and if the reviewer accepts it,
`CREATE TABLE` fails with `length for type varchar cannot exceed 10485760`. The
pre-widening code could not produce this (the declared N came from the source
schema). No clamp, and no fallback to `text`.

Severity: **Low** (needs a >10 MB single value in a `VARCHAR(N)` column).

### L8. `.goreleaser.yaml`'s `before` hook mutates `go.mod`/`go.sum` during the release

`.goreleaser.yaml:3-5`. `go mod tidy` runs against the network at release time.
If it resolves differently from what CI tested — a newly-published patch
release, a proxy hiccup, a `go.sum` entry it decides to drop — the published
binaries are built from a dependency set no test ever ran against, or the
release aborts. A release should build the tagged tree verbatim; `go mod tidy`
belongs in CI as a `git diff --exit-code` check, not in the release path.

Severity: **Low**.

### L9. `MaxTextLength` (singular) is dead outside its own tests

`internal/sqlitereader/text_length.go:19-30`. Only `MaxTextLengths` (plural) is
called from production code (`internal/pipeline/profile.go:174`); the singular
form exists solely for `text_length_test.go`. It is exported API with a
different NUL/BLOB-handling contract than its batched sibling and no caller to
keep the two honest.

Severity: **Low** (dead code, not a bug).

### L10. The bare-invocation usage string omits `run`

`cmd/migrate/main.go:40` — `usage: migrate <profile|review|load|verify|resolve> ...`.
`run` is a real, dispatched subcommand (`:43-44`) and is the primary
end-to-end entry point. It is also the string the Homebrew formula's `test`
block asserts against (`.goreleaser.yaml:63`), so the omission is now baked
into the published package's smoke test.

Severity: **Low** (documentation).

### L11. `determineVerify`'s non-terminal branch never prints the prompt it is reading an answer for

`cmd/migrate/postload_verify.go:85-92` vs. `:94`. The interactive path prints
`Run migrate verify now? [y/N]: `; the piped path reads stdin without printing
anything. A user running `migrate load < answers.txt` sees no record of what
was asked or answered — only the timeout message, and only when nothing
arrived.

Severity: **Low** (cosmetic).

### L12. `epochToInt64` accepts `int64`/`int`/`float64`; `toFloat64` also accepts `float32`

`internal/copywriter/transform.go:594-608` vs. `:626-645`. The PR #98 round
added `int` to `toFloat64` explicitly to keep the two helpers' coverage
matched ("the two helpers should have matched coverage regardless of which
call site exposed the gap"), then left `float32` in only one of them. So
`unix_epoch_seconds` accepts a `float32` while `unix_epoch_millis`/`micros`
reject it as "unexpected type". No driver in use produces `float32`, so this
is latent.

Severity: **Low**.

### L13. `numericSortKey` gives two NaN `float64` values the same sort key while `exactNumericEqual` reports them unequal

`internal/pipeline/verify_load.go:632-643` and `:578-590`. `math.NaN() !=
math.Trunc(math.NaN())` is true (NaN compares unequal to everything), so
`numericSortKey` falls to `numericKeyText`, which renders `"NaN"` — one shared
key. `exactNumericEqual` returns `e == a`, i.e. false. This is the documented
sortKeyFor invariant broken in the harmless direction (equal keys, unequal
values) rather than the harmful one, and `compareColumnUnordered`
(`:484-519`) compares keys only, so it currently cannot produce a wrong
verdict. Recording it because this is the invariant the brief flagged as
having broken three times, and it is now *not* exactly true.

Severity: **Low**.

---

## Clean bill — read carefully in this pass, nothing found

### Re-verified against the changed code (not taken on trust)

- **`verify_load.go`'s numeric-comparison quartet.** Re-derived the invariant
  from scratch after the `sortKeyFor` `[]byte` change: for whole, in-range `f`,
  `int64(f) == n` implies `float64(n) == f` exactly, so
  `int64EqualsFloat64`'s two conditions are equivalent to `numericSortKey`'s
  single one — the pairs `crossTypeNumericEqual` calls equal *are* exactly the
  pairs `numericSortKey` maps to a shared key. The 2^53/int64-range boundary
  is applied identically in both. `-0.0` vs `int64(0)` and `float64(-0.0)` vs
  `float64(0.0)` both agree in key and in comparison. Intact, except for the
  NaN edge recorded as L13.
- **The `[]byte`-in-a-text-column fix (issue #83).** `valuesMatch`'s new
  `case []byte: if a, ok := actual.(string)` arm and `sortKeyFor`'s move of
  `[]byte` into the `\x08string:` namespace are mutually consistent in the
  direction that is reachable (SQLite-side `[]byte`, Postgres-side `string`).
  The reverse (`expected string`, `actual []byte`) requires a `bytea` target
  fed a Go `string`, which pgx's bytea codec rejects at COPY, so it never
  reaches verify. `compareColumnUnordered` compares sort keys only, never
  `valuesMatch`, so the merge cannot introduce a false verdict.
- **`isTextTargetType` / `COLLATE "C"` (H1's fix).** The `varchar(` prefix test
  now matches the only varchar-shaped value `varcharFinding` emits;
  `newPgColumnScanner` correctly routes `varchar(N)` to its `pgtype.Text`
  default; `primaryKeyOrderingIsSafe`'s no-transform and BINARY-collation
  preconditions are both still checked before `COLLATE "C"` is emitted; `jsonb`
  is still excluded. Correct.
- **`profile.go`'s batched VARCHAR-widening scan (`9cfec0c`).** Confirmed it
  genuinely batched: `MaxTextLengths` is called **once per table**, before the
  per-column loop, with an early return when the column list is empty
  (`text_length.go:36-38`), and `varcharSuggestions` already restricts it to
  tables whose VARCHAR lengths actually vary. Exactly one extra query per
  qualifying table — the opposite of #55's per-column regression.
- **`decide_column.go`'s M5/#84 fix.** The new gate
  (`TransformExpr != "" || fallbackTargetNeedsStorageCheck(SuggestedType)`)
  and `CheckFallbackFit: best.TransformExpr == ""` are internally consistent
  with `verifyTransformsAgainstFullTable`'s `if s.Transform == "" &&
  !s.CheckFallbackFit` skip and its no-transform branch. No spec can be marked
  active and then silently skipped, and no column can be double-counted in
  `remaining` (the `results` membership check at `verify_transform.go:89`
  guards it). The `varchar(N)` half of M5 is closed differently, by widening at
  profile time rather than by a fit check — reasonable, with the L7 caveat.
- **`readAnswerWithDeadline` / `determineVerify` (PR #101).** Walked all six
  scripted-stdin shapes against the byte-count rule: `"y\n"` → answer, verifies;
  `"y"` with no newline then EOF → answer, verifies; `"\n"` → answer, trims to
  `""`, skips *without* the misleading "no answer" message (L8 correctly
  fixed); `"  \n"` → same; immediate EOF (`</dev/null`) → no answer, message
  printed, skips, matching the documented behavior; `"y\nn\n"` → first line
  wins. Basing `ok` on `line != ""` rather than on the error value is correct
  and matches the stated intent. The parked goroutine on the timeout path is
  one-shot and nothing else reads stdin afterward.
- **`markTableCompleted` ordering vs. a mid-table COPY failure (H2's fix).**
  The `to_regclass` probe passes a `pgx.Identifier{}.Sanitize()`d name as a
  *text parameter*, which `to_regclass` parses as a quoted identifier — correct
  and case-exact. The `EXISTS (SELECT 1 ... LIMIT 1)` emptiness probe, the
  `hasRows` → reconcile-state path, and the `!hasRows` → `DROP TABLE IF
  EXISTS` → recreate path are each right, and the underlying premise (a single
  COPY statement is its own implicit transaction, so nonzero rows imply a
  committed COPY) holds for pgx's `CopyFrom` including on an abrupt connection
  drop. `progress.skipAlreadyLoadedTable` uses the source-side count, keeping
  `done` consistent with `total` by construction. The `resume` gate on the
  probe is correct given `connectForLoad` always provisions a fresh database on
  a non-resume run.
- **`boolean01.go`'s stale-comment fix (`88b8f7e`).** Both comments now say
  `0.02` and describe the integer-hundredths comparison, matching
  `disagreementMargin` and `confidenceHundredths` exactly. Re-verified the full
  ladder against every `Confidence:` literal in
  `internal/profiler/heuristics`: 0.55, 0.85, 0.88, 0.90, 0.95, 0.99, with the
  smallest intentional clean-win gap at 0.03 and the intentional disagreement
  gap at 0.02 — `disagreementMargin = 0.02` still satisfies both. L2 correctly
  closed.
- **`resolver.Decide`'s top-two selection loop.** Re-walked it against ties,
  equal-to-best values, descending input, and the single-finding case; still
  correct. The defect is the gate's scope (M3), not the selection.
- **`columnListOpenParen`'s ordinary cases.** `CREATE TABLE t(a ...)` (no
  space), `CREATE TABLE "t" (...)`, `CREATE TABLE IF NOT EXISTS t (...)`,
  `CREATE TABLE main.t (...)`, `CREATE TABLE "foo(bar)" (...)` (the L5 case it
  was written for), and the regex-doesn't-match fallback all resolve to the
  correct offset. Only the two edges in L4 are wrong.
- **`internal/tui`'s L6/L7 fixes.** `dateTransformPreview`'s `ParseFloat` +
  `f == math.Trunc(f)` correctly accepts the `%v` scientific-notation form
  without admitting fractional values (L3 is the residual guard gap).
  `buildTableList`'s deferred `SetCurrentItem` correctly preserves selection
  and cannot index out of range; calling it from `onTypeSelected` is safe
  (`m.tableList` is non-nil by then, and `m.summary.Tables`' order is stable).
- **The `jsonb` picker arm and `text_to_jsonb`.** `previewValueForType`'s new
  `jsonb` case, `text_to_jsonb`'s always-return-a-`string` contract,
  `expectedForCompare`'s string-only canonicalization, and
  `pgColumnScanner.value`'s `targetType == "jsonb"` canonicalization all agree:
  both sides of a jsonb comparison go through `canonicalJSON`. The `[]byte`
  input case returning `string(v)` rather than `raw` is what keeps #61 closed
  for that input shape — verified, not assumed.
- **`cmd/migrate`'s config/state lifecycle.** `runRunFinish` keeps both the
  config and `.state.json` when post-load verification fails (issue #62's fix)
  and only cleans up on a clean pass; `runPostLoadVerify` runs against the
  in-memory `cfg`/`connCfg` before any cleanup, so it is independent of
  `--keep-config`. `cleanupConfigAfterLoad`'s "state file exists iff config
  exists" invariant holds, and `executeLoad` (the `migrate load` path) never
  calls it. The two-features/one-artifact hazard is correctly handled here.
- **`scripts/verify-all-fixtures.sh`'s state-file coupling.** The script's
  `statef="$cfg.state.json"` matches `cmd/migrate/main.go:420`'s
  `statePath := configPath + ".state.json"` exactly, so `created_db` extraction
  and the `dropdb` cleanup do work. Exit status (`fail_count == 0 &&
  error_count == 0` as the final command) is correct.

### Taken on trust — unchanged since `db35d39`, covered by audit-final's clean bill

`git diff db35d39..HEAD` shows no changes to any of these, so audit-final's
findings on them stand without re-derivation:

- `internal/ddl/` in full (`identifiers.go`, `generate.go`, `foreign_keys.go`,
  `foreign_key_indexes.go`).
- `internal/config/` in full.
- `internal/copywriter/pipeline.go` (the `TableSource` producer/consumer
  lifecycle) and `load.go`.
- `internal/sqlitereader/rows.go` (`normalizeBlobValue`), `schema.go` (FK
  grouping, implicit-`REFERENCES` resolution, the virtual-table skip),
  `esri.go`, `containment.go`.
- `internal/pipeline/infer_foreign_keys.go`.
- `cmd/migrate/provision.go`'s `deriveDatabaseName` and `cmd/migrate/verify.go`'s
  report generation.
- `internal/review/`, `internal/profiler/{dayfirst,heuristic,timestamp}.go`, and
  every `internal/profiler/heuristics/*.go` except `boolean01.go` (re-verified
  above) — though `iso8601.go`, `numeric_text.go`, `dayfirst_date.go`,
  `sentinel_null.go`, `comma_number.go` and `unix_epoch.go` were re-read in
  full as the evidence base for H1.
- `canonicalJSON` / `writeCanonicalJSON`'s `big.Rat` path (unchanged; the
  jsonb *plumbing* around it was re-verified above).
- Concurrency: no new goroutine was introduced in this range;
  `readAnswerWithDeadline`'s one-shot reader is re-verified above.

---

## Not reviewed

- `*_test.go` files — read for context (particularly `postload_verify_test.go`,
  `resume_integration_test.go`, `transform_test.go`, `logic_test.go`,
  `typepicker_test.go`, `collation_test.go`) but not review targets per the
  brief.
- `testdata/` fixtures and the checked-in fuzz corpus.
- `docs/` other than the two plan files the brief named.
- Whether `HOMEBREW_TAP_GITHUB_TOKEN`'s actual GitHub scopes are minimal — not
  determinable from the repository. Worth confirming out of band that it is a
  fine-grained PAT scoped to `barrettclark/homebrew-tap` contents only, since
  `.goreleaser.yaml:53` hands it to a third-party build tool.
- Runtime behavior: nothing was executed against a real Postgres or a real
  SQLite corpus. Every finding above is derived from the source; the
  load-test campaign (Phase B) and fuzz work (Phase C) are the right places to
  confirm H1's Consequence B and M1's trigger empirically.
- GitHub issue #3 (deferred PostGIS/geometry target types) — out of scope by
  standing instruction.
