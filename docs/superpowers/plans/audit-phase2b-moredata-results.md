# Phase 2B Results — `../more data/` (25 databases)

Executed per the plan's "Per-database procedure." All scratch Postgres databases and
`/tmp/audit-p2b` files were dropped/cleaned after each run.

Column counts use the tool's own auto-approve threshold (confidence >= 0.9 = clean;
< 0.9 = needs-review).

---

## 1. bigendianwal.db
- Clean/needs-review: 0 / 0 (0 tables — file has an empty schema)
- Wall-clock: instant; profile exit 0
- Crash: none
- Findings: none. Observation: this is a WAL-mode torture-test file with `database pages 1`
  and no schema — genuinely empty at the SQLite level, not a tool bug.

## 2. collision.db
- Clean/needs-review: 4 / 0
- Wall-clock: instant; profile exit 0, load exit 1
- Crash: **yes** — `load` fails
- Findings:
  - **Genuine bug (crash).** Table `products` has two columns whose names are identical
    in their first 63 bytes (`col_very_long_name_that_exceeds_postgresql_identifier_limit_aaax`
    / `...aaay`). `profile` resolves both fine, but `load` fails DDL generation:
    `ERROR: column "col_very_long_name_..._aaa" specified more than once (SQLSTATE 42701)`.
    The tool does not detect or disambiguate identifier collisions caused by Postgres's
    63-byte identifier truncation. This is exactly the scenario the fixture is named for.

## 3. companies.db
- Clean/needs-review: 10 / 0
- Wall-clock: instant
- Crash: none
- Findings: none. **Regression check passed** — `year_founded`, `current_employees`,
  `total_employees` all correctly resolve via `numeric_text_to_integer` (previously fixed
  bug). Load matched source exactly (55,991 rows, spot-checked 5 rows + full count).

## 4. demo01.db
- Clean/needs-review: n/a — profile crashed before producing a config
- Wall-clock: instant
- Crash: **yes**
- Findings:
  - **Genuine bug (crash).** `profile` fails outright:
    `error: reading schema: reading foreign keys for delta: sql: Scan error on column
    index 4, name "to": converting NULL to string is unsupported`.
    Root cause: table `delta` has `srcid INTEGER NOT NULL REFERENCES blob` — a valid,
    common SQLite foreign-key form with no explicit referenced-column list (implicit
    reference to the parent's primary key). `PRAGMA foreign_key_list` returns NULL for
    the `to` column in this case, and the schema reader scans it into a non-nullable
    string, panicking the whole profile run. Confirmed as systemic — reproduces
    identically on `sqliterepo.db` (both are Fossil SCM repository databases using this
    FK style).

## 5. dow-health-taxis.db
- Clean/needs-review: 20 / 0 (3 tables: dowjones, healthexp, taxis)
- Wall-clock: instant
- Crash: none
- Findings: none confirmed as bugs. **Observation:** `taxis.pickup`/`dropoff` are
  timezone-naive text (`2019-03-23 20:21:09`, no offset) and resolve to `timestamptz`
  via `iso8601_to_timestamptz` at 0.9 confidence (auto-approved). The transform assumes
  UTC; loaded values reinterpreted in local time show as `17:29:03-06` etc. Since the
  source carries no explicit timezone, there's no ground truth to call this "wrong" in
  isolation — flagging only because the plan explicitly calls out timestamp-timezone-shift
  as a spot-check risk, and it's the same code path implicated in the confirmed date-shift
  bug found elsewhere (see employee.db, neh-grants.db, sunspots.db, TPC-H-small.db below).

## 6. employee.db
- Clean/needs-review: 26 / 2 (7 tables)
- Wall-clock: ~5.5s profile, ~30.5s load (300k+ employees, 2.8M salary rows)
- Crash: none (needs-review gate correctly triggered exit 1 on `first_name`/`last_name`
  VARCHAR-length-preservation ambiguity — working as designed)
- Findings:
  - **Genuine bug, high severity, matches "timestamp shifted by a timezone" spot-check
    warning in the plan.** `birth_date`, `hire_date` (table `employees`) and
    `from_date`/`to_date` (tables `dept_emp`, `dept_manager`, `salaries`, `titles`) are
    all declared `DATE` with pure date values and no time-of-day component
    (`1953-09-02`). The `iso8601_timestamp` heuristic resolves them to `timestamptz` via
    `iso8601_to_timestamptz` at **0.9 confidence — auto-approved, no review required**.
    On load, the transform assumes UTC midnight; displayed in local time (or any
    non-UTC session) the calendar date rolls back a day:
    `1953-09-02` → loaded as `1953-09-01 19:00:00-05`. This is silent data corruption a
    human would never notice from a "successful" load. Reproduced identically on
    `neh-grants.db`, `sunspots.db`, and `TPC-H-small.db` — see below. This is squarely
    issue #3 territory (missing a plain `date` target type / DATE-vs-timestamp
    distinction) and should be logged as a concrete, high-confidence example for it.

