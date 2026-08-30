# sqlite2pg audit — phase 1 findings

Scope: all non-test sources under `internal/` and `cmd/migrate/`, read fresh
against `README.md` and the package doc comments. Audit only; no source file
was modified. Claims marked "verified" were reproduced by running the real
code paths (a throwaway `main` under `internal/`, since the packages are
`internal/`-scoped; it was deleted afterwards — `git status` is clean apart
from the pre-existing untracked plan file).

Ordered most-severe first.

---

## 1. The type picker silently deletes the column's transform, even when the human re-picks the same type

**File:line** — `internal/tui/typepicker.go:91-101` (`onTypeSelected`), with
`internal/review/state.go:98-128` (`ApplyDecision`)

**Failure scenario** — `bikes.last_reported` profiles as `timestamptz` via
`unix_epoch_seconds` (transform `unix_epoch_seconds`, confidence 0.85 →
flagged for review). The human presses `n` to jump to it, presses Enter to
open the picker, sees `timestamptz` pre-selected as the current type, and
presses Enter to accept it — the natural "yes, that's right" gesture.
`onTypeSelected` writes `Transform: ""` unconditionally. The config now says
`target_type: timestamptz, transform: ""`. At COPY time
`copywriter.Transform("", int64(1712345678))` returns the raw int64, and pgx
fails encoding an int64 into a `timestamptz` column — the whole load aborts
mid-table. The same happens for every transformed type the picker can land
on (`boolean` ← `int_to_bool`, `date` ← `yyyymmdd_to_date`/`julian_day_to_date`,
`integer` ← `strip_commas`, `uuid` ← `uuid_format`). Confirming the tool's own
correct answer is what breaks it.

**Severity** — high

**Fix direction** — when the newly selected type equals the column's current
`TargetType`, preserve the existing transform (or, more generally, map
(declared shape, target type) → the transform that reaches it) instead of
hard-coding `Transform: ""`. The "never carry a stale transform onto a *new*
type" rule is right; it just shouldn't fire when the type is unchanged.

---

## 2. `numeric_text` → `numeric_text_to_integer` silently corrupts large whole numbers

**File:line** — `internal/copywriter/transform.go:120-135`, driven by
`internal/profiler/heuristics/numeric_text.go:52-64,78-87`

**Failure scenario** — a TEXT column holding plain digit strings longer than
15 digits (account numbers, IMEIs, Discogs/MusicBrainz numeric IDs, ISBN-13
without hyphens). `plainNumberPattern` matches, `hasMeaningfulLeadingZero` is
false, `ParseFloat` succeeds, `f == math.Trunc(f)` holds, and `f > MaxInt32`
so the target becomes `bigint` at confidence 0.90 — auto-approved. Verified:

- `"9007199254740993"` → `9007199254740992` (float64 mantissa rounding)
- `"12345678901234567890"` → `9223372036854775807` (int64 saturation)

Neither returns an error, so `verifyTransformAgainstFullTable` reports the
column clean and the wrong values are written to Postgres with no warning
anywhere. This is silent data corruption, not a load failure.

**Severity** — high

**Fix direction** — parse whole-number text with `strconv.ParseInt` (falling
back to `ParseFloat` only for the fractional/double path), and have the
heuristic disqualify (or downgrade to `text`/`numeric`) any sample that
doesn't round-trip through int64.

---

## 3. `migrate load --resume` provisions a brand-new database, so resumed tables are simply missing

**File:line** — `cmd/migrate/main.go:262-269` (unconditional
`deriveDatabaseName` + `provisionDatabase`) vs `cmd/migrate/main.go:305-315`
(`resume && completed[tableName]` → skip)

