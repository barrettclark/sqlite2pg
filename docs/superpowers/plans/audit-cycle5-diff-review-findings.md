# Audit Cycle 5 — Phase A: fresh-eyes diff review findings

Reviewer: outside reviewer with no prior context on this codebase.
Scope: every non-test file in `internal/` and `cmd/`, plus `.goreleaser.yaml`,
`.github/workflows/`, `scripts/`, `Makefile`, `.golangci.yml`. Concentrated on
`git diff d0cb219..HEAD` (PRs #150–#154) per the brief; cycles 3 and 4 clean
bills carried forward and re-derived only where that diff shows a change.

Read-only pass. No file outside this report was modified, no issue was filed.

Baseline: `d0cb219` (`d0cb21951d288cbfab55fc6fc43d2d04429fb77d`).
HEAD: `bb55d2f` (`bb55d2fc946f0092e0449085692bedf5c6603f3a`).

**Totals: 2 Medium, 7 Low. No High.**

---

## Findings

### M1 — The type picker now offers integer/bigint/smallint for a TEXT column whose values are written in scientific notation, and the real COPY then fails

`internal/tui/logic.go:245-251` (the `case "integer", "bigint", "smallint":`
arm of `previewValueForType`), introduced by PR #151 / issue #139 as the fix
for cycle 4's M1.

```go
numText := value
if strings.ContainsAny(numText, "eE") {
    if f, perr := strconv.ParseFloat(numText, 64); perr == nil {
        numText = strconv.FormatFloat(f, 'f', -1, 64)
    }
}
result, err := copywriter.Transform("numeric_text_to_integer", numText)
```

The fix's own comment states the load-bearing premise: *"That form is only ever
produced from a float64."* That is not true. `value` is
`review.formatSampleValue`'s output, `fmt.Sprintf("%v", v)`
(`internal/review/samples.go`), and for a `string` SQLite value `%v` returns the
string **verbatim**. A TEXT-storage column whose rows are literally spelled in
scientific notation — a routine shape in instrument/lab/CSV-derived exports
(`"1.23e+05"`, `"1.5e3"`) — reaches this branch as itself, not as a float64
rendering.

**Trigger:** a TEXT column holding e.g. `"1.5e3"` in its samples. Open the type
picker on it and select `bigint`.

**Observable wrong behavior:** `strconv.ParseFloat("1.5e3")` succeeds, `numText`
becomes `"1500"`, `numeric_text_to_integer` accepts it, so
`previewValueForType` returns `valid=true, transform="numeric_text_to_integer"`
and previews `1500`. `validTypesForColumn` therefore offers `integer`/`bigint`/
`smallint`, and `onTypeSelected` (`internal/tui/typepicker.go:111-132`) commits
`TargetType: bigint, Transform: numeric_text_to_integer` to the config via
`ApplyDecision`. At real COPY time the raw value handed to the transform is the
untouched string `"1.5e3"`, `parseWholeNumberText` finds the `.`, scans
`"5e3"` for a non-`'0'` byte, hits `'5'`, and errors *"has a non-zero
fractional part"*. `sqlite2pg load` aborts mid-COPY. Nothing catches it first:
a TUI human override is final — `verifyTransformsAgainstFullTable` runs during
profiling, before review, and is never re-run on a `DecisionRequest`.

Before PR #151 the picker correctly refused the type, because
`numeric_text_to_integer` rejected the exponent form outright. This is the
literal hazard the surrounding comment block warns about, four lines above the
new code: *"Any type this validates for must always carry the transform that
actually makes it work, or a human selecting it here breaks the real COPY."*

The correct discrimination is on the sample's underlying storage class, not on
whether the display string contains `e`/`E` — the picker only ever sees the
display string, so the fix would need the raw value (or at least the column's
observed storage classes) to distinguish "a float64 rendered by `%v`" from "a
string that happens to look like one".

Verified empirically: a scratch reimplementation of the exact ten lines
reproduces `"1.5e3" -> "1500"` and `"1.712345678e+09" -> "1712345678"`;
`parseWholeNumberText` was derived from source
(`internal/copywriter/transform.go:640-660`).

Severity: **Medium** (loud COPY abort on a config the tool's own UI certified,
reachable through the single most common review action; not silent corruption).

Root cause: PR #151 (`f212de1`), issue #139 / cycle-4 M1.

---

### M2 — `release.yml` re-introduces the release-time `go mod tidy` that issue #117 deliberately removed, and both `.goreleaser.yaml` and `ci.yml` still document it as gone

`.github/workflows/release.yml:46-49`, contradicted by `.goreleaser.yaml:3-8`
and `.github/workflows/ci.yml:34-40`.

```yaml
      - name: go.mod is tidy
        run: |
          go mod tidy
          git diff --exit-code go.mod go.sum
```

`.goreleaser.yaml`'s header comment is unambiguous about why this was removed:

> No `before.hooks` here: a release must build the tagged tree verbatim.
> `go mod tidy` at release time hits the network and can resolve a different
> dependency set than CI tested against, **or abort the release outright**
> (issue #117 / L8). The tidiness check lives in CI instead.

and `ci.yml` restates it: *"The release build no longer runs `go mod tidy`
(issue #117): a release must build the tagged tree verbatim. Enforce tidiness
here instead, so a drift is caught in review, not at release."*

PR #152 (issue #147 / cycle-4 L5) put the step back into the release job — a
different mechanism (a workflow step rather than a goreleaser `before` hook),
but the same job, the same working tree, the same network dependency, and the
same position ahead of goreleaser.

**Trigger:** push a `v*` tag on a tree whose `go.mod`/`go.sum` were tidy at PR
time but whose tidy output differs on the release runner — a proxy/sumdb
resolution difference, a toolchain patch bump under `go-version: "1.26.8"`, or
simply a transient network failure inside `go mod tidy` itself.

**Observable wrong behavior:** the release job fails at the tidy step and
publishes nothing — no binaries, no Homebrew cask — for a reason that has
nothing to do with the tagged tree. That is verbatim the "abort the release
outright" failure mode #117 was filed to eliminate. Bounded (it fails closed,
never publishes a mis-resolved build), and the tag can be re-pushed, but the
guarantee #117 bought is gone and two doc comments now describe behavior the
repository no longer has.

Note the cycle-4 L5 concern (a tag racing CI) is genuinely fixed by the other
four steps #152 added; only the tidy step conflicts. Deriving the fix cheaply:
the tidy check belongs in CI only, or in the release job as a non-mutating
`go mod tidy -diff` (Go 1.23+), which reports drift without touching the tree.

Derived from source; not run against a real tag.

Severity: **Medium** (contract violation against a documented, deliberate
decision; currently latent).

Root cause: PR #152 (`df13fdb`), issue #147 / cycle-4 L5.

---

### L1 — `sqlite2pg verify` writing to stdout still has no write-error latching, but `load --verify` writing to stdout now does

`cmd/sqlite2pg/verify.go:81` versus `cmd/sqlite2pg/postload_verify.go:195-196`.

```go
// verify.go
reportOut := io.Writer(os.Stdout)   // wrapped in errWriter only when --out is set
...
// postload_verify.go
ew := &errWriter{w: out}            // always wrapped, and out is os.Stdout
out = ew
```

PR #152 (issue #146 / cycle-4 L4) closed the asymmetry in one direction and
opened it in the other. As of HEAD:

| command | destination | truncated report detected? |
|---|---|---|
| `verify --out f` | named file | yes (#136) |
| `load --verify > f` | stdout | yes (#146, new) |
| `verify > f` | stdout | **no** |

**Trigger:** `sqlite2pg verify --pg ... src.db cfg.yaml > report.txt` on a full
disk, clean verification.

**Observable wrong behavior:** truncated report, exit code 0 — the exact
failure the sibling path now catches on the identical stream. `verifyLoadedTables`'
own doc comment (`verify.go:151-161`) exists specifically to keep the two report
paths from drifting; this is a third drift in the same pair.

Severity: **Low** (defense in depth; no data risk).

---

### L2 — The two report paths disagree on what a FAILED verdict *plus* a report write error reports

`cmd/sqlite2pg/verify.go:130-141` (`verifyOutcome`) versus
`cmd/sqlite2pg/postload_verify.go:204-217`.

`verifyOutcome` composes both facts:

```go
return nil, fmt.Errorf("%s; also failed writing the report: %w", msg, reportErr)
```

`runPostLoadVerify` returns the FAILED error from `:208` and never reaches the
`ew.err` check at `:215`, so the write error is discarded entirely.

**Trigger:** a genuine value mismatch *and* a stdout/report write failure in the
same invocation, on either path.

**Observable behavior:** both exit non-zero and both name the FAILED verdict
first, so the exit code and the headline are consistent — but the standalone
path tells the operator the report is also incomplete and the post-load path
does not, leaving them reading a truncated report with no indication it is
truncated. Answering the brief's question directly: the divergence is
message-only, not exit-code-relevant, so it is not the dangerous form of
"two similar paths silently disagree" — but it is unnecessary, and closing it is
a three-line change (check `ew.err` before the `!summary.passed()` block and
append it the way `verifyOutcome` does).

Confirmed the other half of the brief's question: `ew` wraps the writer passed
as **both** `progressOut` and `reportOut` at `postload_verify.go:199`, so
`ew.err` does cover a failure inside `verifyLoadedTables`' own `fmt.Fprintf`
progress lines and `writeVerifyReport`'s ~30 unchecked `fmt.Fprint*` calls, not
just the final summary line. That part is right.

Severity: **Low**.

---

### L3 — `maskParensAndStringLiterals` masks only single-quoted string literals, so `DEFAULT "COLLATE NOCASE"` (and the backtick form) still produce a false NOCASE

`internal/sqlitereader/collation.go:129-160`, specifically the `collationName`
test at `:136`:

```go
collationName := depth == 0 && (c != '\'' || precededByCollateKeyword(s, i))
```

At top level, only a `'...'` span is eligible for masking; `"..."`, `` `...` ``
and `[...]` spans are always kept verbatim so a quoted collation *name* survives
for `collateClauseRe`. SQLite's double-quoted-string misfeature means `"..."`
and `` `...` `` are also accepted as **string literals** when they don't resolve
to an identifier — which is exactly what a `DEFAULT` value is.

**Trigger, verified empirically against `sqlite3`:**

```
sqlite3 t.db "CREATE TABLE t1 (a TEXT DEFAULT \"COLLATE NOCASE\", b INT);" \
             "INSERT INTO t1(b) VALUES (1);" "SELECT a FROM t1;"
COLLATE NOCASE
```

`sqlite_master.sql` stores the text verbatim, and
`parseColumnCollations` on it returns `map[a:NOCASE]` (verified with a scratch
copy of `collation.go`) — column `a` is genuinely BINARY.

**Observable wrong behavior:** `ColumnCollations` reports NOCASE for a BINARY
column; if it is a primary-key column, `primaryKeyOrderingIsSafe` returns false
and the table drops from the precise streaming PK-ordered verification path to
`verifyTableUnordered`, which materializes an entire column from both sides in
memory. Degraded precision and memory cost on a large table, never a wrong
verdict — the parser can still only over-report non-BINARY.

This is the residual of cycle-4's L3 that PR #151 closed for `'...'` only. Same
severity, same fail-safe direction.

Severity: **Low**.

---

### L4 — `pgTemporalMinYear = -4713` is one year more permissive than PostgreSQL actually is

`internal/pipeline/verify_transform.go:230` (and, identically and
pre-existing, `internal/copywriter/transform.go:717`'s
`minPlausibleTimestampYear`).

Go's `time.Time.Year()` uses astronomical year numbering (year 0 = 1 BC), so Go
year `-4713` is **4714 BC**, not 4713 BC. Measured against Postgres 18:

```
select '4713-01-01 BC'::date        -> 4713-01-01 BC        (ok)
select '4714-11-24 BC'::date        -> 4714-11-24 BC        (ok)
select '4714-01-01 BC'::date        -> ERROR 22008 date out of range
select '4714-11-24 BC'::timestamptz -> ok
select '4714-01-01 BC'::timestamptz -> ERROR 22008 timestamp out of range
```

So Postgres's true floor is 4714-11-24 BC (Julian day 0), i.e. Go year `-4713`
is *partly* storable — 38 days of it — and Go years `-4714` and below are not.
The guard `if y < pgTemporalMinYear { return false }` therefore accepts any
instant in Go year `-4713`, of which all but the last 38 days will still abort
COPY with "date/time field value out of range".

The upper bounds are exactly right, also verified: `'5874897-12-31'::date` is
accepted and `'5874898-01-01'::date` errors; `'294276-12-31 23:59:59+00'::timestamptz`
is accepted and `'294277-01-01'` errors. So `pgDateMaxYear` and
`pgTimestampMaxYear` need no change — this is only the shared minimum.

**Trigger:** a `julian_day_to_date` / `excel_serial_to_timestamptz` /
`unix_epoch_*` conversion landing in Go year -4713 before 24 November. No
realistic source data reaches it; recording it because cycle 4's clean bill
asserted these bounds "match PostgreSQL's documented range" and they are one
year off in the permissive direction.

Severity: **Low** (defense in depth, no realistic trigger).

---

### L5 — `sortKeyFor`'s entire invariant doc comment is now attached to `normalizeNarrowNumeric`; `sortKeyFor` has no doc comment at all

`internal/pipeline/verify_load.go:654-707`.

PR #151 inserted `normalizeNarrowNumeric` (and its own six-line comment)
*between* `sortKeyFor`'s ~35-line doc block and `func sortKeyFor`, so Go
attaches the whole thing — including the "two values `valuesMatch` would
consider equal always produce the same key" invariant statement and the
history of the three times it has broken — to the wrong function.

Verified: `go doc -all -u ./internal/pipeline` lists `func sortKeyFor(v any) string`
with no documentation.

Given that this exact quartet is the project's named "same-two-functions"
hot spot, and that the misplaced text is precisely the invariant that keeps
breaking, having it document a three-line helper instead of the function it
constrains is worth fixing. Purely a comment move.

Severity: **Low** (documentation).

---

### L6 — `load --resume` on an already-complete load now re-validates every foreign key under ACCESS EXCLUSIVE locks; "idempotent" is not "free"

`cmd/sqlite2pg/main.go:663-694`, `cmd/sqlite2pg/state.go:18-26`.

The #142 fix is correct — the FK set really is re-derived from a mutable config
and the one-shot flag really did suppress newly-eligible constraints, so
removing the gate is right. But the replacement comment claims:

> Re-running is safe: every statement is idempotent (DROP CONSTRAINT IF EXISTS
> + ADD; CREATE INDEX IF NOT EXISTS), in one transaction, so a re-run after any
> failure or a crash mid-step never hits "already exists"

`ALTER TABLE ... DROP CONSTRAINT IF EXISTS x, ADD CONSTRAINT x FOREIGN KEY ...`
does not become a no-op the second time: it drops the constraint and re-adds it,
and Postgres **fully re-validates** the new constraint (a scan of the child
table plus an index probe per row on the parent). Measured on Postgres 18 with
200 000 child rows:

```
first  ALTER (constraint absent)  968 ms
second ALTER (constraint present) 2205 ms      <- full re-validation
CREATE INDEX IF NOT EXISTS  (1st) 1467 ms
CREATE INDEX IF NOT EXISTS  (2nd)   91 ms      <- genuinely a no-op
```

So `CREATE INDEX IF NOT EXISTS` is a true no-op; the FK `ALTER` is not, and is
in fact *slower* than the first application. All FKs across all tables run in
one transaction, so a `--resume` against a fully-loaded large database now
acquires ACCESS EXCLUSIVE on every constrained table (and SHARE ROW EXCLUSIVE on
every referenced one) and holds them all until the last FK finishes — where
previously the `FKsApplied` gate skipped the step entirely and took no locks.
`state.go`'s new comment calls the flag "purely informational", which is
accurate but concedes the saved work is gone.

Answering the rest of the brief's item 4 in the affirmative:
- Nothing else in `executeLoad` read the removed `st`; `statePath` is still used
  by `markForeignKeysApplied` and by `markTableCompleted` above.
- `markForeignKeysApplied` still runs exactly once per successful invocation.
- The DDL really is re-runnable: the two `ALTER`s above ran back to back with
  only a `NOTICE ... does not exist, skipping` on the first and no error on the
  second, and `pg_constraint.convalidated` is `t` afterwards.
- `--dry-run` never reaches this code (`printDryRunDDL` returns before
  `executeLoad`'s connection work) and never gated on `FKsApplied`, so it is
  unchanged.
- The one new failure mode is a *deadlock between two concurrent `--resume`
  runs* against the same database: each takes ACCESS EXCLUSIVE on a child and
  then needs SHARE ROW EXCLUSIVE on a parent, and the FK statements are emitted
  in sorted-child order, which is not the same as sorted-parent order. Two
  simultaneous resumes could interleave into a lock cycle. Previously the second
  run would have skipped the step. This fails loudly with a Postgres deadlock
  error, needs two concurrent operators, and is not a data risk — noted rather
  than filed separately.

Phase D's plan says to confirm "no change" on a plain non-resume `load`, which
is right (that path always ran the step) — but it does not measure `--resume`,
which is the path that changed. Worth adding.

Severity: **Low** (performance and lock-footprint regression on a valid path;
no wrong result).

---

### L7 — The type picker's integer preview can differ from the value the load actually stores, for a float64 above 2^53

`internal/tui/logic.go:245-269`.

For a REAL-storage sample of `1.2345678901234568e18`, the new reformat produces
`"1234567890123456800"`, which `numeric_text_to_integer`'s *string* branch parses
exactly, so the picker previews `1234567890123456800`. At load time the raw
value is the `float64` itself and the transform's *float64* branch computes
`int64(f)` = `1234567890123456768`. The preview is 32 higher than what is
stored.

Verified with a scratch Go program:
`%v` → `1.2345678901234568e+18`; `FormatFloat(f,'f',-1,64)` →
`1234567890123456800`; `int64(f)` → `1234567890123456768`.

No corruption — the stored value is the correct `float64`→`int64` conversion,
and both sides of `sqlite2pg verify` recompute it identically — but the number a
human is shown while deciding is not the number they will get. Same class as the
"3.7 previews as 3" defect issue #80's audit removed from this arm.

Severity: **Low** (display accuracy).

---

## Clean bill

The eight Phase A surfaces, in the plan's order.

### 1. `verify_transform.go` — `fitsTargetType` / `fitsTemporalRange` (#140 / M2)

- **Bounds.** `pgDateMaxYear` 5874897 and `pgTimestampMaxYear` 294276 are exactly
  right, confirmed against Postgres 18 (`'5874897-12-31'::date` ok /
  `'5874898-01-01'` errors; `'294276-12-31 23:59:59+00'::timestamptz` ok /
  `'294277-01-01'` errors). Only the shared minimum is off, see L4.
- **Target spellings.** The `switch` covers `date` and `timestamptz`, which is
  exactly the set the pipeline produces: I enumerated every `SuggestedType:`
  literal under `internal/profiler/heuristics/` — `boolean`, `double precision`,
  `timestamptz`, `jsonb`, `date`, `uuid`, `uuid[]`, plus
  `esri_typename`'s `integer`/`double precision`/`__drop__` — and `date` and
  `timestamptz` are the only temporal ones. Bare `timestamp` /
  `timestamp with(out) time zone` are unreachable from any heuristic or from
  `review.TypeOptions`; they are there only for a hand-edited config, which is
  the right amount of defensiveness. The `default: return true` arm is correct —
  a `text`-targeted `time.Time` genuinely has no range constraint.
- **Consistency with #124.** Out of range routes to review by returning `false`
  from `fitsTargetType`, which sets `bad = true` in
  `verifyTransformsAgainstFullTable`'s loop — byte-identical to what the
  `unix_epoch_*` arms achieve by erroring out of `copywriter.Transform`. Both
  land in the same `results[col] = {OK:false}` branch. Consistent.
- **Nothing newly *accepted*.** The new branch only ever *narrows*: before,
  `asInt64` failed on a `time.Time` and `fitsTargetType` returned `true`
  unconditionally. Every `time.Time` that used to pass and still passes is a
  strict subset. The stray-`1.7e9`-in-a-`realdate`-column case from cycle-4 M2
  is worth recording precisely: `julian_day_to_date(1e9)` yields year ≈2 733 100,
  which is **below** `pgDateMaxYear`, so it is (correctly) still accepted —
  Postgres genuinely can store that date. The values the check now catches are
  the ones near `maxPlausibleJulianDay` (1e12 → year ≈2.7e9) and
  `excel_serial_to_timestamptz`'s `clampDaysToInt` ceiling (±MaxInt32 days →
  year ≈5.88e6, past the 294276 timestamptz limit). Both were the real gap.
- `tm.Year()` reads the year in `tm`'s own `Location`; every producing transform
  builds in UTC (`time.Unix(...).UTC()`, `excelEpoch` in UTC, `julianDayToDate`'s
  `time.Date(..., time.UTC)`), so there is no zone-dependent off-by-one except
  within one day of a year boundary at the extremes, which is inside L4's noise.

### 2. `sqlitereader/collation.go` — `maskParensAndStringLiterals` + `precededByCollateKeyword` (#145)

Walked the whole masker against the brief's list with a scratch copy of the
file (unexported functions called directly). Every case in the brief behaves:

| input (column body) | result |
|---|---|
| `a TEXT DEFAULT 'COLLATE NOCASE', b TEXT COLLATE RTRIM` | `{b:RTRIM}` ✓ |
| `a TEXT CHECK (a = upper(a) COLLATE NOCASE)` | `{}` ✓ (depth 1) |
| `a TEXT CHECK ((a) = (upper(a) COLLATE NOCASE))` | `{}` ✓ (depth ≥2) |
| `a TEXT DEFAULT '(' COLLATE NOCASE` | `{a:NOCASE}` ✓ (unbalanced `(` in a literal) |
| `a TEXT DEFAULT ')))' COLLATE NOCASE, b TEXT COLLATE RTRIM` | `{a:NOCASE,b:RTRIM}` ✓ |
| `a TEXT COLLATE "NOCASE"` / `'NOCASE'` / `[NOCASE]` / `` `NOCASE` `` | all `{a:NOCASE}` ✓ |
| `a TEXT DEFAULT 'it''s COLLATE NOCASE' COLLATE RTRIM` | `{a:RTRIM}` ✓ (doubled `''`) |
| `"a" COLLATE 'NOCASE'` (COLLATE at offset 0 of `rest`) | `{a:NOCASE}` ✓ |
| `a TEXT /* COLLATE NOCASE */ COLLATE RTRIM` | `{a:RTRIM}` ✓ |
| `a TEXT COLLATE /* x */ 'NOCASE'` | `{a:NOCASE}` ✓ |
| `a TEXT COLLATE\nNOCASE` | `{a:NOCASE}` ✓ |
| `a TEXT GENERATED ALWAYS AS (upper(b) COLLATE NOCASE) STORED` | `{}` ✓ |
| `a TEXT, UNIQUE (a COLLATE NOCASE)` | `{}` ✓ |
| `a VARCHAR(20) COLLATE NOCASE` | `{a:NOCASE}` ✓ (type-length parens masked, clause survives) |

Specifics worth not re-deriving next cycle:

- **The comment-between-COLLATE-and-quote case is safe by construction.**
  `parseColumnCollations` runs `stripSQLComments` on `body` *before*
  `splitTopLevelCommas`, so by the time `precededByCollateKeyword` sees `rest`
  there are no comments left — the comment has already become a single space,
  which the whitespace skip-back handles. That is why row 10 above works.
- **`precededByCollateKeyword` at offset 0.** `k < len(kw)` guards the slice, and
  the `k-len(kw) == 0` short-circuit covers the no-preceding-byte case. Row 8
  exercises it.
- **No byte-offset coupling.** `maskParensAndStringLiterals` builds `b := []byte(s)`
  and only ever *assigns* spaces — it never inserts or deletes — so
  `len(mask) == len(rest)` always. The only consumer is
  `collateClauseRe.FindStringSubmatch`, whose captured group is taken from the
  masked string, and the masked string preserves top-level quoted collation
  names verbatim (that is what the `collationName` test is for). Nothing else in
  `parseColumnCollations` indexes into `rest` after this point.
  `columnListOpenParen`'s `len(createSQL) - len(rest) + k` arithmetic runs on a
  different string entirely and is untouched by this change.
- **Depth accounting cannot be corrupted by a literal**, because quote spans are
  consumed whole by `skipQuoteOrComment` before the `'('` / `')'` cases are ever
  reached; an unbalanced `)` is floored at `depth == 0`.
- One latent hole, recorded so it isn't re-derived: an **unbalanced `[` at top
  level** makes `skipQuoteOrComment` consume to end-of-string, leaving the tail
  unmasked. `sqlite_master.sql` only ever holds text SQLite has already parsed,
  so this is unreachable from a real database — same conclusion cycle 4 reached
  for `matchingParen`.
- L3 above is the one residual false positive, and it fails safe in the same
  direction as before.

### 3. `verify.go`'s `verifyOutcome` (#144) and `postload_verify.go`'s `errWriter` wrap (#146)

- **Exit codes cannot disagree.** For "verification FAILED", both paths return a
  non-nil error, and `main` maps any non-nil error to rc 1. For "FAILED + write
  error" both still return non-nil. There is no input for which one exits 0 and
  the other 1. Only the message differs (L2).
- **`verifyOutcome` itself is correct.** The verdict is tested first, so a
  report-file error can never shadow "your data is wrong" — the #144 goal is
  met. `report written to` is now emitted only on the full-success path, after
  the verdict is known, and only when `outPath != ""`; the previous
  premature-print ordering is gone. The `lines` slice is returned rather than
  printed inside, which is what makes the new
  `cmd/sqlite2pg/verify_outcome_test.go` able to assert it — a genuinely better
  shape.
- **`ew.err` latching.** `errWriter.Write` returns early when `e.err != nil`, so a
  later successful write can never clear a latched error, and the short-write
  latch (`n < len(p)` with nil `err` → `io.ErrShortWrite`) is intact. In
  `runVerify`, `ew` is only dereferenced under `reportFile != nil`, where it is
  guaranteed non-nil. In `runPostLoadVerify`, `ew` wraps the writer used for
  *both* `progressOut` and `reportOut`, so it does cover
  `verifyLoadedTables`' own `fmt.Fprintf` and every `writeVerifyReport`
  `fmt.Fprint*` — the brief's specific question, answered yes.
- `closeErr` is now only reported when `ew.err` is nil (the `switch` ordering),
  which is right: a latched write error is the more informative one and a close
  after a failed write will usually also fail.

### 4. `main.go` — unconditional FK step (#142 / M4)

Covered in L6 (which is a performance/lock finding, not a correctness one).
The correctness questions all check out: no other reader of the removed `st`,
`markForeignKeysApplied` still exactly-once per success, DDL empirically
re-runnable against a real Postgres 18 with no error and `convalidated = t`,
`--dry-run` unaffected. `state.go`'s rewritten `FKsApplied` comment now
accurately describes what the field is (a record, not a gate), and the
`executeLoad` tail comment was updated in the same commit to match — the two
did not drift.

### 5. `verify_load.go` — `normalizeNarrowNumeric` (#148 / L6)

- **Every entry point is covered.** `sortKeyFor` and `valuesMatch` are the only
  two functions that receive a compared value; I grepped all call sites of
  `exactNumericEqual`, `crossTypeNumericEqual` and `numericSortKey` and every one
  is *inside* those two (`valuesMatch:1004,1008`, `sortKeyFor:730`). There is no
  third caller: `compareColumnUnordered` goes through `sortKeyFor` at `:492`, and
  `verifyTableOrdered` through `valuesMatch` at `:358`. Both are normalized at
  their entry.
- **The reassignment is real, not shadowed.** `sortKeyFor` does `v = normalize...(v)`
  *before* the type switch, and the `case int64, float64:` arm passes the
  reassigned `v` (not the switch-bound `t`) to `numericSortKey`, so a normalized
  `int`/`float32` really does take the numeric branch.
- **`float32`-vs-`float32` verdict is unchanged.** Both sides widen to the same
  `float64`, `exactNumericEqual`'s `float64` arm returns `e == a`, which for the
  same `float32` bit pattern is always true — matching what the old `%v`
  fallback returned. No regression.
- **`float32`-vs-`float64` verdict *does* change** (previously `%v`-matched
  `"0.1"` vs `"0.1"`; now `float64(float32(0.1))` ≠ `0.1` → mismatch). This is
  the correct direction — they are different numbers — and the unordered path
  already reported that pair as a mismatch before the change, so the two paths
  now *agree* where they previously disagreed. That is exactly what #148 set out
  to do. It is also unreachable today: `copywriter.Transform` normalizes `int`→
  `int64` in every arm, `modernc.org/sqlite` yields `int64`/`float64`, and no
  `pgColumnScanner` destination produces `float32`.
- The helper deliberately does not cover `int32`/`int8`/`uint*`; those are
  equally unreachable and `%v`-fallback behavior for them is unchanged.
- L5 above is the one defect here, and it is a misplaced comment.

### 6. `profile.go` — `fallbackTargetNeedsStorageCheck` = `!isTextTargetType` (#149 / L7)

Sound, and currently a strict no-op at both call sites:

- **Call site A, `fallbackSampleMismatch`** (reached from
  `decide_column.go:88`, the zero-findings path). Its input is always
  `fallbackTypeFor`'s output, whose complete vocabulary I enumerated from source:
  `double precision`, `bigint`, `integer`, `timestamptz`, `bytea`, `text` (plus
  `fallbackTypeFromDeclared`'s `integer`/`bytea`/`double precision`/`text`). The
  old enumeration returned `true` for all of those except `text`; the new
  negation returns exactly the same answers. No behavior change.
- **Call site B, the decide_column gate at `:148`.** The predicate is only
  consulted when `best.TransformExpr == ""`. I checked every registered
  heuristic: all 20 `Finding` constructions under
  `internal/profiler/heuristics/` set a non-empty `TransformExpr`
  (`esri_typename`'s is `"esri_typename"` or `"drop_column"`, never empty), and
  the only empty-transform `Finding` in the tree is
  `pipeline.varcharFinding`, whose `SuggestedType` is `varchar(N)` or `text` —
  both `isTextTargetType`, both still `false`, and in any case its
  `Confidence: 0.5` forces review before the gate is reached. So the widened
  predicate cannot currently fire for `boolean`/`date`/`jsonb`/`uuid`/`uuid[]`.
- **No expensive-scan regression is being forced**, which was the brief's
  concern — and the converse is worth recording for whoever adds an
  empty-transform heuristic later: if one ever does emit `boolean`/`date`/
  `jsonb`/`uuid`/`uuid[]`, the widened gate will schedule a full-table scan whose
  two checks (`fallbackValueFitsTarget` and `fitsTargetType` →
  `copywriter.FitsRange`) both `default: return true` for those types. The scan
  would be a guaranteed-clean, cost-only pass. Not a bug today; a comment on
  `fallbackValueFitsTarget` recording that its `switch` must grow alongside this
  predicate would prevent it.
- `isTextTargetType` is the right shared predicate: `text`, bare `varchar`, and
  the `varchar(` prefix — the same set `verifyTableOrdered` uses to decide
  `COLLATE "C"`, so the two now share one definition of "string-holding".

### 7. Release / CI surface (#147 / L5)

- **Versions match `ci.yml` exactly**: `actions/setup-go@v5` with
  `go-version: "1.26.8"`, `golangci/golangci-lint-action@v8` with
  `version: v2.13.2`, `govulncheck@v1.7.0`, and byte-identical `gofmt -l -e .`
  and `go mod tidy` + `git diff --exit-code` scripts. The `Makefile` pins the
  same two tool versions. Step *order* is also identical (gofmt, Lint, Build,
  Vulnerabilities, tidy, Test).
- **Failures do block goreleaser**: all steps are in the single `goreleaser` job,
  sequential, with no `continue-on-error` and no `if: always()` anywhere in
  either workflow. Any red step aborts before the
  `goreleaser/goreleaser-action@v6` step.
- **Dropping the standalone `go vet` lost nothing**: `.golangci.yml` enables
  `govet` explicitly under `default: none`, and `run.build-tags: [integration]`
  means the linters also cover the tier-3 files `go vet ./...` would have missed
  without the tag. Net coverage went up.
- M2 above is the one problem with this surface.
- `.goreleaser.yaml` is otherwise unchanged from cycle 4's verified state
  (pinned `version: "2.18.0"`, `homebrew_casks:` with `binaries:`, no
  `before.hooks`), and `builds.goarch` is `amd64`/`arm64` only — no 32-bit
  target, so `julianDayToDate`'s `int(year)` narrowing is safe on every shipped
  binary.

### 8. README verify section (#141 / M3 + verify-accuracy pass)

- **The ordered/multiset claim is now stated correctly.** README lines 106-120
  say the ordered path requires a primary key "whose every column is
  transform-free and BINARY-collated in the SQLite source". That is exactly
  `primaryKeyOrderingIsSafe` (`verify_load.go:254-269`): a first loop rejecting
  any PK column with a non-empty `Transform`, a second rejecting any whose
  `ColumnCollations` entry is not `BINARY` (case-insensitively). The stated
  fallback — "compares each column as a sorted value multiset ... a reported
  example is a position in the sorted comparison, not a source row" — matches
  `compareColumnUnordered` and `writeVerifyReport`'s
  `sorted-comparison position %d` wording verbatim. The parenthetical
  "byte-order-collated on both sides, even for `varchar(n)` text keys" matches
  `verifyTableOrdered`'s `isTextTargetType`-gated `COLLATE "C"`.
- **Every invocation example parses under Go's `flag`.** The five
  `sqlite2pg ...` lines in the README (lines 38, 64, 65, 66, 67) and the prose
  form at line 96 all put flags **before** positionals, which is required
  because every subcommand uses `flag.NewFlagSet` + `fs.Parse(args)` and
  `flag.Parse` stops at the first non-`-` argument. Cycle 4's M3 is fully fixed;
  I re-grepped the whole file and found no remaining flag-after-positional.
- The `--out`, `--verify`/`--noverify`, prompt-default and
  post-load-mismatch-exit-code descriptions all match `postload_verify.go` and
  `verify.go` as of HEAD.

### Also re-checked, unchanged in this diff

`git diff d0cb219..HEAD` touches nothing else in `internal/` or `cmd/`, so
cycles 3 and 4 clean bills stand for `internal/ddl/`, `internal/config/`,
`internal/copywriter/pipeline.go` and `load.go`, `internal/resolver/`,
`internal/review/`, `internal/profiler/` (heuristics read only as evidence for
surface 1 and 6), `cmd/sqlite2pg/provision.go` / `progress.go` /
`resolve_helpers.go`, `scripts/verify-all-fixtures.sh`, `Makefile`,
`.golangci.yml` and `.github/workflows/{ci,vulncheck}.yml`. `go build ./...`
and `go test ./...` are green at HEAD.

---

## Not reviewed

- `*_test.go` files — read for context only
  (`verify_outcome_test.go`, `resume_fk_newtable_integration_test.go`,
  `profile_cycle4_test.go`, `verify_load_cycle4_test.go`,
  `verify_transform_cycle4_test.go`, `collation_cycle4_test.go`,
  `logic_cycle4_test.go`), not review targets per the brief.
- `testdata/` fixtures and the checked-in fuzz corpora.
- `docs/` other than the three plan files the brief named.
- `go.mod` / `go.sum` dependency-version review — `make vulncheck` owns it.
- `HOMEBREW_TAP_GITHUB_TOKEN`'s GitHub scopes — still not determinable from the
  repository, same as cycles 3 and 4.
- GitHub issue #3 (deferred PostGIS/geometry targets) — out of scope by standing
  instruction.
- Full end-to-end `profile`/`load`/`verify` runs against a real fixture — that is
  Phase B's job. What I *did* execute: `go build ./...`, `go test ./...`, a
  scratch copy of `collation.go` driven against 22 CREATE TABLE bodies, a scratch
  reproduction of `previewValueForType`'s new reformat, `sqlite3` for the
  `DEFAULT "COLLATE NOCASE"` storage check (L3), and `psql` against Postgres 18
  for the temporal-range bounds (L4, surface 1) and the FK/index re-run timings
  (L6, surface 4).

### Suggested Phase B / D follow-ups

- **Phase B**: a purpose-built fixture with a TEXT column of scientific-notation
  strings would demonstrate M1 end to end (profile → review → load), though M1 is
  reachable only through the interactive picker, so a unit test on
  `previewValueForType("1.5e3", "bigint")` is the cheaper reproduction.
- **Phase D**: add a `load --resume` timing on an already-complete
  `beets_library.db` load. The plan currently only measures the non-resume path,
  which is the one #142 did *not* change; L6's regression is invisible there.
