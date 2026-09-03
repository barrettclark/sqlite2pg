# Audit Cycle 4 — Phase A: fresh-eyes diff review findings

Reviewer: outside reviewer with no prior context on this codebase.
Scope: every non-test file in `internal/` and `cmd/`, plus `.goreleaser.yaml`,
`.github/workflows/{ci,release,vulncheck}.yml`, `scripts/`, `Makefile`,
`.golangci.yml`. Concentrated on `git diff db35d39..HEAD` (PRs #124–#138) per
the brief; cycle 3's clean bill was re-verified only where that diff shows a
change.

Read-only pass. No file was modified, no issue was filed.

Baseline: `db35d39`. HEAD: `84e6e2d`.

**Totals: 4 Medium, 7 Low. No High.**

---

## Findings

### M1 — The type picker no longer offers integer/bigint/smallint for a REAL column whose sample renders in scientific notation

`internal/tui/logic.go:224-256` (the `case "integer", "bigint", "smallint":`
arm of `previewValueForType`).

```go
result, err := copywriter.Transform("numeric_text_to_integer", value)
if err != nil {
    return value, "", false
}
```

`value` is not a raw SQLite value — it is `review.formatSampleValue`'s
**display string**, produced by `fmt.Sprintf("%v", v)`
(`internal/review/samples.go:78`). For a `float64`, Go's `%v` switches to
scientific notation once the decimal exponent reaches 6:
`fmt.Sprintf("%v", float64(1000000))` is `"1e+06"`, and
`fmt.Sprintf("%v", float64(1712345678))` is `"1.712345678e+09"`. This is not a
hypothetical — it is the exact fact the sibling commit documents at length
50 lines above, in `dateTransformPreview`'s own comment block
(`internal/tui/logic.go:84-96`, issue #92 / L6).

`numeric_text_to_integer`'s string branch routes through
`parseWholeNumberText` (`internal/copywriter/transform.go:640`), which finds
the `.`, scans `s[i+1:]` for a non-`'0'` byte, and errors
`"has a non-zero fractional part"` on `"712345678e+09"`. So the arm returns
`valid=false`.

**Trigger:** a REAL-affinity column of large whole numbers — the codebase's own
running example, `bikes.last_reported` stored as REAL — reviewed in the TUI.
Open the type picker on it.

**Consequence chain:** `validTypesForColumn` filters the offered list by
`previewValueForType`'s `valid` flag (`internal/tui/typepicker.go:20`), so
`integer`, `bigint` and `smallint` are silently absent from the picker for that
column. A human who wants `bigint` cannot select it, even though the real COPY
would accept it fine (`numeric_text_to_integer`'s `float64` branch handles the
raw value correctly — it is only the *display string* round-trip that fails).
`timestamptz` **is** still offered for the same value, because
`dateTransformPreview` was switched from `ParseInt` to `ParseFloat` for exactly
this reason. `real` / `double precision` / `numeric` are also still offered
(that arm calls `strconv.ParseFloat` directly, which understands `e+09`).

This is the two-commits/one-contract shape: commit `2e48443` ("dateTransformPreview
parses epoch values via ParseFloat, not ParseInt") fixed the scientific-notation
problem for the date arms, and commit `5522180` ("type picker attaches real
transforms") — same PR family, same file — replaced the integer arm's
`strconv.ParseFloat` + `int64(f)` with a strict decimal-text parser, reintroducing
the same defect one arm over. The pre-`5522180` code handled `"1.712345678e+09"`
correctly.

Severity: **Medium**

---

### M2 — `julian_day_to_date` and `excel_serial_to_timestamptz` still produce timestamps Postgres cannot store, and full-table verification cannot see it

`internal/copywriter/transform.go:335-365` (`julian_day_to_date`) and
`internal/copywriter/transform.go:441-446` (`excel_serial_to_timestamptz`).

PR #124 (issue #111 / L2) established a contract for the numeric→temporal
transforms: bound the *result* to PostgreSQL's timestamptz range so that
`verifyTransformAgainstFullTable` can route the column to review rather than
letting COPY abort (or, worse, letting verify recompute the same wrong value on
both sides). All three `unix_epoch_*` arms got it:

```go
return rejectImplausibleTimestamp(time.Unix(int64(sec), nanos).UTC(), "unix_epoch_seconds", f)
```

`julian_day_to_date` did not. Its only guard is `maxPlausibleJulianDay = 1e12`,
which its own comment says exists solely to keep `julianDayToDate`'s int64
intermediates from overflowing and "leaves a ~1e6x margin below the overflow
point" — i.e. it is deliberately ~six orders of magnitude looser than any real
calendar date:

```go
if math.IsNaN(jd) || jd < -maxPlausibleJulianDay || jd > maxPlausibleJulianDay {
    return nil, fmt.Errorf("julian_day_to_date: %v is out of range", f)
}
return julianDayToDate(int64(jd)), nil
```

**Trigger:** a `realdate`-declared column (Esri File Geodatabase export) where
the 500-row sample is entirely inside `[1721425.5, 2816787.5]` — the JulianDay
heuristic requires `plausible == total`, so it must be — but one row elsewhere
in the table holds, say, `1e9`. This is precisely the issue #13 / #42 premise
the full-table check exists for.

**Consequence chain:** JulianDay fires at confidence `0.90`, which meets the
default `--threshold 0.9`, so `decideColumnTentative` routes the column to
`verifyTransformsAgainstFullTable`. That check runs `Transform("julian_day_to_date", 1e9)`
on every row; it returns `time.Date(year≈2733100, ...)` with **no error**, and
`fitsTargetType` has nothing to say about a `time.Time`
(`internal/pipeline/verify_transform.go:205-212` — `asInt64` fails, so it
returns `true`). The column verifies clean and auto-approves. `sqlite2pg load`
then aborts mid-COPY with a raw Postgres "date/time field value out of range"
error, on a column the tool has just certified. A `--resume` retries the same
table and hits the same failure. This is the exact failure mode issue #111 was
filed for.

`excel_serial_to_timestamptz` has the same shape but is an explicitly
*documented* choice (`transform.go:768-787`: "a loud, catchable failure instead
of silent corruption") — which is now inconsistent with the philosophy PR #124
adopted for `unix_epoch_*`. It is also easier to reach in one respect
(ExcelSerialDate tolerates 50% out-of-window samples) and harder in another
(confidence `0.85` is below the default threshold, so a human must approve the
column first). Both arms should either clamp-and-reject like the epoch arms, or
the epoch arms' new behavior should be documented as a deliberate divergence.

Severity: **Medium** (julian_day_to_date; excel_serial_to_timestamptz alone
would be Low)

---

### M3 — Every README invocation example that puts a flag after the positional argument is non-functional

`README.md:28`, `README.md:54-57`, and `README.md:87` (the prose form of
`verify`).

```
sqlite2pg run <source.db> --pg <postgres-url>
...
sqlite2pg load     <config.yaml> --pg <postgres-url>
sqlite2pg verify   <source.db> <config.yaml> --pg <postgres-url>
```

Every subcommand parses with Go's `flag` package (`flag.NewFlagSet(...)`,
`fs.Parse(args)`). `flag.Parse` **stops at the first argument that does not
begin with `-`** and hands the remainder back as positional args. So
`--pg <url>` after the positional is never parsed; it lands in `fs.Args()`,
`fs.NArg()` comes back as 3 instead of 1, and the command exits 1 on the
`usage:` guard.

Verified empirically against a binary built from HEAD:

```
$ sqlite2pg load /tmp/x.yaml --pg 'postgres://x'
error: usage: sqlite2pg load [--pg url] [--dry-run] [--force] [--resume] [--threshold F] [--verify|--noverify] <config.migration.yaml>
rc=1
$ sqlite2pg run /tmp/x.db --pg 'postgres://x'
error: usage: sqlite2pg run --pg url [--sample-size N] [--threshold F] [--keep-config] [--verify|--noverify] <source.db>
rc=1
```

**Consequence chain:** the very first command in the README's "How it works"
section — the one-command quickstart this tool leads with — fails outright when
copy-pasted. The error message at least prints the correct form, but the
correct form is nowhere shown in the README; the checked-in
`scripts/verify-all-fixtures.sh` is the only place in the repo that gets the
flag order right (`"$BIN" load --noverify --pg "$PG_URL" "$cfg"`).

Not a #130 regression — `git show db35d39:README.md` shows the same broken
ordering with the old binary name — but PR #130 rewrote all of these lines and
left the defect in place. Called out here because the brief explicitly names
"README invocation examples" as part of the #130 review.

Severity: **Medium** (documentation defect; no data risk, but it is the
tool's front door)

---

### M4 — `FKsApplied` silently suppresses foreign keys for a table newly included between a completed load and a `--resume`

`cmd/sqlite2pg/main.go:663-699`, `cmd/sqlite2pg/state.go:18-25`.

```go
st, err := readState(statePath)
...
if !st.FKsApplied {
    statements, skipped := ddl.GenerateForeignKeyConstraints(cfg)
    ...
} else {
    fmt.Println("foreign keys already applied in a prior run — skipping")
}
```

`FKsApplied` is a single boolean over "the FK step ran once", but the *set* of
FK statements is derived fresh from `cfg` on every invocation — and `cfg` can
change between the run that set the flag and a later `--resume`.

**Trigger:** run `sqlite2pg load --pg ... config.yaml` to completion
(`FKsApplied` becomes `true`; every table is in `Completed`). Then edit the
config to flip a table from `include: false` to `include: true` — or add one —
and run `sqlite2pg load --resume --pg ... config.yaml`. The schema-drift check
hashes the *source file*, not the config (`config.DetectDrift`), so nothing
objects.

**Consequence chain:** the newly-included table is created and COPY'd normally,
then the FK step is skipped with the reassuring line "foreign keys already
applied in a prior run — skipping". The new table's own foreign keys and FK
indexes are never created, **and** every foreign key on an already-loaded table
that previously failed `invalidForeignKeyReason` with "references table X,
which is excluded or missing" — and which is now valid — is never created
either. The load reports success. Nothing downstream detects it:
`sqlite2pg verify` compares row values, not constraints.

This is the two-features/one-artifact shape the brief names (precedents: #62,
#128). Note that since PR #132 made every FK statement idempotent
(`DROP CONSTRAINT IF EXISTS ... , ADD CONSTRAINT`; `CREATE INDEX IF NOT EXISTS`)
and PR #127 wrapped them in one transaction, the flag no longer buys correctness
at all — only a saved round-trip. `state.go:19` already concedes it is "an
optimization, not a correctness gate", but the code still uses it as a gate.

Related, and worth stating explicitly since the brief asks: a `--resume` that
finds `FKsApplied` true but a constraint manually dropped will also not restore
it. That one needs out-of-band DBA action to reach, so it is much weaker than
the config-edit path above.

I confirmed the adjacent hazard is **not** reachable: `connectForLoad` writes
`writeState(statePath, loadState{Database: dbName})` — a full overwrite — on
every non-`--resume` run (`cmd/sqlite2pg/provision.go:121`), so a second
`sqlite2pg run --keep-config` against the same source always starts from
`FKsApplied: false`. Also not reachable: a `--resume` DROP-and-recreate of an
empty table colliding with already-applied FKs, since a table in `Completed` is
filtered out before the existence probe, and `FKsApplied` can only be true once
every table is in `Completed`.

Severity: **Medium**

---

### L1 — `cmd/sqlite2pg/main.go`'s package doc still names the old binary

`cmd/sqlite2pg/main.go:1`

```go
// Command migrate replaces pgloader for SQLite -> Postgres migrations: it
```

The one `migrate`-as-command-name reference PR #130's ~70-file sweep missed. I
grepped every `errors.New`, `fmt.Errorf`, `fmt.Fprint*` and flag `Usage:` string
in `internal/` and `cmd/` for a stale or wrong command reference; this doc
comment is the only survivor. The other four `migrate` hits
(`internal/sqlitereader/esri.go:58`, `internal/config/schema.go:25,35`,
`internal/profiler/heuristic.go:2`, `internal/profiler/timestamp.go:32`) are the
English verb, correctly left alone. The `run` subcommand is still dispatched
(`main.go:44-45`), all six subcommands are present in the `switch` and in the
top-level usage string, and `scripts/verify-all-fixtures.sh`
(`SQLITE2PG_BIN` / `bin/sqlite2pg` / `./cmd/sqlite2pg`) and `.goreleaser.yaml`
(`main: ./cmd/sqlite2pg`, `binary: sqlite2pg`, cask `name: sqlite2pg` /
`binaries: [sqlite2pg]`) are all consistent.

Severity: **Low**

---

### L2 — A `--out` write failure shadows the verification verdict, and "report written to" prints before pass/fail is known

`cmd/sqlite2pg/verify.go:106-121`

```go
if reportFile != nil {
    closeErr := reportFile.Close()
    if ew.err != nil {
        return fmt.Errorf("writing report to %s: %w", *outPath, ew.err)
    }
    if closeErr != nil { ... }
    fmt.Printf("report written to %s\n", *outPath)
}

if !summary.passed() {
    return fmt.Errorf("verification FAILED: ...")
}
```

Two ordering issues, both cosmetic-to-mildly-misleading rather than
exit-code-wrong (the process exits non-zero in every case):

1. If the report file write fails *and* verification genuinely found
   mismatches, only the write error is reported. The operator is told the disk
   is full, not that their data is wrong. The brief asks specifically whether a
   genuine verification failure "isn't shadowed by a report-file error" — it is,
   in the message, though not in the exit code.
2. `report written to <path>` prints unconditionally once the file closes
   cleanly, including when verification then FAILs, so it appears above the
   `verification FAILED:` line. (Arguably correct — the report *was* written —
   but the brief asks that it print only on full success, and it does not.)

The `errWriter` itself is sound: the short-write latch is correct
(`n < len(p)` with nil `err` → `io.ErrShortWrite`), `e.err = err` on a
successful write cannot clear a previously latched error because the guard at
the top returns early, and `ew` is only dereferenced under `reportFile != nil`.
The early-error path at `verify.go:100-103` closing without checking is fine —
there is already an error to return.

Severity: **Low**

---

### L3 — `collateClauseRe` matches a `COLLATE` inside a string literal or CHECK expression

`internal/sqlitereader/collation.go:69` and `:107`.

`stripSQLComments` deliberately preserves quoted spans verbatim
(`collation.go:230-236`), and `parseColumnCollations` then runs
`collateClauseRe.FindStringSubmatch(rest)` over the whole remainder of the
column definition — literals included.

**Trigger:** `CREATE TABLE t (id TEXT PRIMARY KEY DEFAULT 'COLLATE NOCASE', ...)`,
or more plausibly a CHECK constraint such as
`name TEXT CHECK (name = upper(name) COLLATE NOCASE)` on a column that is itself
BINARY.

**Consequence chain:** `ColumnCollations` reports `NOCASE` for a genuinely
BINARY column. The single consumer is `primaryKeyOrderingIsSafe`
(`internal/pipeline/verify_load.go:259-267`), so the table drops from the
precise, streaming, PK-ordered verification path to `verifyTableUnordered`,
which holds an entire column's values from both sides in memory. Fail-safe in
direction — the parser can only over-report non-BINARY, never under-report — so
the result is degraded precision and memory cost on a large table, not a wrong
verdict.

The rest of the rewritten parser held up under adversarial reading. I walked the
`skipQuoteOrComment` state machine against every interleaving the brief names:
a comment opener inside a quoted string (the quote is entered first and consumed
whole), a quote opener inside a comment (same, in reverse — the byte-by-byte
callers hit the `--` / `/*` opener before the quote), `--` at end-of-input with
no newline (`IndexAny` returns -1 → consume to `len(s)`, correct), a lone
trailing `-` or `/` (`i+1 < len(s)` guard falls through to `return i`, correct),
`\r\n` line endings (consumes the `\r`, leaves the `\n` — harmless), doubled-quote
escaping in all three quote styles, and `[`-without-`]`. The one genuinely
fragile case — an unbalanced `[` outside quotes whose `]` is found inside a later
string literal — cannot arise from `sqlite_master.sql`, which SQLite only ever
populates with text it has already parsed. `columnListOpenParen`'s
`len(createSQL) - len(rest) + k` offset arithmetic is sound: every intermediate
(`rest` after the preamble strip, `TrimLeft`, `leadingIdentifier`'s `rest`) is a
true suffix of `createSQL`. `ColumnCollations`' known-column filter
(`collation.go:57-61`) does neutralize table-level constraint clauses as
claimed; the only way past it is a column literally named `CHECK`/`PRIMARY`/etc.
in matching case, which is not worth guarding against.

Severity: **Low**

---

### L4 — The post-load verification path writes the same report with no error latching

`cmd/sqlite2pg/postload_verify.go:191`

```go
summary, err := verifyLoadedTables(ctx, sourceDB, conn, cfg, out, out)
```

PR #137 (issue #136) latched write errors for `sqlite2pg verify --out`. The
`run --verify` / `load --verify` path calls the identical
`verifyLoadedTables` → `writeVerifyReport` engine, passing `out` (in practice
`os.Stdout`) as `reportOut` with no `errWriter`. `writeVerifyReport` does ~30
unchecked `fmt.Fprint*` calls.

**Trigger:** `sqlite2pg load --verify --pg ... config.yaml > report.txt` on a
full disk, with a clean verification.

**Consequence:** a truncated report and exit code 0. The same failure `--out`
now catches. This is a genuine asymmetry between the two report paths — the
very drift `verifyLoadedTables`' doc comment (`verify.go:129-135`) exists to
prevent — though it is a narrower one (stdout, not an explicitly named file).
`.golangci.yml:22-26`'s comment already accepts this trade-off in writing.

Severity: **Low**

---

### L5 — A `v*` tag can publish with failing gofmt, lint, tidy, or govulncheck

`.github/workflows/release.yml:22-36`, `.github/workflows/ci.yml:4-7`.

The release job runs `go build`, `go vet`, `go test`, then goreleaser. Since
steps are sequential in one job with no `if: always()`, a failing test **does**
block the publish — that part of the brief's question checks out.

But the comment above those steps says "Gate the release on the same checks CI
runs", and they are not the same checks: CI additionally runs `gofmt -l -e`,
`golangci-lint`, `govulncheck`, and the `go mod tidy` + `git diff --exit-code`
tidiness gate. None of those run in `release.yml`.

PR #130 also added `tags: ["v*"]` to `ci.yml`'s `on.push`, so CI *does* now run
on the tag — but as a separate workflow with no dependency edge to Release.
GitHub does not order or gate one workflow on another. The two race, and
goreleaser can finish (pushing binaries and the Homebrew cask to
`barrettclark/homebrew-tap`) while CI's Lint step is still red.

Nothing here passes vacuously in the narrower sense the brief asks about:
`gofmt -l -e . 2>&1` correctly captures parse errors into `$out` and fails on
non-empty output; `golangci-lint-action@v8` at a pinned `v2.13.2` with
`.golangci.yml`'s `default: none` + explicit enable list is a real run; the
`tidy-check` `git diff --exit-code go.mod go.sum` does cover both files that
`go mod tidy` can touch. `.goreleaser.yaml` validates — I ran
`goreleaser@v2.18.0 check` against it and it passed, so the `homebrew_casks:`
block (including `binaries:`) is schema-correct for the pinned version, and
nothing was lost by removing the `before:` hook (the builds section is
self-contained and CI now owns tidiness).

Severity: **Low**

---

### L6 — `sortKeyFor` and `valuesMatch` disagree for `int` and `float32` (latent)

`internal/pipeline/verify_load.go:689-726` and `:978-1053`.

`sortKeyFor`'s numeric case is `case int64, float64:` only. A plain `int` or a
`float32` falls to the `default:` arm, `fmt.Sprintf("\x09%T:%v", v, v)` →
`"\x09int:5"`. The Postgres side always comes back as `int64`/`float64` from
`pgColumnScanner.value`, keyed `"\x06num:5"`. So the unordered path
(`compareColumnUnordered`, which compares **keys only** — it never calls
`valuesMatch`) reports a mismatch, while the ordered path's `valuesMatch` falls
through to its `%v` fallback at `:1052` and reports a match. The documented
invariant — "two values `valuesMatch` would consider equal always produce the
same key" — does not hold for these two types.

This is latent, not live: `copywriter.Transform` normalizes `int` → `int64` in
every arm that handles it, and `database/sql` yields `int64`/`float64` from
`modernc.org/sqlite`, so no `int` or `float32` should reach the comparison. But
`transform.go` carries five separate `case int:` arms added specifically because
"the rest of the pipeline treats a plain int as an integer-shaped value", and
`toFloat64` accepts `float32` — so the pipeline clearly does not consider these
impossible. Flagged because this quartet is the brief's named
"same-three-functions" hot spot and the invariant should be closed by
construction, not by luck.

The NaN change itself (PR #131, issue #122 / L13) is correct and consistent:
`numericSortKey` sends NaN down the `numericKeyText` branch (`t == math.Trunc(t)`
is false for NaN) producing `"\x06num:NaN"` on both sides, matching
`exactNumericEqual`'s new `NaN == NaN` arm; `crossTypeNumericEqual` correctly
still refuses NaN-vs-int64 via `int64EqualsFloat64`'s `f != math.Trunc(f)`; and
±Inf keys as `"+Inf"`/`"-Inf"` with `e == a` agreeing. The issue #83 `[]byte`
key-namespace merge is likewise consistent in the direction that matters
(`[]byte` expected vs `string` actual is handled in `valuesMatch:1030` and keys
identically). The reverse — `string` expected vs `[]byte` actual — is *not*
handled in `valuesMatch` and would `%v`-mismatch, but that requires a bytea
target holding a TEXT-storage SQLite row, which pgx's `ByteaCodec` refuses to
encode at COPY time long before verify runs.

Severity: **Low**

---

### L7 — `fallbackTargetNeedsStorageCheck` is narrower than issue #84's new gate reads

`internal/pipeline/profile.go:482-489`, used at
`internal/pipeline/decide_column.go:148`.

```go
if !needsReview && best.SuggestedType != ddl.DropSentinel &&
    (best.TransformExpr != "" || fallbackTargetNeedsStorageCheck(best.SuggestedType)) {
```

```go
func fallbackTargetNeedsStorageCheck(target string) bool {
    switch target {
    case "integer", "bigint", "double precision", "timestamptz", "bytea":
```

The helper was written for `fallbackTypeFor`'s output vocabulary (the
zero-findings default_passthrough path, issue #69). PR #131 reused it verbatim
to gate the *heuristic-winner* path, whose vocabulary is different: heuristics
emit `boolean`, `date`, `jsonb`, `uuid`, `uuid[]` and `varchar(N)` as well. A
heuristic winning with one of those and an empty `TransformExpr` would
auto-approve with no full-table check — the exact gap #84 closed for the others.
Also note `smallint` is absent, so `fitsTargetType`'s int2 range check would not
run for a no-transform smallint decision either.

Latent today: I checked every `SuggestedType:` literal under
`internal/profiler/heuristics/` and every one of them pairs a concrete
non-passthrough type with a non-empty `TransformExpr`, so no registered
heuristic reaches the empty-transform branch with an uncovered type. Worth a
comment recording the coupling, since the two call sites now share a helper
whose domain assumption only holds for one of them.

Severity: **Low**

---

## Clean bill

### Re-verified this cycle (changed in `db35d39..HEAD`)

- **`internal/copywriter/transform.go` — the four arms PR #124 finished.**
  `iso8601_to_timestamptz` (string / `time.Time` / erroring `default`),
  `dayfirst_to_timestamptz`, `numeric_text_to_integer` and
  `numeric_text_to_double` are mutually consistent with the five earlier-round
  arms (`strip_commas`, `strip_commas_float`, `text_to_jsonb`,
  `nullif_sentinels`, `iso8601_to_date`). Each of the nine now has an
  `int64`/`int` arm where the pipeline can produce one, a `float64` arm with an
  explicit whole-number and `±2^63` bound where an int64 is the output, and an
  erroring `default`. The empty-string→NULL leniency asymmetry
  (`numeric_text_to_*` return `nil, nil`; `strip_commas*` error) is correct —
  it tracks each paired heuristic's own leniency, and `RejectNull` covers the
  PK / NOT NULL case. No arm has become a silent no-op:
  `verifyTransformAgainstFullTable` can still fail every one of them.
  `esri_typename` and `nullif_empty` are genuine, documented pass-throughs
  (dead code in the latter's case); M2 above is the one real gap.
- **`epochToInt64`'s new `float32` case** (issue #121 / L12) now matches
  `toFloat64`'s coverage exactly — `int64`, `int`, `float32`, `float64` on both,
  with the `float32` case recursing through the same NaN/range guard rather than
  duplicating it. `unix_epoch_seconds` (via `toFloat64`) and
  `unix_epoch_millis`/`micros` (via `epochToInt64`) now accept the same input
  set.
- **`rejectImplausibleTimestamp`'s bounds** (`-4713` / `294276`) match
  PostgreSQL's documented timestamptz range, and the sub-second split in
  `unix_epoch_seconds` (`math.Floor` + `math.Round((f-sec)*1e9)`) is correct for
  negative values (`f = -0.5` → `sec = -1`, `nanos = 5e8` → `-0.5s`) and
  degenerate for large `|f|` in the harmless direction (`f - sec == 0` exactly
  once `f` is integral).
- **`internal/sqlitereader/collation.go`'s third rewrite.** See L3 for the
  adversarial walk of `skipQuoteOrComment`, `stripSQLComments`, `matchingParen`,
  `splitTopLevelCommas`, `leadingIdentifier`'s doubled-quote handling, the
  `CREATE VIRTUAL TABLE` → `-1` short-circuit, and `columnListOpenParen`'s
  offset arithmetic. Only the string-literal `COLLATE` false positive (L3) is
  wrong, and it fails safe.
- **`internal/tui/logic.go`'s `dateTransformPreview` magnitude guard** (PR #126).
  The guard is `f >= float64(epochSecondsMin) && f <= float64(epochMicrosMax)`,
  and those are exactly the outermost bounds of the three `switch` arms inside —
  so no legitimate epoch value is skipped. `epochMicrosMax` is 2.05e15, below
  2^53, so `int64(f)` inside the guard is exact and defined. NaN fails
  `f == math.Trunc(f)`; ±Inf fails the bounds. An out-of-window `f` does fall
  through to the excel-serial / julian-day block at `:127` and then to the
  string transforms at `:141` — the fall-through is intact. (M1 is a different
  arm of the same file.)
- **`internal/pipeline/verify_load.go`'s numeric quartet.** Re-derived
  `exactNumericEqual` / `crossTypeNumericEqual` / `numericSortKey` / `sortKeyFor`
  / `valuesMatch` from scratch rather than trusting the comments. The NaN fix,
  the `[]byte` key-namespace merge, and the `isTextTargetType` `varchar(`
  prefix test are all mutually consistent; L6 records the one residual scope gap.
  `newPgColumnScanner`'s `default` arm correctly routes `varchar(N)` to
  `pgtype.Text`, and `primaryKeyOrderingIsSafe`'s no-transform precondition means
  a `varchar(N)` PK really does reach the `COLLATE "C"` branch it now qualifies for.
- **`internal/pipeline/profile.go`'s `varcharFinding`.** The boundary is right:
  `target > maxPostgresVarcharLen` (10485760), so exactly-at-the-limit still
  emits `varchar(10485760)`, which PostgreSQL accepts. The `text` fallback keeps
  `Confidence: 0.5` and `Heuristic: "varchar_length_preservation"`, identical to
  the varchar branch, so it is gated by the same below-threshold review path —
  the review flag is not lost. `sqlitereader.MaxTextLengths`' `CAST(... AS TEXT)`
  correctly forces character-counting for a BLOB-storage row, matching what
  `varchar(n)` actually limits, and its all-NULL → absent-from-map contract is
  handled at the call site (`profile.go:203`). The batching (one query per table)
  short-circuits cleanly on an empty column list.
- **`internal/ddl/foreign_keys.go` / `foreign_key_indexes.go` (PRs #128/#132).**
  The disambiguated constraint name round-trips: `foreignKeyStatement` uses the
  same `name` variable, quoted identically, in both the `DROP CONSTRAINT IF
  EXISTS` and the `ADD CONSTRAINT` subcommand, so a truncate-and-hash name works
  end to end. PostgreSQL processes `ALTER TABLE` subcommands in passes with
  drops ahead of constraint adds, so the single-statement form is safe.
  `printDryRunDDL` emits the same statements the real path does, in the same
  order (CREATE TABLE, then FK constraints, then FK indexes), all sorted.
  `disambiguateNamesReserving` still reserves the `pg_class` table names for
  index naming; FK constraint names do not consume `pg_class`, so not reserving
  them is correct.
- **`cmd/sqlite2pg/state.go` / `provision.go` / `main.go`'s state lifecycle.**
  `connectForLoad`'s full `writeState` overwrite on every non-resume run is what
  keeps `FKsApplied` and `Completed` from leaking across runs; `markTableCompleted`
  and `markForeignKeysApplied` are both read-modify-write and preserve the other
  fields. `runRunFinish` / `cleanupConfigAfterLoad` still hold issue #62's
  invariant. M4 is the one place the flag's semantics come apart from the config.
- **`errWriter` (PR #137)** — see L2; the writer itself is correct.
- **`internal/resolver/confidence.go`'s `Decide`** (PR #131, issue #106 / M3).
  The `secondBest` refactor from `float64` to a `profiler.Finding` +
  `haveSecondBest` flag is correct for a negative or zero confidence and for the
  single-finding case (where the loop never runs and `haveSecondBest` stays
  false), which the old `secondBest := -1.0` sentinel could not distinguish. The
  new agreement escape hatch does not break the one live disagreement pair:
  `boolean01`'s `textConfidence = 0.88` vs `numeric_text`'s `0.90` is a 2-hundredth
  gap and the two differ in both `SuggestedType` and `TransformExpr`, so review
  is still forced. The documented third-finding scope limit is real and
  correctly described.
- **`internal/tui/typepicker.go` / `tablelist.go`** (issue #93 / L7). The
  `defer`red `SetCurrentItem` runs after repopulation with a bounds check and
  cannot index out of range; calling `buildTableList` from `onTypeSelected` is
  safe (`m.tableList` is non-nil by then). `result.(bool)` and `result.(int64)`
  in `previewValueForType` cannot panic — `int_to_bool` and
  `numeric_text_to_integer` never return `(nil, nil)` for a non-nil string except
  on the empty string, which is explicitly handled.
- **`internal/pipeline/decide_column.go`'s `CheckFallbackFit`** plumbing
  (issue #84). `verifyTransformsAgainstFullTable`'s `Transform == "" &&
  !CheckFallbackFit` early-resolve, the per-column `remaining` bookkeeping, and
  the `errAllColumnsResolved` short-circuit are all correct; column names are
  unique per table so the shared `results` map cannot collide. L7 records the
  helper's domain mismatch.
- **`scripts/verify-all-fixtures.sh`.** `SQLITE2PG_BIN` / `bin/sqlite2pg` /
  `./cmd/sqlite2pg` are consistent with the rename; `statef="$cfg.state.json"`
  still matches `configPath + ".state.json"`; the `trap - EXIT INT TERM`
  disarm-then-exit pattern is right; the final `[ "$fail_count" = "0" ] &&
  [ "$error_count" = "0" ]` is genuinely the last command, so the exit status is
  real. Flag order in every invocation is correct (flags before positionals) —
  unlike the README, see M3.
- **`Makefile`.** `fmt-check` captures `2>&1` and fails on non-empty output;
  `tidy-check`'s `git diff --exit-code go.mod go.sum` covers both files `go mod
  tidy` writes; `lint` and `vulncheck` are pinned to the same versions CI uses;
  no target pipes a command whose exit code would be swallowed. `check`'s
  prerequisites run in order under a serial `make` (a `make -j check` would
  interleave them, but only `tidy-check` mutates the tree and nothing depends on
  the ordering for correctness of the verdict). `campaign: build` is a
  no-op prerequisite (`go build ./...` produces no `bin/sqlite2pg`; the script
  builds its own), harmless.
- **`.golangci.yml`.** I checked the errcheck exclusion claim rather than taking
  it: the only `(*os.File).Close` calls in non-test code are
  `internal/config/diff.go:19` (a read handle, for drift hashing) and
  `cmd/sqlite2pg/verify.go:101,107` (the `--out` report file, which #137
  handles explicitly). Every other `Close` in the tree is on `*sql.DB`,
  `*sql.Rows`, `pgx.Rows` or `*pgx.Conn`. Config and report files are written
  with `os.WriteFile` (`internal/config/save.go:16`,
  `internal/resolver/file_resolver.go:79`, `cmd/sqlite2pg/state.go:51`), which
  returns a checked error. So the exclusion is not hiding an unchecked
  write-then-close anywhere else. The `fmt.Fprint*` exclusion is genuinely
  suppressing one checkable error class — the post-load report path (L4) — and
  otherwise targets stdout/stderr/buffers. `staticcheck`'s three exclusions and
  `misspell`'s `DOUB` rule are correct as documented.
- **`.github/workflows/vulncheck.yml`** — fine, as the brief expected.

### Taken on trust — unchanged since `db35d39`

`git diff db35d39..HEAD` shows no functional change to these, so cycle 3's and
audit-final's clean bills stand without re-derivation:

- `internal/ddl/identifiers.go` (`disambiguateNames` /
  `disambiguateNamesReserving` / `truncateBytes`) and `generate.go` — comment-only
  changes in this range.
- `internal/config/` in full (comment-only changes plus one error-string rename).
- `internal/copywriter/pipeline.go` and `load.go` (the `TableSource`
  producer/consumer lifecycle) — comment-only change.
- `internal/sqlitereader/rows.go` (`normalizeBlobValue`), `schema.go`, `esri.go`,
  `containment.go`.
- `internal/pipeline/infer_foreign_keys.go`, and `verify_load.go`'s
  `canonicalJSON` / `writeCanonicalJSON` `big.Rat` path.
- `internal/review/` (state machine, `samples.go`) — comment-only changes; I did
  read `formatSampleValue` in full as the evidence base for M1.
- `internal/profiler/{dayfirst,heuristic,timestamp}.go` and every
  `internal/profiler/heuristics/*.go` except `boolean01.go` (comment-only, and
  re-verified above) — though `julian_day.go` and `excel_serial_date.go` were
  read in full as the evidence base for M2.
- `cmd/sqlite2pg/progress.go`, `provision.go`, `resolve_helpers.go` (moved, not
  changed) — `provision.go` was re-read in full while checking M4.
- Concurrency: no new goroutine in this range. `readAnswerWithDeadline` is
  unchanged since `7a19a37`, which cycle 3 verified.

---

## Not reviewed

- `*_test.go` files — read for context (`transform_cycle3_test.go`,
  `collation_cycle3_test.go`, `collation_fuzz_test.go`,
  `verify_load_nan_cycle3_test.go`, `varchar_finding_cycle3_test.go`,
  `resume_fk_*_integration_test.go`, `verify_out_error_test.go`,
  `typepicker_test.go`) but not review targets per the brief.
- `testdata/` fixtures and the checked-in fuzz corpus.
- `docs/` other than the two plan files the brief named.
- `go.mod` / `go.sum` dependency-version review — `make vulncheck` and the
  weekly workflow own that.
- Runtime behavior against a real Postgres or a real SQLite corpus. Everything
  above is derived from source, with three exceptions I did execute:
  `goreleaser@v2.18.0 check` (passes), and the two `sqlite2pg load` /
  `sqlite2pg run` invocations that confirm M3. M2's trigger and M4's config-edit
  path are the right candidates for Phase B's load-test campaign to confirm
  empirically.
- Whether `HOMEBREW_TAP_GITHUB_TOKEN`'s GitHub scopes are minimal — still not
  determinable from the repository, same as cycle 3 noted.
- GitHub issue #3 (deferred PostGIS/geometry target types) — out of scope by
  standing instruction.
