# Audit phase 4 — cumulative diff review

Range reviewed: `76ae27e~1..aa1f44b` — **30 commits**, not 26 as the brief stated
(`git log --oneline 76ae27e~1..aa1f44b | wc -l` → 30, oldest `76ae27e`, newest
`aa1f44b`). Scope: `internal/` and `cmd/` only. `go build ./... && go test ./...`
is green at `aa1f44b`; every finding below is a defect the existing test suite
does not cover. Findings marked "verified" were reproduced with a throwaway test
against the current tree.

Ordered most severe first.

---

## HIGH

### 1. #34's `NOT NULL` DDL and #31's NULL check were never reconciled — a nulling transform on a source `NOT NULL` column aborts the load

**`internal/pipeline/decide_column.go:77`** (with `internal/ddl/generate.go:64-67`)

Issue #31 (`534c6d5`) taught `verifyTransformAgainstFullTable` to reject a value
a transform maps to `NULL`, but only for a primary-key column — the call site
passes `col.PrimaryKeySeq > 0`. Issue #34 (`14396e8`, committed *after* #31) then
started emitting a real `NOT NULL` constraint for any column with
`ColumnConfig.NotNull`. That widened the hazard #31 was guarding from
"primary-key columns" to "every source `NOT NULL` column", and the guard was not
widened with it.

