# Whole-codebase correctness audit — `internal/` + `cmd/`

Fresh-eyes, non-diff-scoped pass over every non-test file in `internal/` and
`cmd/` as of branch `audit-cycle-2-closeout` (db35d39). Read-only; nothing was
modified.

Findings are ordered by severity. Each names the concrete input that triggers
it and the observable wrong behavior. A "clean bill" section at the end lists
what was read carefully and found sound, so triage knows coverage.

---

## High

### H1. `isTextTargetType` never matches the `varchar(N)` type the profiler actually produces → verify walks the two sides in different row orders

`internal/pipeline/verify_load.go:278-285` (used at `:301`)

```go
func isTextTargetType(targetType string) bool {
	switch targetType {
	case "text", "varchar":
```

The only varchar-shaped target type this codebase can ever produce is
`fmt.Sprintf("varchar(%d)", n)` — see `varcharFinding` at
`internal/pipeline/profile.go:386-393`. Bare `"varchar"` is not in
`review.TypeOptions` (`internal/review/review_model.go:18-22`) and no code path
emits it, so the `"varchar"` arm is dead while the live form falls through to
`false`.

Consequence: for a table whose primary key is a `VARCHAR(N)`-declared text
column, `primaryKeyOrderingIsSafe` returns true (no transform, BINARY
collation), so `verifyTableOrdered` runs — but the PK's `ORDER BY` expression
is emitted **without** `COLLATE "C"`. Postgres then orders by the database's
default collation (e.g. `en_US.UTF-8`) while `StreamTableOrdered` orders by
SQLite BINARY. The two sides walk different row orders and every column of the
table mismatches.

Trigger: a MySQL-origin export (the exact case `varcharSuggestions` targets)
with a `VARCHAR(N)` text primary key and at least two `VARCHAR` columns of
differing N, where the reviewer accepts the suggestion (or presses `f` /
"Finish" which marks everything reviewed as-is). Concrete data: PK values
`"Makefile.in"` and `"aclocal.m4"` — the exact pair the comment at `:302-310`
cites — sort in opposite relative order under BINARY vs `en_US.UTF-8`.

This is precisely the cross-file drift class the audit brief flagged: the
comment at `:273-274` asserts `"text"` and `"varchar"` are "the only text-shaped
entries `review.TypeOptions` offers today", which was never true of the
generated value.

Severity: **High** (mass false-positive verification failure on a realistic
schema shape; also silently disables the collation protection the whole
`primaryKeyOrderingIsSafe` mechanism exists to provide).

---

### H2. `migrate load --resume` cannot resume the table it failed on — `CREATE TABLE` is unconditional

`cmd/migrate/main.go:558-579` (the DDL/COPY loop), with
`internal/ddl/generate.go:88` emitting a bare `CREATE TABLE %s (`

`executeLoad` only records a table in the state file *after* its COPY
completes (`markTableCompleted`, `:575`). If the COPY fails mid-table (a
transform error, a not-null violation, a network drop), the table has already
been created by the `conn.Exec(ctx, stmt)` at `:565` and that DDL is committed.

On `migrate load --resume`, `connectForLoad` reconnects to the **same**
database (`cmd/migrate/provision.go:95-118`), the interrupted table is not in
`Completed`, so the loop re-runs `CREATE TABLE` for it and Postgres rejects it
with `relation "..." already exists`. The resume path therefore fails on the
first table it is supposed to resume, every time.

Trigger: any load that fails partway through table N of M. `--resume` then
aborts immediately instead of continuing from table N.

Fix shape (not applied): `CREATE TABLE IF NOT EXISTS`, or `DROP TABLE IF
EXISTS` for the not-yet-completed table before recreating it (the COPY itself
is atomic per-statement, so no partial rows survive — but the empty relation
does).

Severity: **High** (the `--resume` feature is non-functional for its primary
scenario).

---

### H3. `iso8601_to_date` silently truncates time-of-day for `time.Time` values, bypassing its own guard

`internal/copywriter/transform.go:109-112`

```go
case "iso8601_to_date":
	s, ok := raw.(string)
	if !ok {
		return raw, nil          // <-- time.Time passes straight through
	}
```

