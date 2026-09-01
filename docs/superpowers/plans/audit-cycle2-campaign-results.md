# verify-all-fixtures campaign — 2026-09-01 06:42:08

PG_URL: `postgres://localhost:5432/?sslmode=disable`  |  sample-size: 500  |  profile-only over: 1200MB

| # | database | size | tables | cols | need-review | profile | load | verify | rows cmp | notes |
|---|---|---:|---:|---:|---:|---|---|---|---:|---|
| 1 | atomic_database.db | 7MB | 12 | 58 | 2 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into XRAY_ENERGIES: ERROR: COPY from stdin failed: unable to encode "K" into binary format for int4 (OID 23): cannot find enc |
| 2 | AustinRoadConstruction.db | 4MB | 1 | 26 | 6 | ok* 0s | ok 1s | PASS 0s | 3538 | - |
| 3 | bikes.db | 284KB | 1 | 14 | 7 | ok* 0s | ok 0s | PASS 0s | 2509 | - |
| 4 | chinook.db | 864KB | 11 | 64 | 1 | ok* 0s | ok 0s | PASS 0s | 15607 | - |
| 5 | DisabilityCompByCounty.db | 192KB | 1 | 14 | 0 | ok 0s | FAIL rc=1 0s | - | - | error: copying into DisabilityCompByCounty: ERROR: COPY from stdin failed: unable to encode "Unknown" into binary format for int4 (OID 23):  |
| 6 | sakila.db | 5MB | 16 | 89 | 17 | ok* 0s | ok 0s | PASS 0s | 46273 | - |
| 7 | northwind_small.sqlite | 284KB | 13 | 89 | 1 | ok* 0s | ok 0s | PASS 0s | 3308 | - |
| 8 | sample-dates.sqlite | 8KB | 1 | 11 | 3 | ok* 0s | ok 0s | PASS 0s | 5 | - |
| 9 | sample-implicit-fk.sqlite | 12KB | 2 | 4 | 0 | ok 0s | ok 1s | PASS 0s | 6 | - |
| 10 | sample-large.sqlite | 1MB | 1 | 4 | 1 | ok* 0s | ok 0s | PASS 0s | 100000 | - |
| 11 | sample-numeric-text.sqlite | 8KB | 1 | 5 | 0 | ok 0s | ok 0s | PASS 0s | 5 | - |
| 12 | sample-type-mismatch.sqlite | 8KB | 1 | 3 | 1 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into products: ERROR: COPY from stdin failed: unable to encode "lots-of-it" into binary format for int4 (OID 23): cannot find |
| 13 | sample-types.sqlite | 8KB | 1 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 3 | - |
| 14 | sample-uuids.sqlite | 8KB | 1 | 3 | 0 | ok 0s | ok 0s | PASS 0s | 3 | - |
| 15 | sample-varchar.sqlite | 12KB | 2 | 6 | 2 | ok* 0s | ok 0s | PASS 0s | 5 | - |
| 16 | NTAD_Aviation_Facilities_698356094499483505.geodatabase | 8MB | 1 | 92 | 0 | ok 0s | ok 0s | PASS 1s | 19426 | - |
| 17 | SchoolSites2425_-4255819620268625087.geodatabase | 7MB | 1 | 97 | 0 | ok 0s | ok 0s | PASS 0s | 9982 | - |
| 18 | bigendianwal.db | 4KB | 0 | 0 | 0 | ok 1s | ok 1s | PASS 0s | 0 | - |
| 19 | collision.db | 8KB | 1 | 4 | 0 | ok 0s | ok 0s | PASS 0s | 1 | - |
| 20 | companies.db | 7MB | 1 | 10 | 0 | ok 0s | ok 1s | PASS 0s | 55991 | - |
| 21 | demo01.db | 504KB | 31 | 140 | 7 | ok* 0s | FAIL rc=1 1s | - | - | error: copying into config: ERROR: COPY from stdin failed: unable to encode "1" into binary format for bytea (OID 17): cannot find encode pl |
| 22 | dow-health-taxis.db | 996KB | 3 | 20 | 0 | ok 0s | ok 1s | PASS 0s | 7356 | - |
| 23 | employee.db | 228MB | 7 | 28 | 2 | ok* 5s | ok 13s | PASS 12s | 3919026 | - |
| 24 | iris.db | 16KB | 1 | 5 | 0 | ok 0s | ok 0s | PASS 0s | 150 | - |
| 25 | iso10383-mic.db | 452KB | 1 | 17 | 0 | ok 0s | ok 0s | PASS 0s | 2662 | - |
| 26 | kjvbible-u16be.db | 8MB | 3 | 8 | 0 | ok 0s | ok 1s | PASS 0s | 31168 | - |
| 27 | kjvbible-u8.db | 4MB | 3 | 8 | 0 | ok 0s | ok 0s | PASS 0s | 31168 | - |
| 28 | littleendianwal.db | 4KB | 0 | 0 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 29 | manyblobs-4k.db | 24MB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 4102 | - |
| 30 | multilinetext.db | 632KB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 8 | - |
| 31 | neh-grants.db | 9MB | 1 | 33 | 2 | ok* 0s | ok 1s | PASS 0s | 9402 | - |
| 32 | random-json.db | 53MB | 2 | 5 | 0 | ok 0s | ok 2s | PASS 0s | 3844 | - |
| 33 | rt5i.db | 44MB | 5 | 21 | 0 | ok 1s | ok 4s | PASS 4s | 1166436 | - |
| 34 | sample.db | 16KB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 10 | - |
| 35 | sqliterepo.db | 105MB | 36 | 158 | 3 | ok* 0s | FAIL rc=1 1s | - | - | error: copying into config: ERROR: COPY from stdin failed: unable to encode "1" into binary format for bytea (OID 17): cannot find encode pl |
| 36 | ssb-small.db | 4MB | 5 | 58 | 6 | ok* 0s | FAIL rc=1 1s | - | - | error: adding foreign key: ERROR: insert or update on table "lineorder" violates foreign key constraint "fk_lineorder_lo_commitdate" (SQLSTA |
| 37 | sunspots.db | 16KB | 1 | 2 | 0 | ok 0s | ok 0s | PASS 0s | 313 | - |
| 38 | superheroes.db | 1MB | 1 | 7 | 0 | ok 0s | ok 0s | PASS 0s | 6895 | - |
| 39 | test_pk.db | 12KB | 2 | 3 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 40 | titanic.db | 72KB | 1 | 12 | 2 | ok* 0s | ok 0s | PASS 0s | 891 | - |
| 41 | TPC-H-small.db | 10MB | 8 | 61 | 1 | ok* 0s | ok 0s | PASS 0s | 86805 | - |
| 42 | type-mismatch.db | 8KB | 1 | 3 | 1 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into products: ERROR: COPY from stdin failed: unable to encode "lots-of-it" into binary format for int4 (OID 23): cannot find |
| 43 | beets_library.db | 1415MB | 5 | 156 | 14 | ok* 36s | skipped (size) | skipped | - | profile-only, 1415MB |

## Summary

- verified clean: **35**  (0 `migrate verify` mismatches anywhere)
- verify FAILED: **0**
- profile/load errors or skips: **8** (1 profile-only + 7 non-passing loads)

## Analysis of the 7 non-passing loads

`scripts/verify-all-fixtures.sh` marks **every** column reviewed before
loading — it rubber-stamps the tool's own suggestions wholesale to test
the auto-pick + load + verify path end to end. That means a column the
tool *correctly* flagged for review gets force-accepted anyway, and the
load then fails as it should. Classified:

| database | need-review | what happened | verdict |
|---|---|---|---|
| `DisabilityCompByCounty.db` | **0** | `FIPS code` (declared INTEGER, 1 row of 3148 holds `"Unknown"`) auto-approved to `integer` @ 0.99, sample missed the bad row, no full-table type-fit check for a no-transform passthrough → COPY crash | **genuine bug → [#69](https://github.com/barrettclark/sqlite2pg/issues/69)** |
| `atomic_database.db` | 2 | `XRAY_ENERGIES.Inner/Outer` correctly flagged (conf 0.4, `"L2"`/`"K"` in sample); script force-accepted | review gate working — rubber-stamp casualty |
| `sample-type-mismatch.sqlite` | 1 | `products.qty` correctly flagged; force-accepted | gate working |
| `type-mismatch.db` | 1 | `products.qty` correctly flagged; force-accepted | gate working |
| `demo01.db` | 7 | `config.value` (CLOB, mixed content) correctly flagged conf 0.4; force-accepted | gate working |
| `sqliterepo.db` | 3 | same `config.value` shape as demo01 | gate working |
| `ssb-small.db` | 6 | `migrate load` applied the source's own declared FK `fk_lineorder_lo_commitdate`; the source data violates it (SQLite doesn't enforce FKs) | pre-existing / not a tool bug (documented in the 2026-08-29 cycle) |

## What Phase A predicted vs. what the campaign hit

Phase A findings [#60](https://github.com/barrettclark/sqlite2pg/issues/60)
(transformed-PK ordering) and
[#61](https://github.com/barrettclark/sqlite2pg/issues/61) (jsonb canonical
form) both cause `verify` to false-FAIL — but neither fired anywhere in
this 42-database load set, because no loaded table had a text PK routed
through a type-changing transform, and no column resolved to `jsonb`
(declared-JSON columns still land as `text`, per the still-open
[#3](https://github.com/barrettclark/sqlite2pg/issues/3) /
`random-json.db`). They are real, but need a column shape this corpus
doesn't contain in a triggering position. Worth a targeted fixture in
Phase C or E rather than relying on the corpus to surface them.

## Scale results (verify at real size)

- `employee.db` — 228 MB, 7 tables, **3,919,026 rows** verified in 12 s
  (load 13 s).
- `rt5i.db` — 44 MB, **1,166,436 rows** verified in 4 s.
- `sample-large.sqlite` — 100,000 rows, instant.
- `beets_library.db` — 1.4 GB, profile only (36 s); over the 1200 MB
  load/verify gate. Full load+verify belongs to Phase D's timing run.

Work dir with all per-database logs and `migrate verify` reports:
`scratchpad/campaign/` (not checked in).

## Note — unrelated stray file

During this session a zero-byte `../more data/DisabilityCompByCounty.db`
appeared (mtime 06:45:15), ~1 minute **after** this campaign finished
(last campaign write 06:44:xx). It is not one of the 25 original
`../more data/` files, the campaign only ever used the checked-in
`testdata/fixtures/` copy (which is intact, 196 KB), and every other
`../more data/` file is untouched with its original timestamp. Most likely
another local process/session. Left in place — not created or owned by
this work. Flag for Barrett.
