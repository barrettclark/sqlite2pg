# Phase 2A Results — Checked-in Fixtures (`testdata/fixtures/`)

All 15 fixtures run: `profile --sample-size 500` → full-config review → mark
reviewed → `load` against local Postgres → spot-check schema/data against
source → `dropdb`. All scratch Postgres databases and `/tmp/audit-p2a` files
were cleaned up after the run.

**Totals: 15/15 databases run, 0 crashes/hangs, 5 findings** (1 crash-on-load,
1 silent data-corruption bug, 3 issue-#3 evidence entries for auto-increment
surrogate keys / geometry blobs, 1 issue-#1-adjacent observation).

---

## 1. atomic_database.db

- Columns: 40 total across 12 tables, all clean (0.99 confidence, no review needed).
- Wall-clock: profile ~0.15s, load ~1.1s (failed partway — see finding).
- Findings:
  - **Bug (load crash / wrong type, high confidence)**: `XRAY_ENERGIES.Inner`
    and `XRAY_ENERGIES.Outer` are declared `INT` and auto-approved to
    `integer` at 0.99 confidence via `default_passthrough`, but the actual
    column data is mixed — `typeof()` shows 4717 integer rows and 8603 text
    rows (subshell codes like `"K"`, `"L1"`, `"M3"`, etc.), a legal SQLite
    dynamic-typing case. Load failed outright: `ERROR: COPY from stdin
    failed: unable to encode "K" into binary format for int4`. The
    `default_passthrough` heuristic trusts the *declared* SQLite type
    affinity without checking sampled data values, so a column that should
    have been flagged (or typed `text`) was silently auto-approved instead.
    This is a distinct new bug — not just a review-gate miss, since it
    doesn't merely "need review," it produces a load-time crash on data that
    was available in the profiling sample.
  - **Issue #3 evidence**: `LIT_REFERENCES.PKEY` is `INTEGER PRIMARY KEY
    AUTOINCREMENT` in SQLite but maps to plain `integer` in Postgres (not
    `serial`/`bigserial`).

## 2. AustinRoadConstruction.db

- Columns: 26 total, 20 clean, 6 needs-review (all `boolean01` flags at 0.55
  confidence, correctly gated).
- Wall-clock: profile ~0.15s, load ~0.2s.
- Spot-checked: UUID `id`/`data_source_id`, `geometry` (GeoJSON→jsonb),
  `start_date` (ISO8601→timestamptz with correct UTC/local offset), booleans
  — all matched source exactly.
- Findings: no findings. Review gate worked as designed on the six `boolean01`
  columns.

## 3. bikes.db

- Columns: 14 total, 9 clean/auto-resolved, 7 needs-review (`boolean01` ×6,
  the 7th being `num_scooters_available`/`unavailable`, see note below).
- Wall-clock: profile ~0.1s, load ~0.5s.
- Findings:
  - **Bug (silent data corruption, new — not #1/#3/#12)**: `legacy_id`
    (`numeric_text` heuristic → `bigint` via `numeric_text_to_integer`) loses
    precision for large integers. Source values like `2124037125711300644`
    and `1795146692060860976` loaded as `2124037125711300608` and
    `1795146692060860928` respectively — wrong by dozens of units, silently,
    with a "success" load. Root cause: `internal/copywriter/transform.go`
    case `"numeric_text_to_integer"` parses the string with
    `strconv.ParseFloat(s, 64)` then casts to `int64`, so any value beyond
    float64's ~15-17 significant-digit precision (this fixture's IDs are
    19-digit) gets silently rounded to the nearest representable float64
    before conversion. Should parse directly with `strconv.ParseInt`. Same
    code path also backs `numeric_text_to_double`, which doesn't have this
    specific bug (double precision is the target) but shares the float64
    parse.
  - **Observation (issue #1-adjacent, not a strict finding)**:
    `num_scooters_available`/`num_scooters_unavailable` are named identically
    to sibling count columns (`num_bikes_available`, `num_docks_available`,
    etc.) that got auto-approved as `integer`, but because every row in this
    snapshot happens to be 0 (or NULL), they instead tripped `boolean01` and
    got flagged for review at 0.55 confidence. Correctly gated — not a
    finding — but a real example of how column-name-pattern context (matching
    a sibling column family) could disambiguate what raw sampled values
    cannot.

## 4. chinook.db

- Columns: 63 total across 11 tables, 62 clean, 1 needs-review
  (`invoice_items.Quantity`, `boolean01` at 0.55 — correctly gated; source
  data is in fact always `1` in this fixture).
- Wall-clock: profile ~0.04s, load ~4.5s.
- Spot-checked: `invoices` row data and ISO8601 timestamp conversion — exact
  match (apparent `¤` glyphs in psql output for `BillingState` were
  confirmed to be a terminal rendering of `NULL`, not a data issue).
- Findings:
  - **Issue #3 evidence**: `albums.AlbumId` (and the same shape recurs across
    every table's PK in this DB) is `INTEGER PRIMARY KEY AUTOINCREMENT` in
    SQLite but maps to plain `integer`, not `serial`/`bigserial`.

## 5. DisabilityCompByCounty.db

- Columns: 14 total, 13 clean, 1 needs-review (`FIPS code`, `sentinel_null`
  at 0.85 — correctly gated; source has the literal sentinel `"Unknown"`
  mixed with numeric FIPS codes).
- Wall-clock: profile ~0.1s, load ~1.5s.
- Spot-checked: comma-formatted numbers (`"1,698"` → `1698`) and the
  `Unknown`-sentinel row → `NULL` — both correct.
- Findings: no findings.

## 6. sakila.db

- Columns: 16 tables, 16 needs-review columns flagged (varchar-length
  preservation at 0.5, `active`/`boolean01` at 0.55, etc.).
- Wall-clock: profile ~0.18s, load ~0.6s.
- Findings:
  - **Bug matching issue #1's exact shape (high confidence, auto-approved
    wrong)**: `customer.active` is declared `CHAR(1)` and stores `'0'`/`'1'`
    as text — a semantic boolean flag ("is this customer active"). Because
    it's TEXT-affinity rather than INTEGER-affinity, the `boolean01`
    heuristic never got a chance to evaluate it; instead the `numeric_text`
    heuristic claimed it ("every sampled value is a plain whole number
    stored as text") and mapped it to plain `integer` at confidence 0.9 —
    just over the 0.9 auto-approve threshold, so it was **never surfaced for
    human review**. Contrast with `staff.active` in the same database, which
    is declared `SMALLINT` and correctly got flagged via `boolean01` at 0.55.
    This is the concrete "0/1 column silently auto-approved wrong" case the
    plan calls out for issue #1, and it shows the gap is specifically that
    `numeric_text` and `boolean01` don't cross-check each other when a
    0/1-only column happens to have TEXT declared affinity instead of
    INTEGER.
  - **Issue #3 evidence**: multiple `AUTOINCREMENT`/serial-shaped PKs (e.g.
    `actor.actor_id`, `film.film_id`) map to plain `integer`.

## 7. northwind_small.sqlite

- Columns: 13 tables, 1 needs-review column (`Product.Discontinued`,
  `boolean01` at 0.55 — correctly gated).
- Wall-clock: profile ~0.1s, load ~0.23s.
- Spot-checked: `Order.OrderDate` (date-only → timestamptz midnight-UTC
  conversion), `ShipVia`/`Freight`, `Employee.Photo` (BLOB, correctly NULL in
  both source and target) — all matched.
- Findings: no findings. (`Employee.Extension`, a `VARCHAR` column of
  all-digit phone extensions with no leading zeros, was auto-approved to
  `integer` via `numeric_text` at 0.9 — verified correct since no value has a
  leading zero that would be lost.)

## 8. sample-dates.sqlite

- Columns: 9 total, 4 needs-review (the two epoch-scale heuristics and
  Excel-serial-date, all at 0.85 confidence — correctly gated below the 0.9
  threshold).
- Wall-clock: profile ~0.02s, load ~0.2s.
- Spot-checked exhaustively: every row's `creation_date` (YYYYMMDD),
  `last_validation_date`, `logged_at` (M/D/YYYY h:mm:ss AM/PM — labeled
  `iso8601_timestamp` by the heuristic despite not being ISO8601 syntax, but
  parses to the correct instant), `month_name_date` ("Sep 27, 2021"),
  `day_first_date` (D/M/YYYY, including the row-4 case `2/3/2018` = March 2
  2018, confirming the day-first fix from earlier this session holds),
  `excel_serial_date`, `epoch_millis_at`, and `epoch_micros_at` all agreed on
  the same underlying instant per row, cross-checked against each other and
  against the raw SQLite values. All correct.
- Findings: no findings. (Note: the `iso8601_timestamp` heuristic's rationale
  text is misleading for non-ISO8601 formats like `logged_at`'s
  `M/D/YYYY h:mm:ss AM/PM`, but the resulting conversion is correct — a
  labeling/rationale-clarity nit, not a behavior bug, so not logged as a
  finding.)

## 9. sample-large.sqlite

- Columns: 4 total, 1 needs-review (`flag`, `boolean01` at 0.55 — correctly
  gated).
- Wall-clock: profile ~0.07s, load ~0.3s for 100,000 rows.
- Spot-checked: row count (100,000 both sides) and first 3 rows — exact
  match.
- Findings: no findings.

## 10. sample-numeric-text.sqlite

- Columns: 5 total, 3 needs-review-eligible but all auto-resolved (`.99`/`.9`
  confidence) — actually 0 needs-review, profile exited 0.
- Wall-clock: profile ~0.02s, load ~0.1s.
- Spot-checked: `postal_code` correctly stayed `text` (values like `"07030"`,
  `"00501"` have leading zeros, correctly not converted to integer);
  `year_founded` (`"1998.0"` → `1998`), empty-string → `NULL` — all correct.
- Findings: no findings.

## 11. sample-types.sqlite

- Columns: 6 total, all clean (0 needs-review).
- Wall-clock: profile ~0.02s, load ~0.14s.
- Spot-checked all SQLite storage classes: BLOB (hex-exact), large negative
  integer (`-9007199254740992`, near int64/float64 boundary — exact),
  scientific-notation REAL (`1e+100`), multi-byte Unicode TEXT, multiline
  TEXT, NULL — all matched exactly.
- Findings: no findings.

## 12. sample-uuids.sqlite

- Columns: 3 total, all clean (0 needs-review).
- Wall-clock: profile ~0.08s, load ~0.13s.
- Spot-checked: single-value UUID column `station_id` loaded correctly as
  `uuid` type; no NUL-joined multi-value UUIDs present in this fixture (issue
  #12's shape doesn't appear here).
- Findings: no findings.

## 13. sample-varchar.sqlite

- Columns: 6 total across 2 tables, 2 needs-review
  (`varchar_length_preservation` at 0.5 — correctly gated).
- Wall-clock: profile ~0.02s, load ~0.11s.
- Spot-checked: all rows in both tables — exact match, no truncation despite
  the declared `VARCHAR(45)`/`VARCHAR(100)` constraints (data was within
  bounds).
- Findings: no findings.

## 14. NTAD_Aviation_Facilities_698356094499483505.geodatabase

- Columns: 87 total, 86 clean (Esri `int32`/`float64`/`text(n)` typenames all
  correctly mapped via `esri_typename_mapping` at 0.99), 1 needs-review
  (`SHAPE`, geometry blob).
- Wall-clock: profile ~0.23s, load ~0.4s for 19,426 rows.
- Spot-checked: row count (19,426 both sides), `LAT_DECIMAL`/`LONG_DECIMAL`/
  `ELEV` for 3 rows — exact match.
- Findings:
  - **Issue #3 evidence**: `SHAPE` (`geometryblob` Esri type) correctly
    detected and flagged for review at 0.4 confidence with `transform:
    drop_column`, rationale explicitly noting "Esri proprietary binary
    geometry column; cannot be represented without PostGIS." Working as
    designed (review-gated, not silently dropped), but a clean concrete
    example for issue #3's PostGIS-geometry-type gap.
  - **Issue #3 evidence**: `OBJECTID` (Esri's standard auto-increment
    surrogate key) maps to plain `integer`, not `serial`/`bigserial`.

## 15. SchoolSites2425_-4255819620268625087.geodatabase

- Columns: 84 total, 83 clean (Esri `int32`/`float32`/`text(n)`/`realdate`
  typenames correctly mapped; two `realdate` columns — `OpenDate` and
  `ClosedDate` — correctly recognized as Esri Julian Day Numbers via
  `esri_julian_day` at 0.9 confidence), 1 needs-review (`SHAPE`, geometry
  blob, same shape as fixture #14).
- Wall-clock: profile ~0.3s, load ~0.32s for 9,982 rows.
- Spot-checked: row count (9,982 both sides); `OpenDate`/`ClosedDate` Julian
  Day → date conversion verified against raw JDN values (e.g. `2453975.5` →
  `2006-08-27`, `2460856.5` → `2025-06-29`) — correct.
- Findings:
  - **Issue #3 evidence**: same `SHAPE`-column and `OBJECTID`-surrogate-key
    pattern as fixture #14.

---

## Summary of distinct bugs (for Phase 3 triage)

1. **New bug — silent precision loss in `numeric_text_to_integer`**
   (`internal/copywriter/transform.go`): parses via `strconv.ParseFloat`
   then casts to `int64`, corrupting large (>~2^53) integers stored as text.
   Reproduced in `bikes.db.legacy_id`. High severity — silent data
   corruption, not a crash.
2. **New bug — `default_passthrough` trusts declared SQLite type affinity
   over sampled data** for mixed-type columns: `atomic_database.db`'s
   `XRAY_ENERGIES.Inner`/`Outer` are declared `INT` but contain a mix of
   integers and text (legal under SQLite's dynamic typing), leading to a
   load-time COPY crash despite 0.99 "confidence." Medium-high severity —
   currently manifests as a crash, but for less obviously-broken mixed data
   could plausibly manifest as silent coercion/loss instead.
3. **Issue #1** — concrete real-data confirmation: `sakila.db`'s
   `customer.active` (`CHAR(1)`, values `'0'`/`'1'`) is silently
   auto-approved to `integer` at 0.9 confidence via `numeric_text` because
   the column has TEXT affinity, so `boolean01` never evaluates it — even
   though the identically-named/semantically-identical `staff.active`
   (`SMALLINT` affinity) correctly gets flagged via `boolean01`. The gap is
   that `numeric_text` and `boolean01` don't cross-check each other for
   0/1-only text columns.
4. **Issue #3** — several real examples across fixtures of `AUTOINCREMENT`/
   Esri `OBJECTID` surrogate keys mapping to plain `integer` instead of
   `serial`/`bigserial` (atomic_database.db, chinook.db, sakila.db, both
   geodatabases), and of Esri geometry blobs (`SHAPE` columns) correctly
   review-gated but confirming the missing PostGIS geometry type.
5. **Issue #12** — not reproduced in this fixture set; no NUL-joined
   multi-value UUID columns were encountered among the 15 checked-in
   fixtures.