## 7. iris.db
- Clean/needs-review: 5 / 0
- Wall-clock: instant
- Crash: none
- Findings: none. Exact match on load (150/150 rows, values identical).

## 8. iso10383-mic.db
- Clean/needs-review: 15 / 2
- Wall-clock: instant; profile exit 1 (review gate, working as designed)
- Crash: none
- Findings: none. `LAST VALIDATION DATE` / `EXPIRY DATE` correctly flagged for review
  because `numeric_text` and `yyyymmdd_date` heuristics disagreed — the resolver still
  picked the correct winner (`date` via `yyyymmdd_to_date`, confirmed exact match on
  load for `CREATION DATE`/`EXPIRY DATE` against source). Good contrast case showing the
  tool *can* map integer/text YYYYMMDD columns to plain `date` correctly — makes the
  DATE-as-timestamptz bug above (employee.db etc.) more clearly a heuristic gap for the
  ISO-8601-with-full-timestamp path specifically.

## 9. kjvbible-u16be.db
- Clean/needs-review: 6 / 0 (3 tables)
- Wall-clock: instant
- Crash: none
- Findings: none. UTF-16BE encoding correctly decoded; verse text byte-for-byte
  identical to source (spot-checked Genesis 1:1-3, full 31,097-row count match).

## 10. kjvbible-u8.db
- Clean/needs-review: 8 / 0 (3 tables)
- Wall-clock: instant
- Crash: none
- Findings: none. Identical spot-check results to the u16be variant.

## 11. littleendianwal.db
- Clean/needs-review: 0 / 0 (0 tables)
- Wall-clock: instant
- Crash: none
- Findings: none. Same empty-schema WAL torture-test file as bigendianwal.db — observation
  only, not a tool bug.

## 12. manyblobs-4k.db
- Clean/needs-review: 6 / 0 (2 tables)
- Wall-clock: instant
- Crash: none
- Findings: none. Verified via MD5 hash comparison (Python `sqlite3` vs `psql`) on
  multiple blobs plus full-table byte-length sum (8,394,754 bytes both sides,
  4,098 rows) — no truncation or corruption.

## 13. multilinetext.db
- Clean/needs-review: 6 / 0 (2 tables)
- Wall-clock: instant
- Crash: none
- Findings: none. Embedded newlines preserved; content length sum matches exactly
  (630,269 chars, 4 rows).

## 14. neh-grants.db
- Clean/needs-review: 29 / 2
- Wall-clock: instant; profile exit 1 (review gate on Latitude/Longitude, working as
  designed)
- Crash: none
- Findings:
  - **Second confirmed instance of the date-shift bug (see employee.db).**
    `BeginGrant`, `CouncilDate`, `EndGrant` are declared TEXT with values like
    `4/1/2006 12:00:00 AM` (always midnight, i.e. functionally date-only) and resolve
    to `timestamptz` via `iso8601_to_timestamptz` at 0.9 confidence, auto-approved.
    Loaded: source `4/1/2006 12:00:00 AM` → Postgres `2006-03-31 18:00:00-06` — the
    calendar date rolls back a full day.
  - `Latitude`/`Longitude` mixing numeric values with an `"Unknown"` sentinel correctly
    flagged for review at 0.85 confidence via `sentinel_null` — working as designed, not
    a bug.

## 15. random-json.db
- Clean/needs-review: 5 / 0 (2 tables)
- Wall-clock: instant
- Crash: none
- Findings:
  - **New type-decision gap (does not cleanly fit #1/#3/#12 — recommend its own
    backlog item).** Column `x` (table `data1`) is declared `JSON` in SQLite and every
    sampled value is valid, deeply-nested JSON. The resolver assigns `text` at 0.99
    confidence via `default_passthrough` — no heuristic recognizes the declared `JSON`
    type as a signal for Postgres `jsonb`. Data itself loads intact (byte-exact,
    verified via length-sum: 55,151,104 bytes both sides, 3,840 rows) — this is a missed
    opportunity, not data loss, but a real, easily-actionable type-decision gap given
    SQLite's declared type is an unambiguous, unused signal here.

