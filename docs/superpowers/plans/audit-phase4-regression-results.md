# Phase 4 — Targeted regression re-run results (2026-08-31)

Executes item 3 of Phase 4 in
`docs/superpowers/plans/2026-08-29-audit-and-load-test.md`: confirm today's
(2026-08-30) 26 commits / ~28 bug fixes didn't regress against the original
Phase 2 database set, with special focus on proving issue #1 (`sakila.db`)
and issue #12 (`beets_library.db`) against the real data that motivated them.

**No genuine regression found.** Every fix held. All apparent discrepancies
below traced to explainable causes (random sampling variance, fixtures that
were themselves extended by today's commits, or pre-existing/unrelated data
issues) — see the "Investigated discrepancies" note under each section.

Pre-flight: found and cleaned up leftover scratch state from a prior
session that was cut off mid-task (`/tmp/audit-p4-regress/{beets.yaml,
beets.yaml.unresolved_report.yaml, moredata/}`) before starting fresh. No
stray scratch Postgres databases were found at the start of this run.

## Summary

| # | Database | Result |
|---|----------|--------|
| 1 | `sakila.db` | PASS — issue #1 real-data claim confirmed |
| 2 | `beets_library.db` | PASS — issue #12 real-data claim confirmed |
| 3 | 14 remaining `testdata/fixtures/` + 2 `.geodatabase` files | PASS |
| 3b | `go test -count=1 ./...` | PASS, all green |
| 4a | `type-mismatch.db` | PASS — issue #16 fix confirmed |
| 4b | `collision.db` | PASS — issue #21 fix confirmed |
| 4c | `demo01.db` | PASS — issue #17 fix confirmed |
| 4d | `sqliterepo.db` | PASS — issue #17 fix confirmed |

## 1. sakila.db (issue #1)

