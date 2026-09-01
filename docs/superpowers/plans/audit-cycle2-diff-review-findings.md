# Audit cycle 2 — fresh-eyes correctness review of `aa1f44b..main` (`internal/`, `cmd/`)

## Summary

Nine findings: **3 High**, **4 Medium**, **2 Low**. Overall the diff is in good
health — the identifier-disambiguation work (issues #21/#43/#44) is now
consistently routed through `ddl.PostgresTableNames`/`PostgresColumnNames` at
every call site I could find (CREATE TABLE, COPY, FK constraints, FK indexes,
dry-run, and `verify`), the FK-index and constraint naming paths do agree on
their generated names, the `TableSource` goroutine lifecycle is correct
(`errCh` send strictly happens-before the deferred `close(rowsCh)`, `doneOnce`
guards the double-close), and the `resolver.Decide` hundredths rework is
correct against the documented ladder.

**Explicit clean bill on the numeric-comparison hot spot.** I traced
`valuesMatch` / `exactNumericEqual` / `numericValue` / `numericSortKey` /
`sortKeyFor` against every case their doc comments claim, including the three
historical regressions (int64/float64 type-tagging, `fmt.Sprintf("%v")`
scientific notation, and float64 precision loss above 2^53). The same-type /
cross-type split is structurally sound and all three past bugs are genuinely
fixed. The one residual crack is Finding 6 below, and it is a narrow edge, not
a repeat of any of the three.

The real problems are concentrated in one place: **`verify` does not know
about every transform and type decision the load path can make.** Findings 1,
2 and 4 are all instances of that, and all three cause `verify` — now wired
into `run`/`load` by default via `--verify` — to report FAIL on a load that
was completely correct. Finding 3 is the cleanest cross-commit contract break
in the diff.

---

## Finding 1 — `verifyTableOrdered` assumes the PK's SQLite sort order equals its Postgres sort order, which is false whenever the PK column's type was changed by a transform (Severity: **High**)

**File:** `internal/pipeline/verify_load.go:264-299` (`verifyTableOrdered`),
`internal/pipeline/verify_load.go:232-243` (`primaryKeyOrderingIsSafe`),
`internal/pipeline/verify_load.go:252-259` (`isTextTargetType`)

**Commits involved:** `a4fd540` (introduced the PK-ordered comparison),
`f8f1524` (added PK ordering as the fix for scan-order drift), `e6bc33e`
(added the `COLLATE "C"` forcing), `50b77d8` (added
`primaryKeyOrderingIsSafe`, closing only the *collation* half of the ordering
question).

**Concrete failure scenario:**

```sql
-- SQLite source
CREATE TABLE items (id TEXT PRIMARY KEY, label TEXT);
INSERT INTO items VALUES ('1','a'),('2','b'), ... ,('10','j'),('11','k');
```

`heuristics.NumericText` (`internal/profiler/heuristics/numeric_text.go`)
applies to any TEXT/CHAR-declared column, fires on plain digit strings, and
returns `SuggestedType: "integer"` (or `"bigint"`) with
`TransformExpr: "numeric_text_to_integer"` at confidence 0.90 — which is
**not** below the default `--threshold 0.9`, so it auto-approves with no human
in the loop.

At verify time:

- `primaryKeyOrderingIsSafe` reads `ColumnCollations` → `id` is `BINARY` →
  returns `true` → the ordered path runs.
- SQLite side: `StreamTableOrdered(..., ORDER BY "id")` — `id` is still TEXT
  in SQLite, so the order is `'1','10','11','2', ...`.
- Postgres side: `isTextTargetType("integer")` is `false`, so no `COLLATE "C"`
  is applied, and `id` is a real `integer` column → `ORDER BY "id"` gives
  `1,2,3,...,10,11`.

The two sides walk the table in genuinely different orders and are then
compared *by position*. Row 1 pairs SQLite `id='10'` (expected `int64(10)`)
against Postgres `id=2`. Essentially every row of every column mismatches.
`migrate run --verify` / `migrate load --verify` then exits non-zero with
"*** LOAD SUCCEEDED, BUT POST-LOAD VERIFICATION FOUND A PROBLEM ***" on a
load that was byte-for-byte correct.

Same shape, less noisy, for a TEXT PK mapped to `uuid` via `uuid_format`
whenever the source stores UUIDs in upper- or mixed-case: SQLite orders the
ASCII text (`'F...' < 'a...'`) while Postgres orders the 16 raw bytes
(`0xA0 < 0xFF`).

What should happen: verify should either order both sides by something the
transform preserves, or degrade to `verifyTableUnordered` (which would pass
here) whenever a PK column carries a `Transform` / a `TargetType` that isn't
order-isomorphic with the SQLite column's storage class.

**Why it's a cross-commit interaction:** `f8f1524`/`e6bc33e` established the
contract "both sides are ordered by the primary key, therefore positional
comparison is valid." `50b77d8` correctly noticed one way that contract can
break (collation) and guarded it — but only that one. Nothing in the chain
ever revisited the fact that the *value* on the Postgres side is not the same
value as on the SQLite side whenever a transform ran, so "ordered by the same
key" was never actually true for transformed PKs. The guard's own doc comment
enumerates exactly one hazard and reads as exhaustive.

**Suggested direction:** Extend `primaryKeyOrderingIsSafe` to also return
`false` when any PK column has a non-empty `Transform`, or when its
`TargetType` is not in the same order-preserving family as its SQLite storage
class (text→text, integer→integer, real→float). Degrading to the unordered
path is already the established safe fallback.

---

## Finding 2 — `verify` compares raw source text against Postgres's canonicalized `jsonb` output, so every `jsonb` column fails verification (Severity: **High**)

**File:** `internal/pipeline/verify_load.go:682-708` (`newPgColumnScanner`
default case), `internal/copywriter/transform.go:164-180` (`text_to_jsonb`)

**Commits involved:** `a4fd540` (verify's scanner/comparison), interacting with
the pre-existing `geojson_text` heuristic + `text_to_jsonb` transform; surfaced
as a real user-facing failure by `b78d905` (post-load verify wired into
`run`/`load`).

**Concrete failure scenario:** A source column with GeoJSON text —
`heuristics.GeoJSON` returns `SuggestedType: "jsonb"`,
`TransformExpr: "text_to_jsonb"` at confidence 0.90 (auto-approves). A row
holds the compact form:

```
{"type":"Point","coordinates":[1,2]}
```

- Load: `Transform("text_to_jsonb", raw)` validates the JSON and returns
  `raw` **unchanged**; pgx writes that text into a `jsonb` column, and
  Postgres canonicalizes it on storage.
- Verify: `newPgColumnScanner("jsonb")` hits the `default:` branch →
  `pgtype.Text` → reads back Postgres's canonical rendering:
  `{"type": "Point", "coordinates": [1, 2]}` (space after every `:` and `,`;
  object keys re-ordered by length-then-bytes; numbers normalized; duplicate
  keys dropped).
- `valuesMatch("{\"type\":\"Point\",...}", "{\"type\": \"Point\", ...}")` →
  both are `string`, so the `case string` arm compares them directly → `false`.

Result: `MISMATCH t.geom: N of N row(s) differ` for every row of every jsonb
column, on a perfectly correct load. This hits precisely the Esri/Spatialite
geodatabase workload the project targets.

What should happen: verify should compare jsonb semantically — either
normalize the expected side through the same canonicalization (e.g. by asking
Postgres, or via a canonical Go JSON re-encode) or scan the column with
`::jsonb = $1::jsonb` semantics rather than as text.

**Why it's a cross-commit interaction:** `text_to_jsonb`'s contract, cemented
by issue #22, is deliberately "validate only, do not reshape the value" — its
doc comment says so explicitly, and `internal/tui/logic.go`'s `default:` case
(commit `0a6b7df`) relies on it ("jsonb's own transform only adds upfront
validation, it doesn't reshape the value pgx receives, so no transform is
needed here either"). `a4fd540` then built verify on the assumption that
`Transform`'s output is what Postgres holds. For `jsonb` that is false —
Postgres, not the transform, does the reshaping.

**Suggested direction:** Give `pgColumnScanner` an explicit `jsonb` case that
compares canonicalized JSON on both sides, or have `verifyTableOrdered`/
`verifyTableUnordered` select `jsonb` columns as `col::text` after routing the
expected value through the same normalization.

---

## Finding 3 — a post-load verification FAILURE still deletes the config and the state file, destroying the only record of what was loaded and where (Severity: **High**)

**File:** `cmd/migrate/main.go:157-171` (`runRunFinish`),
`cmd/migrate/main.go:190-216` (`cleanupConfigAfterLoad`)

**Commits involved:** `9e76e22` (issue #52 — cleanup extended to remove
`.state.json` alongside the config), `b78d905` (post-load verify inserted
before cleanup), `0264f29` (extracted `runRunFinish`, which cemented the
behavior).

**Concrete failure scenario:** `migrate run --pg ... --verify source.db`
(no `--keep-config`). The load succeeds; post-load verification finds a real
value mismatch. `runPostLoadVerify` returns a non-nil error and prints
"LOAD SUCCEEDED, BUT POST-LOAD VERIFICATION FOUND A PROBLEM — the data is
already in Postgres". `runRunFinish` then calls
`cleanupConfigAfterLoad(nil, configPath, false)` — hard-coded `nil` for
`loadErr` — which unconditionally removes:

- `source.db.migration.yaml` (every type decision, transform, rationale, and
  confidence that produced the suspect data), and
- `source.db.migration.yaml.state.json` (the only durable record of **which
  timestamped Postgres database** the data landed in).

The user is told there is a data-integrity problem with data that is already
in Postgres, and is simultaneously stripped of the ability to run
`migrate verify` again, to inspect what decisions were made, or to look up the
database name anywhere other than terminal scrollback.

What should happen: a verification failure should suppress cleanup for exactly
the same reason a load failure does — "so a user who hits a failure without
having anticipated `--keep-config` up front can still inspect what was decided
or retry."

**Why it's a cross-commit interaction:** `cleanupConfigAfterLoad`'s contract,
set by issue #38 and restated verbatim in `9e76e22`'s expanded doc comment, is
"the config is only ever removed after a load that actually succeeded... on
any load error it is left in place, independent of `--keep-config`."
`b78d905` introduced a *new* failure mode strictly after the point where that
decision is made, and passed `nil` rather than the new error into the function
whose whole job is to make that call. `0264f29`'s comment ("a verification
failure is reported but does not change what cleanup does") records the choice
without reconciling it against the invariant `9e76e22` had just documented.
This is the same two-features-one-artifact shape as issue #52 itself.

**Suggested direction:** Pass `verifyErr` into `cleanupConfigAfterLoad` (or add
a `keep bool` parameter derived from it) so a verification failure preserves
both files, and print the retained config/state paths in the failure message.

---

## Finding 4 — sub-microsecond timestamps from `excel_serial_to_timestamptz` (and RFC3339Nano input) can never match Postgres, which stores only microseconds (Severity: **Medium**)

**File:** `internal/copywriter/transform.go:238-243, 460-466`
(`excelSerialToTime`), compared in `internal/pipeline/verify_load.go:817-821`
(`valuesMatch`'s `time.Time` case) and keyed in
`internal/pipeline/verify_load.go:630-631` (`sortKeyFor`'s
`t.UTC().UnixNano()`).

**Commits involved:** `a4fd540` (verify's `time.Time` comparison and nanosecond
sort key), interacting with the pre-existing `excel_serial_date` heuristic;
user-visible via `b78d905`.

**Concrete failure scenario:** An Access/Excel-exported serial datetime column
(the `excel_serial_date` heuristic's own target, confidence 0.85). Verified
directly by running `excelSerialToTime`:

| serial | transform output | ns |
|---|---|---|
| `45000.4567` | `2023-03-15T10:57:38.880000212Z` | 880000212 |
| `43831.123456` | `2020-01-01T02:57:46.598400101Z` | 598400101 |
| `40000.3333` | `2009-07-06T07:59:57.119999863Z` | 119999863 |

Postgres `timestamptz` has microsecond resolution, so `.880000212` is stored as
`.880000`. Verify re-runs the identical transform, gets `.880000212` back,
and `expected.Equal(actual)` is `false` — one mismatch per row, for a load
that is as correct as `timestamptz` allows. (`sortKeyFor` keys off
`UnixNano()`, so the no-PK path fails identically.)

The same applies to `iso8601_to_timestamptz` whenever a source string carries
7+ fractional-second digits, which `time.RFC3339Nano` (first entry in
`profiler.timestampLayouts`) happily parses.

Only whole/half-day serials (`44197.5`, `44197.25`) come out µs-aligned, which
is likely why fixture testing missed this.

**Why it's a cross-commit interaction:** partly — `a4fd540` built verify on
"re-run the transform and the answer must be identical", a contract that holds
for every transform whose output round-trips losslessly through its target
Postgres type. `excel_serial_to_timestamptz` (and nanosecond ISO input)
predate verify and do not.

**Suggested direction:** Round/truncate both sides to microsecond precision in
`valuesMatch`'s `time.Time` case and in `sortKeyFor`'s time key, matching what
`timestamptz`/`timestamp` can actually store. (Truncating in the transform
itself would also fix it, and would make the load lossless-by-construction.)

---

## Finding 5 — the TUI derives a date/timestamp transform from a *single* sample while it validated the type against *all* samples (Severity: **Medium**)

**File:** `internal/tui/typepicker.go:100-113` (`onTypeSelected`),
`internal/tui/logic.go:80-121` (`dateTransformPreview`),
`internal/tui/logic.go:378-393` (`validTypesForColumn`)

**Commits involved:** `0a6b7df` (issue #41 — attach the transform a picker
option needed to the applied decision), interacting with `21a058a`
(`iso8601_to_date` now errors instead of truncating).

**Concrete failure scenario:** A TEXT column whose sampled values mix ISO and
compact date spellings, e.g. `["2021-06-01", "20210704", "2022-01-15"]`.

- `validTypesForColumn` offers `date` because `previewValueForType` succeeds
  for *every* value independently — `iso8601_to_date` handles the first and
  third, `yyyymmdd_to_date` handles the second.
- The human picks `date`. `onTypeSelected` then re-derives the transform from
  `firstNonNullValue(...)` only — a single sample — and stores
  `iso8601_to_date`.
- `ApplyDecision` sets `Reviewed = true`, which bypasses the profile-time
  full-table verification entirely (that only runs in `ProfileDatabase`).
- At COPY time, `Transform("iso8601_to_date", "20210704")` fails
  `ParseTimestamp` → the whole `migrate load` aborts mid-table with
  `t.col: iso8601_to_date: cannot parse "20210704"`.

A silent variant of the same bug exists for `timestamptz`: samples
`["1712345678", "40000"]` both validate (unix-epoch-seconds window and
Excel-serial window respectively), but the stored transform is
`unix_epoch_seconds`, so `40000` loads as `1970-01-01T11:06:40Z` with no error
at all — and `verify` re-runs the same wrong transform, so it agrees.

**Why it's a cross-commit interaction:** `validTypesForColumn` (pre-existing)
established "a type is offered only if *every* sample validates." `0a6b7df`
added transform derivation on top of it but used a different, weaker basis —
one sample — so the picker can now attach a transform that the very validation
which offered the type does not support.

**Suggested direction:** Derive the transform across all sample values in
`onTypeSelected` and only attach one when every non-NULL sample resolves to
the *same* transform; otherwise leave it unset (or refuse to offer the type).

---

## Finding 6 — `sortKeyFor` and `valuesMatch` disagree for cross-type numerics at the top of int64's range, breaking `sortKeyFor`'s documented invariant (Severity: **Medium**)

**File:** `internal/pipeline/verify_load.go:581-592` (`numericSortKey`),
`internal/pipeline/verify_load.go:802-815` (`valuesMatch`)

**Commits involved:** `e6bc33e` (cross-type numeric equality), `50b77d8`
(precision fix that introduced `numericSortKey`'s int64-range window).

**Concrete failure scenario:** `sortKeyFor`'s doc comment states the invariant
"two values `VerifyTable` would otherwise consider equal (per `valuesMatch`)
always produce the same key." Take `expected = int64(math.MaxInt64)` (a bigint
rowid) and `actual = float64` as scanned from a `double precision` column
(which is exactly `2^63 = 9223372036854775808.0`, since `MaxInt64` is not
representable):

- `valuesMatch`: `exactNumericEqual` returns `ok=false` (different concrete
  types) → `numericValue` converts `int64(MaxInt64)` to `9223372036854775808.0`
  → **equal**, no mismatch.
- `numericSortKey(int64)`: `strconv.FormatInt` → `"9223372036854775807"`.
- `numericSortKey(float64 2^63)`: the window check is
  `t < int64UpperBoundAsFloat` where that constant *is* `2^63`, so the
  exclusive bound excludes this value → falls through to `numericKeyText` →
  `"9223372036854775808"`.

Different keys. So the same data **passes** on a table with a primary key
(`verifyTableOrdered` → `valuesMatch`) and **fails** on a table without one
(`verifyTableUnordered` → `compareColumnUnordered` → `sortKeyFor` only, which
never consults `valuesMatch`). Trigger: a no-PK table with a SQLite NUMERIC
column that stores some rows as INTEGER and at least one as REAL — which
`fallbackTypeFor` maps to `double precision` — holding a value above 2^53.

**Why it's a cross-commit interaction:** yes, and it is the fourth iteration of
the exact saga the code comments document. `e6bc33e` made `valuesMatch` treat
cross-type numerics as equal via a float64 round-trip. `50b77d8` correctly made
the *same-type* path exact and gave `numericSortKey` an int64-range window —
but that window is tighter than `numericValue`'s (unbounded) conversion, so the
two functions no longer agree at the boundary. The two paths are now the only
two places verify decides equality, and they use different rules.

**Suggested direction:** Make `compareColumnUnordered` confirm a key mismatch
with `valuesMatch` before recording it (cheap — only on the disagreement path),
which structurally guarantees the two can never diverge again regardless of
future key-formatting changes.

---

## Finding 7 — `determineVerify` silently ignores a piped `y` answer (Severity: **Medium**)

**File:** `cmd/migrate/postload_verify.go:66-91`

**Commits involved:** `b78d905` (the prompt), `0264f29` (the CI-hang fix).

**Concrete failure scenario:** `echo y | migrate load --pg ... config.yaml`.
Stdin is a pipe, which is an `*os.File` and is not a terminal, so the new guard
returns `false` before reading a single byte. The user's explicit "yes" is
discarded and verification never runs — with no output saying so (the prompt
itself is also suppressed, so there is no "Run migrate verify now?" line to
hint that an answer was expected). A scripted pipeline that has been answering
this prompt would silently stop verifying.

The CI-hang fix is correct in intent (an unwritten pipe genuinely hangs), but
it cannot distinguish "pipe with an answer waiting" from "pipe that will never
be written."

**Why it's a cross-commit interaction:** `b78d905` established "prompt mode
reads an answer from `in`"; `0264f29` narrowed that to "prompt mode reads an
answer from `in` only if `in` is a terminal", without a message when the
narrowing kicks in.

**Suggested direction:** Do a non-blocking/short-deadline read on the pipe
before giving up, or at minimum print "stdin is not a terminal — skipping
verification (pass `--verify` to force it)" so the silent skip is visible.
Also consider making `--dry-run` + `--verify` an explicit usage error, since
`runLoad` currently parses `--verify` and then returns from the `--dry-run`
branch without ever using it.

---

## Finding 8 — `compareColumnUnordered` panics (rather than erroring) if the two sides' lengths differ (Severity: **Low**)

**File:** `internal/pipeline/verify_load.go:443-471`

**Commits involved:** `f8f1524`/`9de206a` (the unordered path).

**Concrete failure scenario:** `compareColumnUnordered` loops
`for i := range sortedExpected` and indexes `sortedActual[i]`. The lengths are
guaranteed equal only by the `COUNT(*)` comparison done earlier in
`VerifyTable`, in separate, non-snapshot-isolated statements on both sides.
Running `migrate verify` against a Postgres database an application is
concurrently writing to — a row DELETEd between the `COUNT(*)` and the
`SELECT` — makes `sortedActual` shorter and panics with index-out-of-range
instead of reporting an error. The ordered path already handles the exact
analogous case gracefully ("postgres table %s ran out of rows at row %d
despite matching row counts (concurrent write during verify?)"); the unordered
path does not.

**Suggested direction:** Guard the loop with `min(len(expected), len(actual))`
and return a distinct "row count changed during verify" error for the
remainder, mirroring `verifyTableOrdered`'s message.

---

## Finding 9 — FK index names are disambiguated against each other but not against table names, which share `pg_class` (Severity: **Low**)

**File:** `internal/ddl/foreign_key_indexes.go:59-73`,
`internal/ddl/identifiers.go:68-84`

**Commits involved:** `00aa829` (issue #43 — FK index names disambiguated
schema-wide), `1c1e4b3` (issue #44 — table names disambiguated schema-wide).

**Concrete failure scenario:** The two disambiguation passes run over disjoint
name sets. A source table literally named `idx_orders_customer_id` alongside an
`orders` table with an FK on `customer_id` produces a `CREATE TABLE
"idx_orders_customer_id"` and a `CREATE INDEX "idx_orders_customer_id"` — both
land in the same schema-scoped `pg_class` namespace, and the second fails with
"relation already exists". Both commits' doc comments correctly identify
`pg_class` as one shared namespace; neither pass actually considers the other's
names. Contrived input, but it is a real failure, not a hypothetical, and it is
exactly the hazard both issues were filed about.

**Suggested direction:** Feed `PostgresTableNames`'s output into
`GenerateForeignKeyIndexes`'s `disambiguateNames` call as additional
already-claimed display names, so index naming can see the relations that
already exist.

---

## Categories checked with nothing to report

- **DDL identifier truncation vs. FK index naming agreement** (the two paths
  the brief flagged as edited separately): they agree. Both go through
  `disambiguateNames(display, identity)` with the same truncate-then-sha1
  primitive, and both use the *raw source* table name in the display string
  consistently. FK constraint names are correctly disambiguated per-table
  (Postgres scopes `conname` per `conrelid`) while index names are correctly
  disambiguated schema-wide. Only the cross-namespace gap in Finding 9 remains.
- **`ddl.PostgresTableNames` reaching every call site:** verified for
  `executeLoad` (CREATE TABLE + COPY), `printDryRunDDL`,
  `GenerateForeignKeyConstraints` (both the ALTER TABLE target and the
  REFERENCES target), `GenerateForeignKeyIndexes` (the ON clause), and
  `verifyLoadedTables`. No site left on the raw source name.
- **Stale-state-file hazard from `executeLoad` no longer removing
  `.state.json`:** I traced this expecting a bug (a second non-`--resume`
  `migrate load` inheriting `FKsApplied: true` and silently skipping every FK
  constraint and index). It is safe: `connectForLoad` writes
  `loadState{Database: dbName}` wholesale on the non-resume path, clearing both
  `Completed` and `FKsApplied` before `executeLoad` reads it.
- **`TableSource` goroutine lifecycle** (`095387c`/`095f152`): correct.
  `defer close(ts.rowsCh)` runs strictly after `ts.errCh <- err`, so `Next`'s
  closed-channel/`errCh` handoff has no race; `doneOnce` correctly makes
  `Close` idempotent; the `select` on `ts.done` prevents the send-side leak.
- **Zero-length BLOB (#58) vs. verify:** these agree. `normalizeBlobValue`
  produces `[]byte{}`, pgx's `scanPlanBinaryBytesToBytes` returns a non-nil
  `make([]byte, 0)` for an empty bytea (nil only for SQL NULL), and
  `pgColumnScanner.value` distinguishes them on exactly that. `sortKeyFor`
  keys them apart too (`"\x00nil"` vs `"\x04bytes:"`). No cross-commit break.
- **Batched full-table verification (`311fb25`):** `remaining` is decremented
  exactly once per column (guarded by the `results` presence check), the
  `errAllColumnsResolved` sentinel is passed through `streamQuery` unwrapped so
  `errors.Is` works, and empty-transform specs are seeded before the scan
  without shadowing an active column. No vacuous tests found —
  `query_counter_test.go` genuinely counts queries.
- **`resolver.Decide` hundredths rework (`3b2f0ab`):** correct against the
  documented ladder. `gap <= 2` forces review at the intended 0.88→0.90
  disagreement gap and clears the intended 0.85→0.88 clean-win gap.
- **`ColumnCollations` parser:** its failure modes all err toward reporting a
  non-BINARY collation (e.g. a `COLLATE` inside a column-level `CHECK`), which
  degrades to the safe unordered path. I could not construct an input where it
  wrongly reports `BINARY` for a genuinely NOCASE/RTRIM column.
- **`ReadForeignKeys` graceful degradation (`03fa321`):** the `continue` paths
  cannot leave a half-built composite `ForeignKeyInfo`, since an implicit
  reference has a NULL `to` on every row of the constraint and
  `skippedRefTables` short-circuits them all. (Cosmetic only: a second FK id
  implicitly referencing the same unresolvable parent is dropped without its
  own `SkippedForeignKey` record.)