## 16. rt5i.db
- Clean/needs-review: 26 / 0 (5 tables — an R-tree spatial index database)
- Wall-clock: ~0.7s profile, ~3.2s load (500k + 500k + 83k + 83k rows)
- Crash: none
- Findings: none. `rt5i`, `rt5i_rowid`, `rt5i_parent`, `rt5i_node` (R-tree shadow
  tables) all loaded and spot-checked correctly, including binary blob payload in
  `rt5i_node.data` (byte-length sum match: 37,213,120 bytes).

## 17. sample.db
- Clean/needs-review: 6 / 0 (2 tables)
- Wall-clock: instant
- Crash: none
- Findings: none. Exact match, trivial fixture.

## 18. sqliterepo.db
- Clean/needs-review: n/a — profile crashed before producing a config
- Wall-clock: instant
- Crash: **yes**
- Findings:
  - **Same bug as demo01.db, confirmed systemic.** Identical crash:
    `error: reading schema: reading foreign keys for delta: sql: Scan error on column
    index 4, name "to": converting NULL to string is unsupported`. This is a Fossil SCM
    repository database (much larger, 110MB) with the same `delta` table / implicit-FK
    pattern. Reproducing on two independent real-world databases confirms this is not a
    one-off fixture quirk but a genuine gap in FK-list scanning for any SQLite schema
    using column-list-less `REFERENCES` clauses.

## 19. ssb-small.db
- Clean/needs-review: 30 / 5
- Wall-clock: instant; profile exit 1 (review gate, working as designed); load fails
  partway through
- Crash: load errors on FK constraint (see below) — not a profiler/DDL/COPY crash
- Findings:
  - **Issue #1 data point (no bug — review gate worked correctly).** `date` table's
    `d_monthnuminyear`, `d_lastdayinweekfl`, `d_lastdayinmonthfl`, `d_holidayfl`,
    `d_weekdayfl` are all flagged at 0.55 confidence via `boolean01` and correctly
    withheld from auto-approval. Notably `d_monthnuminyear` (a month number 1–12) is
    genuinely all `1` in this particular 25-row cut of the data — a case where a human
    reviewer, not blind auto-approval, is exactly the right gate.
  - **Observation, not a tool bug:** `load` fails at the FK-constraint stage —
    `ERROR: insert or update on table "lineorder" violates foreign key constraint
    "fk_lineorder_lo_commitdate"`. Confirmed via `PRAGMA foreign_key_check` that the
    *source* SQLite data already violates its own declared FK constraints (2,462
    distinct `lo_commitdate` values with no matching `date.d_datekey`) — SQLite doesn't
    enforce FKs by default, so this data problem was already latent in the "small" cut
    of this benchmark fixture. The tool correctly surfaces it via Postgres's stricter
    enforcement rather than silently loading an invalid constraint. Worth noting that
    the 5 non-FK tables load fine and are left in place (constraint just isn't added) —
    reasonable behavior, but leaves a partially-loaded database on error that isn't
    obviously flagged as such beyond the exit code.

## 20. sunspots.db
- Clean/needs-review: 2 / 0
- Wall-clock: instant
- Crash: none
- Findings:
  - **Third confirmed instance of the date-shift bug.** Column `Year` (declared TEXT,
    values like `2012-12-31`, no time component) resolves to `timestamptz` at 0.9
    confidence, auto-approved. Loaded: `2012-12-31` → `2012-12-30 18:00:00-06`.

## 21. superheroes.db
- Clean/needs-review: 7 / 0
- Wall-clock: instant
- Crash: none
- Findings: none. `first_appearance_year` correctly resolves via `numeric_text_to_integer`;
  full 6,895-row spot-check matched exactly.

## 22. test_pk.db
- Clean/needs-review: 3 / 0 (2 tables, both empty)
- Wall-clock: instant
- Crash: none
- Findings: none. Schema-only fixture (0 rows both tables); FK and PK constraints
  created correctly.

## 23. titanic.db
- Clean/needs-review: 10 / 2
- Wall-clock: instant; profile exit 1 (review gate, working as designed)
- Crash: none
- Findings:
  - **Issue #1 data point (no bug).** `Survived` flagged at 0.55 via `boolean01` —
    correctly withheld from auto-approval even though it happens to be a genuine
    boolean in this dataset; review gate did its job. `Age` (text with fractional
    values) correctly flagged at 0.85 via `numeric_text`. Full 891-row load matched
    source exactly on spot-checked columns.

## 24. TPC-H-small.db
- Clean/needs-review: 47 / 1 (8 tables — standard TPC-H schema)
- Wall-clock: instant; profile exit 1 (review gate, working as designed)
- Crash: none
- Findings:
  - **Fourth confirmed instance of the date-shift bug, on a standard, widely-used
    benchmark schema.** `LINEITEM.L_SHIPDATE`/`L_COMMITDATE`/`L_RECEIPTDATE` and
    `ORDERS.O_ORDERDATE` are declared `DATE`, pure-date values (`1996-01-02`), resolved
    to `timestamptz` at 0.9 confidence, auto-approved. Loaded: `1996-01-02` →
    `1996-01-01 18:00:00-06`. Given TPC-H is an industry-standard schema, this
    significantly raises the real-world impact of the bug already seen in employee.db,
    neh-grants.db, and sunspots.db.
  - **Issue #1 data point (no bug).** `ORDERS.O_SHIPPRIORITY` (all-zero in this sample)
    correctly flagged at 0.55 via `boolean01` rather than auto-approved.

## 25. type-mismatch.db
- Clean/needs-review: 3 / 0
- Wall-clock: instant; profile exit 0 (no review gate triggered)
- Crash: **yes** — `load` fails
- Findings:
  - **Genuine bug, high severity, exactly matches this fixture's evident purpose.**
    Column `qty` (table `products`) is declared `INTEGER`, but SQLite's weak typing
    allows row `id=2` to store `qty = 'lots-of-it'` (TEXT). The profiler samples all 3
    rows (well within `--sample-size 500`) yet resolves `qty` as `integer` at **0.99
    confidence via `default_passthrough` — auto-approved, no review** despite one
    sampled value being unambiguously non-numeric. `load` then hard-fails:
    `ERROR: unable to encode "lots-of-it" into binary format for int4 (OID 23):
    cannot find encode plan`. The `default_passthrough` path does not validate that
    sampled values are actually parseable as the column's declared/target type before
    assigning high confidence — a real gap distinct from the heuristic-driven paths
    (`numeric_text`, `boolean01`, etc.) which do inspect samples.

---

## Summary

- **25 databases run** (all files in `../more data/`).
- **Confirmed genuine tool bugs / crashes:**
  1. `collision.db` — DDL crash on Postgres 63-byte identifier collisions (no
     disambiguation).
  2. `demo01.db` / `sqliterepo.db` — profile crash on implicit-column FK
     (`REFERENCES table` with no column list) → NULL scan panic. Systemic, reproduced
     on two independent files.
  3. **Date-shift bug (4 independent reproductions: `employee.db`, `neh-grants.db`,
     `sunspots.db`, `TPC-H-small.db`)** — DATE-only / midnight-only values get resolved
     to `timestamptz` via `iso8601_to_timestamptz` at 0.9 confidence (auto-approved),
     and loading shifts the calendar date across a day boundary in non-UTC sessions.
     Highest-impact finding of this pass — silent, systematic data corruption on a very
     common real-world column shape, auto-approved with no human review. Strong
     candidate for issue #3 (needs a `date`-vs-`timestamptz` distinction in the ISO8601
     heuristic, mirroring what `yyyymmdd_date` already does correctly).
  4. `type-mismatch.db` — `default_passthrough` assigns 0.99 confidence to an
     `INTEGER`-declared column containing a non-numeric sampled value; load then
     hard-crashes on encode.