The non-midnight rejection immediately below (`:126-128`, issue #42) only runs
for `string` inputs. But `modernc.org/sqlite` scans `DATE`/`DATETIME`/
`TIMESTAMP`-declared columns straight into `time.Time` (documented at
`internal/pipeline/profile.go:271-280`, and the `ISO8601` heuristic explicitly
handles that case at `internal/profiler/heuristics/iso8601.go:26-34`). So a
`DATETIME`-declared column whose 500-row sample is all-midnight gets
`SuggestedType: "date"` + `TransformExpr: "iso8601_to_date"`, and every value
reaching the transform is a `time.Time`, not a string.

Consequence chain:
1. `verifyTransformsAgainstFullTable` runs the real transform over every row
   (`internal/pipeline/verify_transform.go:100`). Every row returns
   `raw, nil` with no error, so the full-table check *always* passes for this
   column — the same "the transform can never fail, so full-table verification
   is a silent no-op" defect issues #22 and #42 were filed for, reintroduced
   through the type-switch fall-through.
2. At COPY time, pgx encodes the `time.Time` into a `date` column and Postgres
   discards the time-of-day. A `2019-03-04 14:22:00` row that the sample
   missed is silently stored as `2019-03-04`.

`migrate verify` *does* catch it after the fact (expected carries the
time-of-day, actual is midnight), but the design intent — route the column to
review before loading — is defeated.

Trigger: a `DATETIME` column that is midnight in the sampled 500 rows and has
at least one non-midnight row elsewhere. chinook.db's `employees.BirthDate` is
exactly the all-midnight `DATETIME` shape that exercises this path.

Severity: **High** (silent data loss on the load path plus a dead full-table
guard).

---

## Medium

### M1. The type picker attaches an empty transform for `boolean` and the numeric types, promising types the real COPY rejects

`internal/tui/logic.go:187-234` (`previewValueForType`), consumed by
`commonTransformForType` (`:283-303`) and `onTypeSelected`
(`internal/tui/typepicker.go:120-135`)

`previewValueForType` returns `transform == ""` for `integer`/`bigint`/
`smallint`, `real`/`double precision`/`numeric`, `boolean`, `jsonb`, and
`text`. Transforms are only ever derived for `date`/`timestamptz`/`uuid`/
`uuid[]`. The doc comment justifies this as "a directly-compatible raw value
… which pgx's COPY protocol accepts unconverted", which is false for several
reachable combinations, because COPY uses the **binary** format:

- **`boolean`.** SQLite has no boolean storage class; the raw value is always
  `int64` or `string`. Picking `boolean` on a column whose current target is
  *not already* `boolean` yields `Transform: ""`, and pgx cannot binary-encode
  `int64`/`string` into `bool`. Reachable whenever a human converts an
  `integer`-typed (default_passthrough, or `_id`-suffixed and thus excluded
  from `Boolean01` per `boolean01.go:19`) 0/1 column to boolean — the single
  most common review action this tool exists for. (The `int_to_bool` transform
  survives *only* on the re-confirm path at `typepicker.go:123-124`, where
  `typeName == col.TargetType`.)
- **`integer`/`bigint`/`smallint` on a TEXT column.** `previewValueForType`
  validates via `strconv.ParseFloat` of the *display string*, so `"1998"` in a
  TEXT column validates as `integer` with no transform. The correct transform
  (`numeric_text_to_integer`) is never attached; the raw Go `string` reaches
  pgx's int codec and COPY fails.
- **`integer` on a REAL value.** `previewValueForType` previews `"3.7"` as
  `"3"` and marks it valid, but with `Transform: ""` the raw `float64` 3.7 is
  handed to pgx for an `int4` column. The preview promises a truncation that
  never happens.
- **`jsonb`.** Falls into the `default:` arm at `:255`, so *every* string
  validates. The picker offers `jsonb` for a column of plain prose, with no
  `text_to_jsonb` validation; COPY then fails with `invalid input syntax for
  type json`.

This is the same "the picker promises a type the real COPY rejects" defect
class issue #27 fixed for `smallint` range, still open for transforms.

Severity: **Medium** (requires a human override, but the boolean case is the
tool's flagship review interaction).

### M2. `previewValueForType` routes integer previews through `float64`, reintroducing issue #15's precision loss in the UI

`internal/tui/logic.go:192-202`

```go
f, err := strconv.ParseFloat(value, 64)
...
n := int64(f)
```

`copywriter.parseWholeNumberText` (`internal/copywriter/transform.go:397-410`)
exists specifically because `ParseFloat` + `int64()` corrupts values past
float64's ~15-17 significant digits (the bikes.db `legacy_id` fixture). The TUI
never got the same fix.

Trigger: previewing `"1234567890123456789"` under `bigint` displays
`1234567890123456768` (off by 51) and reports `FitsRange` OK. Also,
`math.MaxInt64`-scale values saturate rather than erroring, so the picker
offers `bigint` for values it will render wrong.

Severity: **Medium** (display-only in isolation, but it is the number a human
uses to decide the column's type, and it silently contradicts the
`parseWholeNumberText` invariant it is meant to mirror).

### M3. `excelSerialToTime` overflows `time.Duration` for out-of-range serials, producing a silently wrong timestamp

`internal/copywriter/transform.go:460-466`

```go
return excelEpoch.
	Add(time.Duration(days) * 24 * time.Hour).
	Add(time.Duration(fracSeconds * float64(time.Second)))
```

`time.Duration` is int64 nanoseconds, capped at ~292 years. `ExcelSerialDate`
(`internal/profiler/heuristics/excel_serial_date.go:38-52`) only requires that
**50%** of sampled values land in `[36526, 49310]` — it explicitly tolerates a
minority outside that window, and the transform is then applied to *every* row.

Trigger: an `INTEGER`-declared column named e.g. `created_at` where 60% of
rows are Excel serials (~44000) and 40% are epoch-seconds (~1.7e9).
`time.Duration(1.7e9) * 24 * time.Hour` overflows int64 and wraps, so the
transform returns an arbitrary wrong timestamp with **no error**. Nothing
catches it: `verifyTransformAgainstFullTable` sees no error and
`fitsTargetType` has no opinion on a `time.Time`, and `migrate verify`
recomputes the same wrong value on both sides so it compares equal.

Severity: **Medium** (silent corruption; requires a mixed-magnitude column,
but the heuristic is explicitly designed to accept those).

### M4. `migrate verify` reports 100% false mismatches for a `[]byte` value in a `text`-target column

`internal/pipeline/verify_load.go:961-1024` (`valuesMatch`) and `:678-709`
(`sortKeyFor`)

`fallbackTypeFor` returns `"text"` for a TEXT-declared column whose sample is
all strings (or all NULL, via `fallbackTypeFromDeclared`), and `"text"` is
explicitly exempt from every storage-class check
(`fallbackTargetNeedsStorageCheck`, `internal/pipeline/profile.go:430-437`).
SQLite's dynamic typing permits a BLOB row in that same column.

At COPY, pgx's `TextCodec` happily accepts `[]byte` and writes it as text — the
load succeeds and the data is correct. At verify:

- `expected` is `[]byte` (raw, no transform), `actual` is `string` (from
  `pgtype.Text`).
- `valuesMatch` enters the `case []byte:` arm, the `actual.([]byte)` assertion
  fails, and it drops to the `fmt.Sprintf("%v", …)` fallback at `:1024`, which
  renders `[]byte("hi")` as `"[104 105]"` vs `"hi"` → mismatch.
- In the no-PK path, `sortKeyFor` gives `"\x04bytes:hi"` vs `"\x08string:hi"` →
  mismatch at a different sorted position too.

Every such row is reported as a data-integrity failure that isn't one. This is
the "verify doesn't model every type decision the load path can make" bug
class the brief asked to re-check; `expectedForCompare`
(`verify_load.go:918-925`) still only normalizes `jsonb`.

Related latent issue: the `%v` fallback and `sortKeyFor`'s `default:` arm
(`"\x09%T:%v"`, which *includes the type*) disagree by construction — any pair
the fallback calls equal gets different sort keys, so the ordered and unordered
paths can reach opposite verdicts on the same data. Documented invariant
("two values `valuesMatch` considers equal always produce the same key",
`:643-648`) does not hold for the fallback arm.

Severity: **Medium**.

### M5. Auto-approved decisions with a non-empty target type but no transform skip full-table verification entirely

`internal/pipeline/decide_column.go:141`

```go
if !needsReview && best.TransformExpr != "" && best.SuggestedType != ddl.DropSentinel {
```

Issue #69 added `CheckFallbackFit` so a zero-findings `default_passthrough`
decision to a concrete non-text type still gets a full-table storage-class
scan. That protection is **not** extended to the "a heuristic won, and its
transform is empty" branch: such a column drops through to
`buildColumnConfig` at `:159` with no full-table check at all.

Today this is only reachable through `varcharFinding` (0.5, always
below-threshold, so it lands in review rather than auto-approving) — so it is
currently latent rather than live. But the condition encodes "no transform
⇒ nothing can go wrong", which issue #69 already proved false for the other
path, and any future heuristic that suggests a concrete type with no transform
inherits the gap silently.

Related, and live today: a `varchar(N)` target is never length-checked against
the data. `varcharSuggestions` derives N purely from the declared type, and
SQLite never enforced it. If the reviewer accepts the suggestion (or presses
`f`), a row longer than N aborts the COPY with `value too long for type
character varying(N)`. No sample-level or full-table check covers this.

Severity: **Medium**.

### M6. `nullif_sentinels` returns a raw `string` for any non-integer numeric value, on a numeric target column

`internal/copywriter/transform.go:325-339`

```go
cleaned := strings.ReplaceAll(s, ",", "")
if n, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
	return n, nil
}
return raw, nil            // <-- string into an integer/double column
```

`SentinelNull` can suggest `"double precision"` (when `sawFloat`, i.e. a
sampled value matched `plainNumberPattern` with a `.`, or a
`commaNumberPattern` value with a decimal component —
`internal/profiler/heuristics/sentinel_null.go:56-72`). For such a column,
`"1,234.56"` fails `ParseInt`, falls through to `return raw, nil`, and pgx
cannot binary-encode a Go `string` into `float8`. The transform reports no
error, so `verifyTransformAgainstFullTable` passes the column clean and the
failure only surfaces mid-COPY.

Also unchecked: `SentinelNull` suggests `"integer"` (int4) with no int4 range
consideration of its own. `fitsTargetType` does catch that at full-table
verification time, so that half is covered.

Severity: **Medium** (load-time failure, not silent corruption, but it defeats
the pre-flight check that is supposed to route the column to review).

### M7. `text_to_jsonb` / `strip_commas` / `strip_commas_float` fall through for non-string values

`internal/copywriter/transform.go:48-64`, `:167`

`GeoJSON.Evaluate` (`internal/profiler/heuristics/geojson.go:22-31`) and
`CommaNumber.Evaluate` (`comma_number.go:26-31`) both **skip** non-string
samples with `continue` rather than disqualifying the column. So:

- A TEXT column of GeoJSON strings plus a few BLOB rows → `jsonb` target;
  `text_to_jsonb` returns `raw, nil` for the `[]byte`, full-table verification
  passes, and pgx feeds the raw blob bytes to the jsonb codec.
- A column with `float64` values and one `"1,234"` string → `CommaNumber`
  suggests `"integer"` with `sawFraction == false` (float samples never set
  it, since only strings are examined); `strip_commas` passes the `float64`
  through unchanged into an `int4` column, and `fitsTargetType`'s `asInt64`
  (`verify_transform.go:180-188`) has no `float64` case, so the range check
  says nothing.

Same root cause as H3: the `raw.(string)` type assertion's `!ok` branch is a
silent pass-through in a transform whose paired heuristic does not guarantee
string-only values.

Severity: **Medium**.

---

## Low

### L1. `Decide` forces review on any near-tie, even when both findings agree on type *and* transform

`internal/resolver/confidence.go:87-105`. The disagreement gate compares only
confidences; it never checks whether the two findings actually disagree about
anything. No currently-registered pair produces identical
`(SuggestedType, TransformExpr)` at a ≤0.02 gap, so this is latent — but it is
the mechanism issue #20 was filed against, and it will re-fire the moment two
heuristics converge on the same answer from different evidence.

### L2. Stale confidence-ladder documentation in `boolean01.go`

`internal/profiler/heuristics/boolean01.go:25-27` and `:35-38` both state
`resolver.disagreementMargin` is `0.04`. It is `0.02`
(`internal/resolver/confidence.go:64`), and the comparison is now on integer
hundredths, not raw floats. The behavior is still correct (gap of 2 ≤ 2 forces
review), but the reasoning a future editor would rely on is wrong. Docs-only.

### L3. `julianDayToDate` uses Go truncating division, not floor division

`internal/copywriter/transform.go:471-484`. Fliegel & Van Flandern requires
floor division; Go's `/` truncates toward zero. For `jdn < -68569` the result
is silently wrong. `JulianDay.Evaluate` requires *every* sample in
`[1721425.5, 2816787.5]`, but the transform still runs on unsampled rows, so a
negative or near-zero JDN elsewhere in the table produces a wrong date with no
error.

### L4. `toInt64` truncates `float64` toward zero for the epoch transforms

`internal/copywriter/transform.go:418-429`. `unix_epoch_seconds` on a REAL-
storage value `1712345678.9` yields `1712345678` (0.9s lost, silently). Minor,
and arguably intended, but it is an undocumented lossy conversion inside a
transform whose contract elsewhere is "error rather than lose data".

### L5. `parseColumnCollations` takes the first `(` in the CREATE TABLE text

`internal/sqlitereader/collation.go:126-133`. A table named `"foo(bar)"` makes
`matchingParen` find a `)` inside the quoted name, so the column-definition
body is parsed from the wrong offset and every column silently keeps the
BINARY default. Consequence is a NOCASE/RTRIM PK being treated as safe to
order by → the H1-class row-order divergence. Extremely obscure trigger.

### L6. `dateTransformPreview` parses the *display* string, which is `%v`-formatted

`internal/tui/logic.go:96-101` calls `strconv.ParseFloat(value, 64)` on the
grid's formatted value. `review.formatSampleValue`
(`internal/review/samples.go:56-70`) renders a `float64` via `%v`, which uses
`%g` and yields scientific notation for large magnitudes (`1.712345678e+09`),
and truncates any value over 40 runes with `…`. Genuine large-magnitude
values therefore fail the plausibility windows and the picker declines to
offer `timestamptz`/`date` for them.

### L7. `buildTableList` per-table counts are never refreshed after a decision

`internal/tui/tablelist.go:9-45` is called once in `Run`
(`internal/tui/app.go:52`); `onTypeSelected` refreshes `m.summary` and the grid
but not the table list. The needs-review/auto-approved counts and the list
title go stale for the rest of the session. Cosmetic.

### L8. `readAnswerWithDeadline` treats an empty line as "no answer given"

`cmd/migrate/postload_verify.go:110-113` returns `ok = line != ""`, so a
scripted bare newline (`printf '\n' | migrate load …`) is reported as "stdin
… no answer was provided" rather than as the explicit "N" the user typed. The
resulting behavior (skip verification) is the same; only the message is
misleading.

---

## Clean bill — checked carefully, nothing found

These were read in full and reasoned through against the specific bug classes
the brief called out. Recording them so triage knows they were covered rather
than skipped.

- **`internal/resolver/confidence.go` — `Decide`'s top-two selection.** Walked
  the `best`/`secondBest` update loop against ties, equal-to-best values,
  descending input, and the single-finding case. The integer-hundredths
  comparison is sound and the ladder invariant (smallest clean-win gap 0.03 >
  margin 0.02 ≥ the intentional 0.88/0.90 disagreement gap) holds against
  every `Confidence:` literal actually present in
  `internal/profiler/heuristics`. Only the *documentation* in `boolean01.go`
  has drifted (L2). No arbitration bug.
- **`internal/ddl/identifiers.go`.** `disambiguateNames` /
  `disambiguateNamesReserving` / `disambiguateOne` are order-independent, pure
  in `(display, identity, reserved)`, and stable across runs. The salt loop
  terminates. `quoteIdent` correctly delegates to `pgx.Identifier.Sanitize` and
  matches the COPY path. Truncation is byte-wise, matching Postgres.
- **`internal/ddl/generate.go`.** Column ordering, PK sequencing (sorted by
  `PrimaryKeySeq`, dropped columns omitted from both list and PK clause),
  `ErrNoIncludedColumns` / `ErrMissingColumnOrder` handling, and the
  `NOT NULL` / inline `PRIMARY KEY` emission all check out.
- **`internal/ddl/foreign_keys.go` and `foreign_key_indexes.go`.** Constraint
  names are per-table (correct — `pg_constraint` is scoped by `conrelid`);
  index names are disambiguated schema-wide *and* against the table-name set
  (correct — `pg_class` is shared). The `perTable` → flat `names` → `i++`
  re-walk is index-consistent because both loops iterate the same nested
  structure in the same order. Local and referenced column names are both
  mapped through the right table's `PostgresColumnNames`.
- **`internal/copywriter/pipeline.go` — the producer/consumer COPY source.**
  `done`/`doneOnce`/`errCh(1)` shutdown is correct; `Close` is idempotent; the
  `errClosed` unwind releases the SQLite cursor; `Next` drains `errCh`
  non-blockingly only after `rowsCh` closes, which is safe because the producer
  sends on `errCh` before `defer close(ts.rowsCh)`… (verified: `close(rowsCh)`
  is deferred and therefore runs *after* the `errCh` send, so the error is
  always visible by the time `Next` looks). No leak, no race.
- **`internal/pipeline/verify_transform.go`.** The batched multi-column scan
  keeps genuinely independent per-column first-violation state; `remaining` is
  decremented exactly once per column (guarded by the `results` membership
  check at the top of the loop); `errAllColumnsResolved` is correctly filtered
  out of the returned error; every active column that never violated is
  back-filled as OK. `RejectNull` is wired from `PrimaryKeySeq > 0 ||
  NotNull`, matching what `GenerateCreateTable` actually emits.
- **`internal/pipeline/verify_load.go` numeric comparison.**
  `exactNumericEqual` / `crossTypeNumericEqual` / `int64EqualsFloat64` /
  `numericSortKey` are mutually consistent: the pairs `crossTypeNumericEqual`
  calls equal are exactly the pairs `numericSortKey` maps to a shared key, and
  the 2^53 / int64-range boundary is applied identically in both. The
  three-times-broken invariant is intact for the numeric case. (It is *not*
  intact for the `%v` fallback arm — see M4.)
- **`canonicalJSON` / `writeCanonicalJSON`.** `UseNumber` + `big.Rat` avoids
  the float64 collapse; the trailing-token check correctly mirrors
  `json.Unmarshal`'s rejection of `"1 2"`; key sorting and the `#` numeric
  marker make the key unambiguous. Sound.
- **`internal/sqlitereader/rows.go`.** `normalizeBlobValue`'s zero-length-BLOB
  vs NULL distinction is correct and applied on every read path (`SampleColumn`,
  `SampleNonNullColumn`, `SampleRows`, `streamQuery`). Each row gets a fresh
  copy of `dest`, so no aliasing across callback invocations.
- **`internal/sqlitereader/schema.go`.** FK grouping by `id` with `seq`
  ordering, the implicit-`REFERENCES` PK resolution with its memo + skip
  caches, and the `no such module:` virtual-table skip (narrow enough not to
  swallow real errors) are all correct.
- **`internal/sqlitereader/collation.go`.** `splitTopLevelCommas` handles all
  four quoting styles and nested parens; `leadingIdentifier`'s `end <= 0` /
  `end <= 1` empty-identifier guards are right; filtering through the
  known-column set correctly neutralizes table-level constraint clauses. Only
  the L5 first-paren edge case.
- **`internal/config/*`.** Version gate, SHA-256 drift detection, YAML
  round-trip. Nothing wrong.
- **`cmd/migrate/provision.go`.** `deriveDatabaseName` sanitization, the
  leading-digit guard, and the length budget (`maxDatabaseNameLen - len(suffix)`)
  are all correct, including the `strings.Trim`-to-empty → `"db"` fallback.
- **`cmd/migrate/verify.go` report generation.** Summary aggregation matches
  the per-table results, the `Ordered == false` caveat is printed, and the
  "… and N more" arithmetic is right.
- **`internal/pipeline/infer_foreign_keys.go`.** Self-reference and own-PK
  exclusions, the `"id"`-only rejection, single-column-PK requirement, and the
  `nonNullCount == 0 ⇒ not contained` rule are all correct; suggestions are
  never auto-applied.
- **Concurrency.** The only goroutines in the codebase are `TableSource`'s
  producer, `tui.Run`'s ctx watcher, and `readAnswerWithDeadline`'s one-shot
  reader. All three are correct; the last deliberately leaks a parked
  goroutine at process exit, which is documented and harmless. Nothing new to
  report beyond the clean `-race` history.

## Not reviewed

- `*_test.go` files (per the brief — context only, not review targets).
- `scripts/`, `testdata/`, `docs/`.
- GitHub issue #3 (deferred PostGIS/geometry target types) — out of scope by
  instruction.