**Failure scenario** — `migrate load --pg ... cfg.yaml` creates database
`chinook_20260830_120000`, loads `albums` and `artists`, then fails on
`tracks` (a COPY error). The state file records albums/artists. The user
re-runs `migrate load --pg ... --resume cfg.yaml`. `deriveDatabaseName` uses
`time.Now()` again, so `provisionDatabase` creates a *different, empty*
database `chinook_20260830_120530`, prints "skipping (already completed)" for
`albums` and `artists`, and creates/loads only `tracks`. The resulting
database contains one table instead of three. Then
`GenerateForeignKeyConstraints` emits
`ALTER TABLE "tracks" ... REFERENCES "albums"` and `conn.Exec` fails with
`relation "albums" does not exist` — or, for a schema with no FKs, the run
exits 0 with a silently incomplete database.

**Severity** — high

**Fix direction** — record the provisioned database name in the state file
and reconnect to it on `--resume` (rather than provisioning), or refuse
`--resume` unless the target database is named explicitly.

---

## 4. The disagreement margin swallows the entire confidence ladder — 0.95 can never beat 0.90

**File:line** — `internal/resolver/confidence.go:11,36`
(`disagreementMargin = 0.1`; `best.Confidence-secondBest < disagreementMargin`)

**Failure scenario** — verified by running the real registry + `Decide`:

```
last_validation_date (TEXT), samples "20211015","20200101"
  findings = [numeric_text=integer 0.90] [yyyymmdd_date=date 0.95]
  => date (yyyymmdd_date) needsReview=TRUE
```

`internal/profiler/heuristics/yyyymmdd_date.go:59-67` states in a comment
that 0.95 was chosen specifically so this heuristic "must win outright rather
than tie and force review". It does not: `0.95 - 0.90 == 0.049999999999999933`,
well inside the 0.1 margin, so every date-named TEXT column holding YYYYMMDD
values is flagged for human review and `migrate profile` exits non-zero on it.

The problem is structural, not one heuristic's: the whole ladder is
0.99/0.95/0.90/0.85, every adjacent gap is ≤ 0.1, and the two gaps that are
*exactly* 0.1 also land inside the margin because of float representation
(`0.95-0.85 == 0.09999999999999998`, `0.90-0.80 == 0.09999999999999998`).
Consequence: *any* column on which two heuristics both fire is escalated,
regardless of how deliberately their confidences were spread apart.

**Severity** — high

**Fix direction** — compare with an epsilon (`best-second <= margin-1e-9`) and
re-space the ladder (or make the margin smaller than the smallest intended
gap) so a deliberate one-step lead actually wins; consider exempting pairs
where one heuristic is a strict specialization of the other.

---

## 5. "Heuristics disagreed" is never persisted, so `load` and the TUI both ignore it

**File:line** — `internal/pipeline/decide_column.go:51-77` (stores
`best.Confidence` unchanged when `needsReview` came from the tie check),
`internal/review/review_model.go:92` (`needsReview := col.Confidence < threshold`),
`cmd/migrate/main.go:231` (`!col.Reviewed && col.Confidence < *threshold`)

**Failure scenario** — the column in finding #4: `decideColumn` returns an
`UnresolvedCase` (so `migrate profile` writes it to `unresolved_report.yaml`
and exits non-zero), but the persisted `ColumnConfig` has
`confidence: 0.95, reviewed: false`. A later `migrate load cfg.yaml` (no
`--force`) evaluates `0.95 < 0.9` → false and loads it without complaint. The
TUI likewise computes `NeedsReview` from confidence alone, so the column shows
no `!` marker and `n`/`N` never jumps to it — the human is never shown the
ambiguity the profiler explicitly detected. Only the below-threshold and
full-table-violation paths (which *do* rewrite confidence, to 0.4) survive
into the config; the tie path evaporates.

**Severity** — high

**Fix direction** — persist the review requirement explicitly (a
`needs_review: true` field on `ColumnConfig`, written by `decideColumn`) and
have `BuildReviewSummary` and `runLoad`'s gate consult it in addition to
confidence — same treatment the full-table-violation path gets today via its
confidence rewrite.

---

## 6. Full-table verification is a no-op for every transform that can't fail

**File:line** — `internal/pipeline/verify_transform.go:40-46`, against
`internal/copywriter/transform.go:41-42,99-100,204-221`