Verified failure scenario: `CREATE TABLE albums (id INTEGER PRIMARY KEY,
mb_albumid TEXT NOT NULL)` where most rows hold canonical UUIDs and one holds
`''` (the real beets shape `uuid_format`'s empty-string→NULL rule exists for).
The sample misses the `''` row, `uuid_format` wins at 0.90, the full-table check
runs and passes (`isPrimaryKey=false`), the column auto-approves, and the
generated DDL is `"mb_albumid" uuid NOT NULL`. At COPY time the `''` row becomes
`NULL` and Postgres aborts the entire table with a not-null violation — the exact
outcome #31 was written to prevent. Same for `numeric_text_to_integer`,
`numeric_text_to_double`, and `nullif_empty` on a `NOT NULL` column.

**Fix direction:** pass `col.PrimaryKeySeq > 0 || col.NotNull` at
`decide_column.go:77` and rename `verifyTransformAgainstFullTable`'s
`isPrimaryKey` parameter to something like `rejectNull`, updating its doc comment
(which currently justifies the check purely in terms of the inline `PRIMARY KEY`
clause).

### 2. The type picker offers types that only work *with* a transform, but selecting one clears the transform

**`internal/tui/typepicker.go:96-101`** (with `internal/tui/logic.go:206-232`
and `internal/profiler/heuristics/uuid_list.go:22-40`)

Issue #18 (`c7b2a3c`) made `onTypeSelected` preserve `Transform` only when
`typeName == col.TargetType`, and clear it for any genuine type change. Issue #27
(`a8f9755`) then rewrote `previewValueForType` so `date`/`timestamptz` validate
and preview a value *by running it through `copywriter.Transform`* — and issue
#12 / `5bd60bf` added `uuid[]`, validated the same way. The picker now advertises
options that are only reachable via a transform, and then applies them with
`Transform: ""`.

Concrete failure scenario: `bikes.last_reported` is `integer` holding
`1712345678`. The picker offers `timestamptz` and shows `e.g. 2024-04-05T19:34:38Z`
(this is exactly what `TestValidTypesForColumn_OffersTimestamptzForAPlausibleUnixEpochValueNotAlreadyTimestamptz`
asserts). The human selects it; the config is written as
`target_type: timestamptz` with no transform; COPY sends a raw `int64` into a
`timestamptz` column and fails. Identically for `uuid[]`: `uuid_list.go`'s doc
comment explicitly promises "a human can now pick uuid[] from the type picker as
a real, working option", but without `uuid_list_format` attached the raw string
goes to a `uuid[]` column and fails.

**Fix direction:** have `previewValueForType`/`dateTransformPreview` return the
transform name they used, and have `onTypeSelected` set that transform on the
`DecisionRequest` instead of `""` (falling back to `""` only when the type
genuinely needs no transform).

### 3. #14's `iso8601_to_date` silently truncates time-of-day, and full-table verification cannot see it

**`internal/profiler/heuristics/iso8601.go:24-70`** (with
`internal/copywriter/transform.go:109-125`)

`allMidnight` is decided from the sample alone, and `iso8601_to_date` *discards*
the time-of-day rather than erroring on it. That is precisely the shape issue #22
(`f7ea712`) identified as making issue #13's full-table verification "a silent
no-op" — a transform that can never fail. #22 fixed `text_to_jsonb` and audited
the rest of the switch, but `iso8601_to_date` was added by #14 (`76ae27e`) and
the audit did not revisit it.

Verified failure scenario: a TEXT column with 500 sampled midnight-only values
(`'1996-01-02 00:00:00'`, …) and a handful of real timestamps
(`'1996-01-04 14:37:00'`) outside the sample. `decideColumn` returns
`target=date transform=iso8601_to_date confidence=0.9 needsReview=false` — no
`UnresolvedCase`. Every non-midnight row silently loses its time on load, with
no error anywhere. This is the same class of corruption #14 was written to fix,
inverted.

**Fix direction:** either make the `date` decision's full-table check reject a
non-midnight value (e.g. a `dateOnlyViolation` check in
`verifyTransformAgainstFullTable` when `targetType == "date"` and the transform
is `iso8601_to_date`), or have `iso8601_to_date` itself return an error for a
non-midnight input, matching the "verify by running the real transform" model.

---

## MEDIUM

### 4. #36 disambiguates FK *index* names per-table, but Postgres index names are schema-scoped

**`internal/ddl/foreign_key_indexes.go:20-56`**

`foreignKeyIndexNames(table, valid)` calls `disambiguateNames` over one table's
foreign keys at a time. That is the right scope for constraint names (unique per
`conrelid`), which is what `foreignKeyConstraintNames` needs — but an index is a
relation, unique per *schema*. Two different tables can therefore produce the
same generated index name.

Verified failure scenario: tables `aaaa…ax` and `aaaa…ay` (61 bytes each,
differing only in the last byte, so neither is itself truncated), each with a
one-column FK to `parents`. Both generate the identical 63-byte name
`idx_aaaaaaaa…a`, and the second `CREATE INDEX` fails with `relation
"idx_…" already exists`, aborting the load after every table has been copied.
`TestGenerateForeignKeyIndexes_TruncatesAndDisambiguatesLongIndexNames` only
exercises two FKs on the *same* table, so it passes.

**Fix direction:** build the index-name display/identity lists across all
included tables in `GenerateForeignKeyIndexes` and call `disambiguateNames` once
over the whole set, keying identity by `table + columns + refTable + refColumns`.
Leave `foreignKeyConstraintNames` per-table as it is.

### 5. `identifiers.go` never disambiguates *table* names, so #21's fix is incomplete for the same failure mode

**`internal/ddl/identifiers.go:32-38`** (with `internal/ddl/generate.go:78`,
`internal/copywriter/load.go:28`)

`PostgresColumnNames` truncates-and-hashes column identifiers; nothing does the
same for table names. `GenerateCreateTable` emits `quoteIdent(table)` verbatim.

Verified failure scenario: two source tables named `bbb…b1` and `bbb…b2`
(64 bytes, identical in their first 63) are emitted as two distinct
`CREATE TABLE "bbb…b1"` / `"bbb…b2"` statements; Postgres truncates both to the
same 63-byte relation name and the second fails with `relation … already
exists`. The identical hazard #21 fixed one level down.

This is also the coherence problem in `identifiers.go` generally: three commits
(#21 columns, #26 quoting, #36 FK/index names) each added a mechanism with a
different scope — per-table for columns and constraints, per-table (wrongly) for
indexes, and nothing at all for tables — without a single stated rule about
which namespace each identifier lives in.

**Fix direction:** add a `PostgresTableNames(cfg)` built on the same
`disambiguateNames` primitive, thread it through `GenerateCreateTable`,
`executeLoad`'s COPY target, FK `ALTER TABLE`/`REFERENCES`, and index `ON`
clauses; document in `identifiers.go` which namespace each helper covers.

### 6. `drop_column` always errors, so every `__drop__` column is force-flagged and blocks `migrate load`

**`internal/pipeline/decide_column.go:76-77`** with
**`internal/copywriter/transform.go:338-339`**

`esri_typename` assigns `TransformExpr: "drop_column"` at confidence 0.99 for a
`geometryblob` column. `decideColumn` runs the full-table check for any
auto-approving decision with a non-empty transform, and `Transform("drop_column", …)`
unconditionally returns an error. The check therefore streams the entire geometry
table and "finds a violation" on row 1.

Verified: `decideColumn` on a `geometryblob` column returns
`target="__drop__" confidence=0.4 needsReview=true unresolved=true`.
Consequences: (a) every Esri geometry column lands in the unresolved report as
noise; (b) `BuildReviewSummary` excludes `__drop__` columns
(`internal/review/review_model.go:88`) so the TUI can never clear it, while
`migrate load`'s gate (`cmd/migrate/main.go:293-297`) iterates *all* columns
including dropped ones — so a `migrate profile` → `migrate load` run on any Esri
source is refused with `SchoolSites2425.SHAPE is unreviewed (confidence 0.40 …)`;
(c) the whole blob table is read once per dropped column for nothing.

This predates the range (`76ae27e~1` has the same shape), but it sits directly in
the blast radius of #20's new `NeedsReview` gate and #30's all-dropped-table
handling, and neither commit noticed it.

**Fix direction:** skip the full-table check when
`best.SuggestedType == dropSentinel` (or when `best.TransformExpr == "drop_column"`)
in `decideColumn`, and consider scoping `migrate load`'s gate to
`ddl.IncludedColumns(tc)` so it matches what the review UI can actually act on.

### 7. #17's implicit-FK resolution reintroduces the hard abort #29 removed from the same file

**`internal/sqlitereader/schema.go:200-210, 236-249`**

Issue #29 (`d7abe5b`) split `ReadSchema`'s failure modes so an unsupported
virtual table module is *skipped and reported* while anything else aborts. Issue
#17 (`385dfc0`) touched the same file and added `primaryKeyColumn`, which is
called from `ReadForeignKeys` and returns a hard error when the referenced table
has 0 or >1 declared primary key columns. Two compositions are wrong:

- A legitimate implicit `REFERENCES parent` where `parent` is a rowid table with
  no declared `PRIMARY KEY` now fails the entire `migrate profile` run with
  `resolving implicit foreign key reference to parent: table parent has 0 primary
  key columns, expected exactly 1`, rather than degrading to "skip this one FK".
- If `parent` is a virtual table that `ReadSchema` *would* have skipped per #29,
  `primaryKeyColumn` → `readColumns` fails with `no such module:`, which
  propagates out of `ReadForeignKeys` and is returned as
  `reading foreign keys for <child>: …` at `schema.go:133` — a plain error, not a
  `SkippedTable`. #29's skip is defeated by a table that isn't even the one being
  read.

**Fix direction:** on a `primaryKeyColumn` failure, drop that single foreign key
(optionally recording it the way `SkippedTable` records a skipped table) instead
of failing `ReadForeignKeys`; and route the `no such module` case through
`isUnsupportedVirtualTableModuleError` there too.

### 8. #35 gates the *Spatialite* `st_*` filter on an *Esri* predicate

**`internal/sqlitereader/esri.go:33-35`** (with `internal/pipeline/profile.go:53-58`)

`FilterSystemTables(tables, esri)` only filters `st_*` when `esri` is true, and
`esri` comes from `IsEsriGeodatabase`, which tests solely for a `GDB_`-prefixed
table. A genuine Spatialite database that is not an Esri FGDB export has no
`GDB_*` tables, so `esri` is false and its `st_*` system tables are now migrated
as user data — the regression in the opposite direction from the one #35 fixed.
The parameter name (`esri`) and the convention it gates (Spatialite's) don't
match, which is what hides the gap.

**Fix direction:** add an `IsSpatialite(tables)` predicate (presence of
`spatial_ref_sys` / `geometry_columns` / `spatialite_history`) and gate `st_*` on
`isEsri || isSpatialite`, renaming the parameter accordingly.

### 9. The confidence ladder no longer satisfies the invariant `confidence.go` claims for it

**`internal/resolver/confidence.go:8-17`**

The doc comment added by #20 (`bc5fc3e`) states the margin "must stay smaller
than the smallest gap between adjacent rungs of the heuristic confidence ladder
(0.99/0.95/0.90/0.85/0.55)". Reading the *current* combined state:

- `aa1f44b` (issue #1, the last commit in the range) added a **0.88** rung
  (`heuristics.textConfidence`) that this enumeration does not mention. 0.88 sits
  0.03 from 0.85 — inside the margin — so `boolean01`'s text finding is now
  defined to "disagree" with every 0.85 heuristic. (No such pair can currently
  co-occur — `sentinel_null`/`unix_epoch*`/`excel_serial` are INT-only and
  `numeric_text`'s 0.85 variant needs a fraction — so this is latent, not live.)
- The 0.99→0.95 gap is **exactly** the margin, not larger than it. Whether
  `esri_typename` (0.99) and `comma_number`/`yyyymmdd_date` (0.95) resolve
  cleanly or force review is therefore decided by float representation error:
  `0.99-0.95` evaluates to `0.040000000000000036`, which lands just above
  `disagreementMargin-1e-9`, so it resolves cleanly *by luck*. Had the
  subtraction rounded down (as `0.95-0.90` → `0.049999999999999933` does) it
  would have flipped.

**Fix direction:** either drop the margin to 0.03 (strictly below every
intentional gap) or compare on integer hundredths (`int(math.Round(c*100))`) to
remove the float question entirely; update the enumerated ladder to include 0.88
and state the rule as "strictly less than the smallest intentional gap".

---

## LOW

### 10. #27's `FitsRange` was inserted inside #15's `parseWholeNumberText` doc comment

**`internal/copywriter/transform.go:346-371`**

The comment block opens with "parseWholeNumberText parses s as an exact int64 …"
and, three paragraphs in, continues "FitsRange reports whether n fits …" — with
`func FitsRange` declared immediately after it and `func parseWholeNumberText`
declared below at line 383 with no doc comment at all. Godoc attributes #15's
entire rationale to #27's function.

**Fix direction:** split the block, leaving the parse rationale attached to
`parseWholeNumberText` and moving it above that function.

### 11. `comma_number` still has no int4 range check, so #15's new verifier dumps large comma numbers into review

**`internal/profiler/heuristics/comma_number.go:53-72`**

#23 (`f7d8ef2`) added the fractional split (`strip_commas_float` →
`double precision`) but left the whole-number branch suggesting a bare `integer`,
unlike `numeric_text`, which computes `sawOutOfInt4Range` and promotes to
`bigint`. Now that #15's `fitsTargetType` range-checks the *produced* value, a
column of values like `"9,999,999,999"` is caught at 0.4 confidence and routed to
manual review instead of being sized as `bigint` automatically.

**Fix direction:** mirror `numeric_text`'s `sawOutOfInt4Range` check in
`CommaNumber.Evaluate` and suggest `bigint` when it fires.

### 12. `FilteredSystemTables` is warned but never persisted, unlike `SkippedTables`

**`internal/pipeline/profile.go:31-36`** vs **`internal/config/schema.go:20-27`**

#29 persists skipped tables into `MigrationConfig.SkippedTables` specifically so
"a human reviewing this config can see exactly what wasn't migrated and why".
#35's filtered system tables get only a stderr warning at profile time; anyone
reading the config later (or running `migrate load` on it in CI) has no record of
them.

**Fix direction:** add `FilteredSystemTables []string` to `MigrationConfig` and
populate it in `ProfileDatabase`, matching the `SkippedTables` precedent.

### 13. #38 keeps the config on failure but a successful `migrate run` orphans the state file

**`cmd/migrate/main.go:47-70`** with **`cmd/migrate/provision.go:106-121`**

`cleanupConfigAfterLoad` removes `<source>.migration.yaml` on success, but
`<source>.migration.yaml.state.json` — which since #19 carries the provisioned
database name plus every completed table — is left on disk forever, pointing at a
config that no longer exists.

**Fix direction:** remove the state file alongside the config in
`cleanupConfigAfterLoad`'s success branch (ignoring `os.IsNotExist`).

### 14. `runResolve` never clears the new `NeedsReview` field

**`cmd/migrate/main.go:517-531`**

`migrate resolve --apply` overwrites `TargetType`, `Transform`, `Rationale`,
`Confidence`, `Source` and sets `Reviewed = true`, but leaves `NeedsReview` (added
by #20) at whatever the profiler wrote. The saved YAML keeps `needs_review: true`
for a column that was explicitly resolved, and `BuildReviewSummary` keeps counting
it under `NeedsReviewCount`. The `migrate load` gate is unaffected because it also
requires `!col.Reviewed`.

**Fix direction:** decide one contract for the field and apply it in both
override paths — either clear it on override (and change `review.State.ApplyDecision`
to match) or document it as a permanently-stable profiler verdict (matching
`internal/tui/logic.go:296-300`) and leave `runResolve` as-is. Right now the two
paths agree only by accident.

### 15. `TableSource.Close()` panics on a second call

**`internal/copywriter/pipeline.go:87-89`**

`Close` is a bare `close(ts.done)`. `LoadTable` calls it via `defer`, and #28's
own test calls it explicitly. There is no current double-call path, but the
method's doc invites callers to "call Close so the goroutine doesn't leak" without
saying it is single-use.

**Fix direction:** guard with a `sync.Once` (mirroring `State.doneOnce` in
`internal/review/state.go`).

---

## Considered, not a bug

- **#25's subsumption by #16.** `4d26369` is test-only; no production code was
  added and none was left half-wired. `sentinel_null.AppliesTo` still doesn't
  recognize `NUMERIC`/`DECIMAL`, but the resulting behaviour (fall through to
  `default_passthrough`, get caught by `fallbackSampleMismatch`, land in review)
  is the documented and tested intent, not an oversight.
- **`config_version` vs `migrate profile`.** #33 makes `config.Load` reject a
  mismatched `config_version`; `ProfileDatabase` does set it
  (`internal/pipeline/profile.go:67`), so `migrate run`'s
  save → `review.NewState` → `config.Load` round-trip is not broken. Test
  fixtures across four files were updated to set it, which is correct, not a
  weakening.
- **Fixture edits in `iso8601_test.go`, `confidence_test.go`, `golden_test.go`.**
  `TestISO8601_DetectsSQLiteCanonicalDatetimeFormat`'s sample changed
  `00:00:00`→`09:15:00`, `…AlreadyParsedAsTimeTime` flipped its expectation to
  `date`, `TestDecide_FlagsCloseDisagreement…` moved 0.8→0.83, and
  `TestGolden_SampleDates`' `month_name_date` moved timestamptz→date. Each
  preserves the original test's *intent* and each is paired with a new explicit
  contrast test (`…WithRealTimeOfDayTargetTimestamptz`,
  `TestDecide_YYYYMMDDMarginOverNumericTextWinsCleanly`). This is not the
  "weakened to make a later fix pass" anti-pattern.
- **Dry-run determinism (#32).** `GenerateForeignKeyConstraints`
  (`foreign_keys.go:21-25`) and `GenerateForeignKeyIndexes`
  (`foreign_key_indexes.go:21-25`) both already sort table names, so
  `printDryRunDDL`'s sort completes the fix — FK and index statements are
  deterministic too.
- **`executeLoad`'s zero-column skip (#30).** The skip is applied while building
  `tableNames`, and both the row-count loop and the DDL/COPY loop iterate that
  same slice — no divergence between what the progress bar sizes and what is
  loaded.
- **#21's renaming across DDL and COPY.** `GenerateCreateTable` and `LoadTable`
  both derive identifiers from `ddl.PostgresColumnNames` over
  `ddl.IncludedColumns(tc)`, in the same order, so the CREATE TABLE column list
  and the COPY target list cannot diverge.
- **`TableSource` error ordering (#28).** The producer's `ts.errCh <- err` runs
  before the deferred `close(ts.rowsCh)`, so a `Next()` that observes the closed
  channel always finds the error already queued; the `default` branch on the
  second call preserves it rather than clobbering with nil. Both halves of #28's
  test are load-bearing.
- **`NeedsReview` surviving `ApplyDecision`.** Deliberate:
  `internal/tui/logic.go:296-300` documents the flag as a stable profiler verdict
  so the flagged-column jump list doesn't shift underfoot, and
  `internal/tui/grid.go:83` renders `Reviewed && NeedsReview` distinctly. (See
  finding 14 for the `runResolve` half of this contract.)
- **#39's SQLite-side quoting.** No `%q`-interpolated identifier remains in any
  production SQL string (`grep '%q' … | grep -E 'SELECT|PRAGMA|…'` finds only an
  error-message format in `foreign_keys.go:81`).
- **`disagreementMargin`'s epsilon direction.** An exact tie (gap 0) still forces
  review, confirmed numerically; the epsilon only shifts behaviour at the margin
  boundary itself (see finding 9).