- **New type-decision gap (not #1/#3/#12):** `random-json.db` — declared `JSON` column
  resolves to `text` instead of `jsonb`; data intact but a clear, actionable type
  signal is unused.
- **Issue #1 data points (working as designed, logged for the backlog, not bugs):**
  `ssb-small.db` (5 columns), `titanic.db` (`Survived`), `TPC-H-small.db`
  (`O_SHIPPRIORITY`) — all correctly gated behind human review rather than
  auto-approved.
- **Issue #3 data point:** the date-shift bug above.
- **Issue #12:** no NUL-joined multi-value UUID columns encountered in this batch.
- **Observations (not tool bugs):** `bigendianwal.db`/`littleendianwal.db` are
  genuinely empty-schema WAL torture-test files; `dow-health-taxis.db`'s
  timezone-naive-timestamp-to-UTC assumption (same code path as the date-shift bug,
  but no ground truth to call it wrong in isolation); `ssb-small.db`'s source data
  already violates its own declared FK constraints (SQLite doesn't enforce FKs),
  surfaced correctly by Postgres's stricter enforcement at load time.
- **Clean, no-findings databases:** `companies.db` (regression-verified), `iris.db`,
  `iso10383-mic.db`, `kjvbible-u16be.db`, `kjvbible-u8.db`, `manyblobs-4k.db`,
  `multilinetext.db`, `rt5i.db`, `sample.db`, `superheroes.db`, `test_pk.db`.

All scratch Postgres databases and `/tmp/audit-p2b` were dropped/removed after each
database and at the end of the run.