**Failure scenario** — a TEXT column where all 500 sampled rows are GeoJSON
documents but one row out of 200k holds `"N/A"`. `geojson_text` fires
(confidence 0.90, target `jsonb`, transform `text_to_jsonb`) and
`decideColumn` runs the full-table check — but `text_to_jsonb` is
`return raw, nil`, so it cannot report anything, the check passes, and the
column auto-approves. COPY then fails at that row with
`invalid input syntax for type json`. The same hole applies to
`esri_typename`, `nullif_empty`, `""`, and the "raw isn't a string, pass it
through" branches of `strip_commas`, `numeric_text_to_*`,
`iso8601_to_timestamptz` and `dayfirst_to_timestamptz`. The documented
guarantee ("verified against the whole table before being auto-approved") is
silently absent for exactly the transforms that do no validation.

**Severity** — medium

**Fix direction** — make the transforms validate what they claim (parse the
JSON in `text_to_jsonb`, reject an unexpected dynamic type instead of passing
it through), or give the verifier a per-target-type validity check so a
no-op transform still gets its values checked against the target type.

---

## 7. `comma_formatted_number` accepts decimals but targets `integer`/`strip_commas`

**File:line** — `internal/profiler/heuristics/comma_number.go:10,42-47` vs
`internal/copywriter/transform.go:44-53`