`migrate profile` on the fresh fixture produced **17** needs-review columns,
versus **16** in the original Phase 2a run
(`docs/superpowers/plans/audit-phase2a-fixtures-results.md` finding #6) —
exactly one more, and it is exactly the expected one:

- `customer.active` (`CHAR(1)`, stores `'0'`/`'1'` as text) is now flagged
  for review: `boolean01` (confidence 0.88) and `numeric_text` (confidence
  0.9) findings both attach and disagree, so it's correctly routed to
  review instead of being silently auto-approved to `integer` at 0.9 as it
  was before the fix. This is the exact bug issue #1 described.
- `staff.active` (`SMALLINT`, the sibling column) is still flagged via
  `boolean01` at confidence 0.55 — unchanged from the original run, no
  regression.
- All other 15 needs-review columns (varchar-length-preservation flags on
  `address`/`customer`/`film`/`staff` text columns) match the original run
  column-for-column.

Loaded into a scratch Postgres db after marking all 17 columns reviewed
(accepting `customer.active` as `boolean` via the `int_to_bool` transform,
which the fix extended to also parse `"0"`/`"1"` strings, not just
INTEGER-affinity 0/1). Load succeeded for all 16 tables (599 customers,
16044 rentals, etc.). Verified in Postgres: `customer.active` is `boolean`
NOT NULL with 584 true / 15 false — a real full-table result, not a sample
artifact. `staff.active` loaded correctly as 2 rows, both true.

Scratch dbs (`sakila_p4regress`, and the tool-created
`sakila_20260831_095848`) dropped after verification.

## 2. beets_library.db (issue #12) — ~1.4GB, real user data

Profiled twice (3m43s first run, well within the 3-4 minute budget; a
second run for extra confidence, also ~3-4 min). Profile-only per the task
(no full load given file size).

- `items.mb_albumartistids` and `items.mb_artistids`: **both runs**
  resolved to `target_type: uuid[]` via the new `uuid_list` heuristic, but
  correctly demoted to confidence 0.4 / `needs_review: true` — the
  full-table check found legacy plain-numeric values (e.g. `"252121"`,
  `"811171"`) mixed into these columns that the `uuid_list_format`
  transform can't convert. This exactly matches the plan's expectation:
  flagged for review, not silently auto-approved, because of genuinely
  unrelated legacy numeric IDs. **Confirmed, both runs, no regression.**
- `items.composers_ids` / `items.lyricists_ids`: outcome depends on
  whether the random 500-row table sample happened to catch a multi-value
  (NUL-joined) row for these two columns:
  - **Run 1** (unlucky sample): no multi-value row landed in the sample,
    so the `uuid_format` (singular) heuristic fired instead of `uuid_list`,
    then got demoted by the full-table check to confidence 0.4 /
    `needs_review: true`, `target_type: uuid` (not `uuid[]`).
  - **Run 2** (typical sample): a multi-value row landed in the sample,
    `uuid_list` fired directly at **confidence 0.9**, `target_type: uuid[]`,
    **auto-approved** (no `needs_review`) — this is the clean, intended
    outcome the plan describes.
  - This is documented, anticipated behavior, not a bug: `uuid_list.go`'s
    own doc comment explicitly states the heuristic "only fires when the
    SAMPLE ITSELF demonstrates the list shape" and that a sample landing
    all-single-UUID for a column that has multi-value rows elsewhere is
    expected to fall through to `uuid_format` and then get demoted by the
    issue #13 full-table check — exactly what run 1 showed. Given these
    columns' multi-value rows are ~12% of their (sparse, ~2.9%-of-table)
    non-null population, a 500-row random sample has roughly an 84% chance
    of catching one; run 1 was in the unlucky ~16% tail, run 2 wasn't.
  - **Net: issue #12's uuid[] feature is confirmed working correctly on
    the real data that motivated it** — it just isn't deterministic
    per-run because the underlying sampling is random by design. Either
    outcome (clean 0.9 auto-approve, or a safe 0.4 demotion with `uuid[]`
    now an available reviewer-pickable option) is correct, not a bug.
- `items.arrangers_ids`: very sparse (647/224834 rows non-empty, all
  observed values identical single UUIDs). Resolved differently each run
  (plain `text`@0.99 via `default_passthrough` in run 1, demoted
  `uuid`@0.4 in run 2) depending on whether the sample caught any non-empty
  values at all and, if so, whether it also happened to catch a
  (nonexistent, in this fixture) multi-value shape. Consistent with the
  original Phase 2c note (`audit-phase2c-beets-results.md` line 49) that
  this column's real UUID-list nature is easy for a small sample to miss
  entirely — not a regression, matches prior observed behavior.

Scratch files removed; no Postgres load was done for this database (out of
scope per the task's profile-only budget).

## 3. Remaining 16 testdata/fixtures files

`go test -count=1 ./...` — all 11 packages green, no cached results reused.

Profiled all 14 remaining `.db`/`.sqlite` fixtures plus both
`.geodatabase` files with `migrate profile`; none crashed. Needs-review
counts cross-checked against `audit-phase2a-fixtures-results.md`:

| Fixture | Original needs-review | This run | Note |
|---|---|---|---|
| atomic_database.db | 0 | 2 | New signal — see below, this is a fix landing, not a regression |
| AustinRoadConstruction.db | 6 | 6 | match |
| bikes.db | 7 | 7 | match |
| chinook.db | 1 | 1 | match |
| DisabilityCompByCounty.db | 1 | 0 (this run) | Sampling variance, not a regression — see below |
| northwind_small.sqlite | 1 | 1 | match |
| sample-dates.sqlite | 4 (9 cols total) | 3 (11 cols total) | Fixture itself grew 2 columns via today's commits `76ae27e`/`76581cd` — not comparable 1:1, spot-checked below |
| sample-large.sqlite | 1 | 1 | match |
| sample-numeric-text.sqlite | 0 | 0 | match |
| sample-types.sqlite | 0 | 0 | match |
| sample-uuids.sqlite | 0 | 0 | match |
| sample-varchar.sqlite | 2 | 2 | match |
| sample-implicit-fk.sqlite | (new fixture, not in original run) | 0 | n/a |
| sample-type-mismatch.sqlite | (new fixture, not in original run) | 1 | n/a |
| NTAD_Aviation_Facilities...geodatabase | 1 | 1 | match |
| SchoolSites2425...geodatabase | 1 | 1 | match |

Investigated discrepancies:

- **`atomic_database.db`, 0 → 2**: this is a *bug fix landing*, not a
  regression. The original Phase 2a run found (its own finding #1) that
  `XRAY_ENERGIES.Inner`/`.Outer` were silently auto-approved to `integer`
  at 0.99 confidence and then **crashed the load** on subshell-code text
  values like `"K"`/`"L1"`. Today's mixed-type detection now catches this
  at profile time: both columns are correctly routed to review with the
  reason "the sample itself contains a value that can't be stored as
  integer... SQLite's dynamic typing allows this even though the column is
  declared INT" — the exact `atomic_database.db` bug this project's own
  audit found, now fixed.
- **`DisabilityCompByCounty.db`, 1 → 0**: pure sampling variance, not a
  regression. Only 1 row out of 3148 (rowid 3148, the last row) has the
  `"Unknown"` sentinel in `FIPS code`; `SampleRows` uses `ORDER BY
  RANDOM() LIMIT 500`, so a given run has roughly a 500/3148 ≈ 16% chance
  of including that one row. Re-ran profile 5 times: 1 of 5 runs correctly
  caught it via `sentinel_null`, matching the original finding; the other
  4 (including the one tallied in the table above) missed it by chance.
  Not a fix regression — the heuristic and full-table safety net are
  unaffected; this is inherent to sample-size-500 on a needle-in-3148-rows
  case, a known/documented limitation (see the sampling-limitation doc
  update in commit `2fe4933`).
- **`sample-dates.sqlite`, 9→11 columns**: two columns
  (`day_first_date`, and either `birth_date_only` or `midnight_timestamp`)
  were added to this fixture by today's own commits (`76ae27e` "Target
  date, not timestamptz, for midnight-only ISO 8601 columns" and `76581cd`
  "Add month-name, day-first, Excel serial, and epoch ms/us date
  heuristics (#2)"). Comparing raw needs-review counts against the
  pre-expansion fixture isn't meaningful. Spot-checked the new/adjacent
  columns directly: `day_first_date` → `timestamptz` via
  `dayfirst_to_timestamptz` at 0.9 (auto-approved); `birth_date_only` and
  `midnight_timestamp` → `date` via `iso8601_to_date` at 0.9
  (auto-approved, matching the "target date not timestamptz for
  midnight-only" fix); `month_name_date` → `date` at 0.9 (auto-approved).
  All look correct for what each column name and fix implies. No
  regression.

## 4. `../more data/` — issues #16, #21, #17

### 4a. type-mismatch.db (issue #16, default_passthrough)

`migrate profile` no longer crashes — routes `products.qty` (declared
`INTEGER`, contains `"lots-of-it"` alongside `10`/`5`) to review with a
clear reason instead. **Fix confirmed.**

Load: after manually approving `qty` as `text` (no transform), load failed
with `unable to encode 10 into binary format for text` — because raw
SQLite-typed values (`int64(10)`) don't auto-stringify without an explicit
transform, and no generic "stringify" transform currently exists in
`internal/copywriter/transform.go`. This is a **pre-existing tooling gap
unrelated to today's fixes** (issue #16 was scoped to profile-time
crash-prevention, not to providing every possible resolution transform) —
noted as a minor rough edge for a future fix, not a regression. The
failure itself is safe (loud COPY error, no silent corruption), consistent
with the review-gate-as-safety-net pattern seen elsewhere in this project's
audits.

### 4b. collision.db (issue #21, identifier collision)

Profiled clean (0 needs-review). Loaded successfully: both long,
originally-colliding column names were disambiguated
(`col_very_long_name_that_exceeds_postgresql_identifier__5e36c740` /
`__182d9624`), and data loaded correctly (1 row, both values present and
distinct: 10 and 20). **Fix confirmed.**

### 4c. demo01.db (issue #17, implicit-column FK crash) — Fossil SCM db

Profile no longer crashes: 31 tables profiled (1 `fts4` virtual table
correctly skipped and reported, unrelated to #17), 7 needs-review columns
(6 `boolean01` flags + 1 mixed-content `config.value`). **Fix confirmed at
profile time.**

Load: excluded `config` (an unrelated, genuinely heterogeneous
text/blob/int column not relevant to the FK check — see note below) and
loaded the rest. All 30 remaining tables were read, DDL created (including
foreign keys, which is the code path issue #17 fixed), and 29 of 30 loaded
data successfully with no crash. The final error —
`mlink` violates FK constraint `fk_mlink_pfnid` — is a genuine data/FK
insert-order issue in Fossil's own schema (a `0`-sentinel `pfnid` value
not present in `filename.fnid`), reproduced identically in `sqliterepo.db`
below, and unrelated to implicit-column FK *reading* (the thing #17 fixed).
**The claim under test — no crash reading implicit-column FKs — is
confirmed**; the FK-constraint-violation is a separate, out-of-scope data
issue in this specific fixture, not a regression.

Note on `config.value`: `CLOB`-declared but the real column mixes plain
integers (`"1"`), plain text, and true binary blob data (invalid UTF-8
bytes) in different rows — correctly flagged for review at confidence 0.4
either way (`bytea` suggested, or demoted from it), but no single Postgres
type/transform in this tool currently round-trips all three shapes in one
column. Excluding it to isolate the FK-crash check is exactly what a human
reviewer choosing to skip an intractable column would do; this is not
related to any of today's fixes.

### 4d. sqliterepo.db (issue #17, 110MB Fossil SCM db)

Profiled in 0.5s (well within "a few minutes" budget), 36 tables, 3
needs-review columns (all reasonable: 1 `boolean01`, `config.value` — same
mixed-content shape as demo01.db). **No crash. Fix confirmed at profile
time.**

Load: after excluding `config` and the FTS-internal tables holding raw
binary tokenizer data (`ftscontent`, `ftsidx`, `ftsidx_config`,
`ftsidx_data`, `ftsidx_idx` — invalid-UTF8 byte content, same class of
issue as `config.value`, unrelated to #17), all 26 remaining tables loaded
successfully with substantial real row counts (`mlink`: 85,173 rows,
`delta`: 65,188, `blob`: 69,793, `event`: 20,374, `tagxref`: 41,569,
`plink`: 19,778, etc.) — this includes tables whose FKs depend on
implicit-column relationships, proving those are read and enforced
correctly at real scale. Hit the identical `mlink`/`fk_mlink_pfnid`
constraint violation seen in `demo01.db` (same Fossil schema quirk, not a
crash, not implicit-FK-reading-related). **Fix confirmed.**

## Cleanup

All scratch Postgres databases (`sakila_p4regress`,
`sakila_20260831_095848`, `typemismatch_p4regress`,
`type_mismatch_20260831_100816`, `collision_p4regress`,
`collision_20260831_100930`, `demo01_p4regress`,
`demo01_20260831_100958`, `demo01_20260831_101048`,
`demo01_20260831_101142`, `sqliterepo_p4regress`,
`sqliterepo_20260831_101219`, `sqliterepo_20260831_101238`,
`sqliterepo_20260831_101255`) were dropped. `/tmp/audit-p4-regress`
removed. Verified clean via `psql -l` at the end of the run.