**Failure scenario** — `commaNumberPattern` is
`^-?\d{1,3}(,\d{3})+(\.\d+)?$`, so `"1,234.56"` matches and the heuristic
suggests `integer` + `strip_commas` at 0.95. Verified:
`Transform("strip_commas", "1,234.56")` → `strip_commas: "1,234.56":
strconv.ParseInt: parsing "1234.56": invalid syntax`. Best case, the
full-table check catches it and drops the column to confidence 0.4 for
review — but there is then no way to load it: the picker's only fix is to
select `double precision`, which (per finding #1) clears the transform and
leaves the raw `"1,234.56"` string headed for a float8 column. Worst case,
if the sample happened to hold only integral values, the column auto-approves
and COPY dies on the first decimal row. `strip_commas` also has no int64
overflow guard (`"9,223,372,036,854,775,808"` errors mid-COPY).

**Severity** — medium

**Fix direction** — split the finding: suggest `double precision` +
a `strip_commas_float` transform when any sample carries a fractional part,
`integer` only when none does (mirroring what `numeric_text` already does
with `sawFraction`).

---

## 8. `julian_day_to_date` truncates instead of rounding, giving the wrong day for half of all JD values

**File:line** — `internal/copywriter/transform.go:102-107` (`int64(f)`) and
`internal/copywriter/transform.go:293-304`

**Failure scenario** — an Esri `realdate` value of `2453975.25` is
2006-08-26 18:00 UT. `int64(2453975.25)` = 2453975, and the Fliegel/Van
Flandern conversion on that integer yields 2006-08-27 — one day late.
Verified: the code returns 2006-08-27 for input 2453975.5 (correct, that *is*
2006-08-27 00:00), which means it returns the same answer for every fraction
in `[0.0, 1.0)`, including the `[0.0, 0.5)` half that belongs to the previous
calendar day. Any geodatabase whose `realdate` columns carry an afternoon
time-of-day (they are timestamps, not just dates — the fixture's `.5` values
merely happen to be midnight) migrates every such row off by one day, with no
error.

**Severity** — medium

**Fix direction** — use `math.Floor(f + 0.5)` (the standard JD → JDN
conversion) rather than `int64(f)`; if time-of-day matters, keep the fraction
and target `timestamptz` instead of `date`.

---

## 9. Mixed-storage columns get a numeric fallback type that can't hold their text rows

**File:line** — `internal/pipeline/profile.go:231-255` (`sawFloat` and
`sawInt` cases precede `sawString`), plus
`internal/profiler/heuristics/sentinel_null.go:29-33`

**Failure scenario** — a `NUMERIC(10,2)`-declared column (chinook's own
`invoice_items.UnitPrice` shape) with one catch-all row holding `'Unknown'`.
`SentinelNull.AppliesTo` tests only for `INT`/`REAL`/`FLOA`/`DOUB` in the
declared type — `NUMERIC` and `DECIMAL` match none of those, so the sentinel
heuristic never runs, and no other heuristic fires. `decideColumn` falls
through to `fallbackTypeFor`, which sees `sawFloat` (from the numeric rows)
*and* `sawString` (from `'Unknown'`) and returns `double precision` on the
first case. Confidence is stamped 0.99, `Transform` is empty so the
full-table verifier returns immediately, and COPY fails on the `'Unknown'`
row with no prior warning. The identical shape occurs with `sawInt` +
`sawString` on an untyped (BLOB-affinity) column.

**Severity** — medium

**Fix direction** — add `NUMERIC`/`DECIMAL`/`NUM` to `SentinelNull.AppliesTo`,
and make `fallbackTypeFor` return `text` whenever `sawString` is combined
with a numeric/time storage class, rather than letting the numeric case win.

---

## 10. DDL identifier quoting uses Go's `%q`, which is not SQL identifier quoting

**File:line** — `internal/ddl/generate.go:29,32,36,70-76`,
`internal/ddl/foreign_keys.go:84-85`,
`internal/ddl/foreign_key_indexes.go:47-48` — vs
`internal/copywriter/load.go:22` (`pgx.Identifier{dbTable}`, which *does*
double embedded quotes)

**Failure scenario** — a source table or column named `Total "Disability"
Recipients` (this project's own fixture already has a column named
`Total: Disability Compensation Recipients`, so punctuation-laden headers
are the norm here). `fmt.Sprintf("%q", name)` emits
`"Total \"Disability\" Recipients"`; Postgres terminates the identifier at
the first inner `"` and rejects the statement with a syntax error, so
`CREATE TABLE` fails. Meanwhile `pgx.Identifier.Sanitize` in `LoadTable`
would have produced the correct `"Total ""Disability"" Recipients"` — so DDL
and COPY don't even agree on what identifier they're naming. A backslash in
an identifier produces a *silently different* name rather than an error.

**Severity** — medium

**Fix direction** — replace every `%q` on an identifier with
`pgx.Identifier{name}.Sanitize()` (or a local equivalent that doubles inner
quotes), so the DDL and COPY paths share one quoting rule.

---

## 11. The picker's validity filter ignores transforms, hiding the correct type and offering out-of-range ones

**File:line** — `internal/tui/logic.go:75-115` (`previewValueForType`),
`internal/tui/logic.go:217-231` (`validTypesForColumn`)

**Failure scenario** — the review grid deliberately shows raw source values.
For `bikes.last_reported` (raw `1712345678`), `validTypesForColumn` tests
`timestamptz` by running the raw text through `dateLayouts`, which fails, so
`timestamptz` appears in the picker only because it happens to be the current
type; for a column the human wants to *change* to `timestamptz`, the option
is absent entirely. The reverse error is also present: `previewValueForType`
accepts any `ParseFloat`-able value for `integer` and `smallint`, so the
picker happily offers `smallint` for a column whose visible sample is
`70000`, and the resulting COPY fails with `value out of range`.

**Severity** — medium

**Fix direction** — validate each candidate type against the value *after*
the transform that type would use (share `copywriter.Transform` rather than
re-deriving validity from the raw string), and range-check int2/int4/int8
targets.

---

## 12. `TableSource`'s producer goroutine blocks forever when pgx stops pulling, and `Next()` can clobber the recorded error

**File:line** — `internal/copywriter/pipeline.go:34-56` (`ts.rowsCh <- transformed`
with no cancellation), `internal/copywriter/pipeline.go:69-78` (non-blocking
`select` on `errCh` with a `default`)

**Failure scenario** — COPY fails server-side partway through a large table
(e.g. `value out of range for type integer` on row 900k). pgx abandons its
`Next()` loop and returns the error. The producer goroutine is parked on
`ts.rowsCh <- transformed` with a full 100-slot buffer and never wakes, so
`StreamTable`'s `defer rows.Close()` never runs: the SQLite cursor and the
goroutine leak for the process's lifetime. In the CLI the process exits
immediately afterwards, so the damage is bounded — but `executeLoad` is a
library-shaped function and any caller that retries or continues accumulates
leaked scans and connections. Separately, `Next()` consumes `errCh` on the
first `false` and, on any subsequent call, takes the `default` branch and
sets `ts.err = nil`, discarding the error it had already recorded.

**Severity** — medium

**Fix direction** — give `TableSource` a `context`/`done` channel selected on
alongside the send (and closed by `LoadTable` on return), and make `Next()`
only overwrite `ts.err` when it actually receives from `errCh`.

---

## 13. `ReadSchema` silently drops any table whose column read fails

**File:line** — `internal/sqlitereader/schema.go:67-74`

**Failure scenario** — `readColumns` returns an error for *any* reason —
the intended case is an unimplemented virtual-table module, but a locked
database, a `database disk image is malformed` on one page, or a permissions
error produce the same error — and the table is dropped from `tables` with
`continue`, no message, no counter. The migration then completes with exit
status 0 while the target database is missing a user table entirely. The
user's only clue is a table that isn't there.

**Severity** — medium

**Fix direction** — distinguish the "unimplemented virtual table module"
error (match on it) from everything else, warn on stderr for every skip, and
carry the skipped list into the profile output so it's visible.

---

## 14. `GenerateCreateTable` emits syntactically invalid SQL for a table with no included columns

**File:line** — `internal/ddl/generate.go:22-40`, called from
`cmd/migrate/main.go:329`

**Failure scenario** — an Esri table whose only column is a `geometryblob`
(mapped to `__drop__`), or any config whose `column_order` key is absent
(it's `omitempty`, so a hand-written or hand-trimmed config loses it and
`IncludedColumns` returns nil). `cols` is empty and there's no primary key,
so the emitted statement is `CREATE TABLE "t" (\n\n);` — Postgres rejects it
with a syntax error and `executeLoad` aborts the entire run at that table.
`--dry-run` prints the same broken statement.

**Severity** — medium

**Fix direction** — skip a table with zero included columns (and report it,
the way skipped foreign keys are reported), and have `Load` reject a config
whose `TableConfig` has columns but an empty `ColumnOrder`.

---

## 15. Transforms that map the empty string to NULL break a primary-key column

**File:line** — `internal/copywriter/transform.go:125-130,142-144,178-192,217-221`,
with `internal/ddl/generate.go:31-33` (inline `PRIMARY KEY`)

**Failure scenario** — the MIC-registry-style `station_id` TEXT column
carries canonical UUIDs and is the table's primary key. `uuid_format` fires
(0.90) and the DDL emits `PRIMARY KEY ("station_id")`. A single row with an
empty string — exactly the case the transform's own comment says it must
tolerate — becomes `nil`, and Postgres rejects it with
`null value in column "station_id" violates not-null constraint`, aborting
the load. The same collision exists for `numeric_text_to_integer`,
`numeric_text_to_double` and `nullif_empty` on any PK column.

**Severity** — medium

**Fix direction** — when a column has `PrimaryKeySeq > 0`, don't let a
nulling transform be chosen for it (or verify with a full-table check that no
value maps to NULL before auto-approving).

---

## 16. Int4/int8 sizing is decided from the sample and the full-table check can't catch an overflow

**File:line** — `internal/profiler/heuristics/numeric_text.go:62-63,78-81`,
`internal/pipeline/profile.go:213-244`, against
`internal/copywriter/transform.go:120-135`

**Failure scenario** — a TEXT column whose 500-row sample holds only values
under 2^31 but whose full table contains `"4000000000"`. `sawOutOfInt4Range`
stays false, the target is `integer`, and the column auto-approves at 0.90.
`verifyTransformAgainstFullTable` then runs `numeric_text_to_integer` over
every row — which returns `int64(4000000000)` with no error, because the
transform has no idea the target is int4 — so the check passes. COPY fails
with `value out of range for type integer`. Same for `fallbackTypeFor`'s
`sawInt`/`sawOutOfInt4Range` split, where there's no transform at all and the
verifier returns immediately at `verify_transform.go:36-38`.

**Severity** — medium

**Fix direction** — have the verifier range-check the produced value against
the *target type*, not just the transform's error, so an int4 target is
proven against every row before auto-approving.

---

## 17. `--dry-run` prints CREATE TABLE statements in random order

**File:line** — `cmd/migrate/main.go:238-244` (ranges `cfg.Tables` directly)

**Failure scenario** — `migrate load --dry-run cfg.yaml > ddl.sql` twice on
the same config produces two files whose table blocks are in different
orders, because Go randomizes map iteration. Diffing two dry runs (the
obvious way to see what a re-profile changed) shows spurious churn.
`executeLoad` at line 304-315 already sorts for exactly this reason; the
dry-run path was missed.

**Severity** — low

**Fix direction** — sort the table names in the dry-run branch, the same way
`executeLoad` does.

---

## 18. `Load` never checks `ConfigVersion`

**File:line** — `internal/config/load.go:11-21`; the promise is in
`internal/config/schema.go:8-10` ("checked by Load to detect configs from an
older schema")

**Failure scenario** — a config written by a future version with
`config_version: 2` (or an unversioned hand-written one, `config_version: 0`)
is loaded by `migrate load` without complaint; any field that changed meaning
is silently misinterpreted. `CurrentConfigVersion` is referenced in exactly
one place (`pipeline/profile.go:53`, on write) and compared nowhere.

**Severity** — low

**Fix direction** — have `Load` reject `cfg.ConfigVersion != CurrentConfigVersion`
with a "re-run `migrate profile`" message.

---

## 19. `NOT NULL` is read from the source and then thrown away

**File:line** — `internal/sqlitereader/schema.go:15,107`
(`ColumnInfo.NotNull`) — never referenced again anywhere in the tree

**Failure scenario** — a SQLite table declaring `email TEXT NOT NULL`
migrates to a Postgres column with no `NOT NULL`, so the constraint is
silently lost. `PrimaryKeySeq`, the neighbouring field, *is* carried through
to `config.ColumnConfig` and into the DDL; `NotNull` is populated and then
dropped on the floor, which reads as an unfinished feature rather than a
decision.

**Severity** — low

**Fix direction** — either carry it into `ColumnConfig` and emit `NOT NULL`
in `GenerateCreateTable`, or delete the field and note the omission in the
README's limitations.

---

## 20. `FilterSystemTables` drops any user table whose name starts with `st_`

**File:line** — `internal/sqlitereader/esri.go:19-27`

**Failure scenario** — a plain (non-Spatialite) SQLite database with a table
named `st_locations` or `st_2024_results` has that table silently excluded
from the migration — no warning, exit 0. The Spatialite check isn't gated on
`IsEsriGeodatabase` or on any Spatialite marker, so it applies to every
source database.

**Severity** — low

**Fix direction** — only apply the `st_` filter when the database is actually
Spatialite/Esri (the `IsEsriGeodatabase` result is already computed in
`ProfileDatabase` right before the call), and log every filtered table.

---

## 21. Generated constraint and index names aren't length-checked

**File:line** — `internal/ddl/foreign_keys.go:81`,
`internal/ddl/foreign_key_indexes.go:47`

**Failure scenario** — `fk_%s_%s` / `idx_%s_%s` over a long table name plus a
composite key's joined column names easily exceeds Postgres's 63-byte
NAMEDATALEN. Postgres truncates silently, so two composite FKs on the same
long-named table can collapse to the same constraint name and the second
`ALTER TABLE` fails with `constraint ... already exists`. Two FKs on the same
column set (different referenced tables) collide even without truncation.
`cmd/migrate/provision.go:16` already handles the 63-byte limit for database
names; DDL names don't.

**Severity** — low

**Fix direction** — truncate-with-hash (or append a disambiguating index)
when the generated name exceeds 63 bytes, reusing the `maxDatabaseNameLen`
constant's reasoning.

---

## 22. `formatSampleValue` truncates by bytes, splitting multi-byte characters

**File:line** — `internal/review/samples.go:67-80` (`s[:maxLen]`)

**Failure scenario** — a text column holding `"Museo Nacional Centro de Arte
Reina Sofía — Colección"`; the 40-byte cut can land inside the `í` or the em
dash, and the grid cell renders a replacement character. Cosmetic, but it's
the data the human is judging the type against.

**Severity** — low

**Fix direction** — truncate on a rune boundary (count runes, or back up to
the last `utf8.RuneStart`).

---

## 23. `migrate run` deletes the config even when the load fails, making `--resume` impossible

**File:line** — `cmd/migrate/main.go:99-101` (`defer os.Remove(configPath)`
registered before the review and load steps)

**Failure scenario** — `migrate run source.db --pg ...`, review finishes, the
load fails on the third table. The deferred `os.Remove` runs on the way out
and deletes `source.db.migration.yaml`, leaving only an orphan
`source.db.migration.yaml.state.json` that now refers to a config that no
longer exists. The README advertises `--keep-config` as "useful ... for a
later `--resume`", but a user who hits a failure without having passed it up
front cannot resume or even inspect what was decided.

**Severity** — low

**Fix direction** — only remove the config on a successful load (move the
removal after `executeLoad` returns nil), keeping it on any error path.

---

## Considered, not a bug

- **`TableSource`'s `errCh` send vs `close(rowsCh)` ordering** — looks racy,
  but `ts.errCh <- err` is the last statement and `close` is deferred, so the
  error is always in the (buffered, cap-1) channel before the consumer sees
  the closed `rowsCh`. Only the repeat-`Next()` case (finding #12) is wrong.
- **`Decide`'s tie-breaking non-determinism** — `f.Confidence > best.Confidence`
  is strict, so the first-registered heuristic wins an exact tie; registration
  order is `init()` order within one package, which Go fixes by filename, so
  it's deterministic. And an exact tie forces review anyway.
- **`threshold` comparison being `<` rather than `<=`** — heuristics at
  exactly 0.90 auto-approving under a 0.90 threshold matches the README's
  "below the confidence threshold" wording; intentional.
- **`excelSerialToTime`'s epoch and range constants** — 1899-12-30 is correct
  (Lotus leap-year bug), and 36526/49310 really are 2000-01-01/2035-01-01 in
  that system. Verified: serial 36526 → 2000-01-01.
- **`ISO8601.AppliesTo` matching Esri `realdate`** (it contains the substring
  `DATE`) — harmless: `realdate` samples arrive as float64, so `total` stays 0
  and the heuristic never fires, leaving `esri_julian_day` uncontested.
- **`text_to_jsonb` handing pgx a plain Go string** — pgx v5's JSON/JSONB
  codec accepts `string`, so this encodes fine; the problem there is the
  missing validation (finding #6), not the type.
- **`Boolean01`'s `!(sawZero || sawOne)`** — redundant with `!sawNonNull`
  given the `default: return false` above it, but not incorrect.
- **`Boolean01`'s `_?id$` name exclusion and `UUIDFormat`/`NumericText`'s
  all-or-nothing sample rules** — deliberate, documented, and consistent with
  the referenced issues.
- **DDL/COPY column-order agreement** — both go through
  `ddl.IncludedColumns(tc)` and both skip a `ColumnOrder` entry missing from
  the `Columns` map, so the orders can't diverge (given a `ColumnOrder`; see
  finding #14 for the empty case).
- **`splitKey`'s `LastIndex`** — correct for a table name containing a dot.
- **`provisionDatabase` overriding the URL's database with `postgres`** —
  documented behavior of the `--pg` flag ("no database name").
