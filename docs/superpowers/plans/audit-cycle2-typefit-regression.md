# PR 2 (#69, profile-time type-fit check) — regression campaign

Full `scripts/verify-all-fixtures.sh` run against the PR 2 build.

**35 verified clean, 0 verify FAILURES.** No regression; #69's check newly
flags nothing that was previously auto-approving cleanly.

Key result — **row 5, `DisabilityCompByCounty.db`**: `need-review` is now
**1** deterministically (`FIPS code`), where before #69 it was 0 in ~84%
of runs (whenever the random 500-row sample missed the single `"Unknown"`
row) and the column silently auto-approved to `integer` at 0.99 → COPY
crash with no warning. The load still fails here **only because the
campaign script blindly rubber-stamps the flagged column** (accepts
`integer`, no transform) — a real reviewer now sees "a full-table check
found a value that can't be stored as integer: \"Unknown\"" and picks
`text`. Same rubber-stamp-casualty category as `atomic_database.db` /
`demo01.db` / `type-mismatch.db`; `ssb-small.db` is the known source-data
FK violation. 7 non-passing loads, same set as Phase B — with
`DisabilityCompByCounty` now correctly in the *flagged* group instead of
randomly slipping through.

Scale: `employee.db` 3.9M rows load 18s / verify 14s; `rt5i.db` 1.17M
rows verify 5s. `beets_library.db` profile 36s (need-review count 15,
sampling variance on its `mb_*` UUID columns — unrelated to #69).

---

PG_URL: `postgres://localhost:5432/?sslmode=disable`  |  sample-size: 500  |  profile-only over: 1200MB

| # | database | size | tables | cols | need-review | profile | load | verify | rows cmp | notes |
|---|---|---:|---:|---:|---:|---|---|---|---:|---|
| 1 | atomic_database.db | 7MB | 12 | 58 | 2 | ok* 0s | FAIL rc=1 1s | - | - | error: copying into XRAY_ENERGIES: ERROR: COPY from stdin failed: unable to encode "K" into binary format for int4 (OID 23): cannot find enc |
| 2 | AustinRoadConstruction.db | 4MB | 1 | 26 | 6 | ok* 0s | ok 0s | PASS 1s | 3538 | - |
| 3 | bikes.db | 284KB | 1 | 14 | 7 | ok* 0s | ok 0s | PASS 0s | 2509 | - |
| 4 | chinook.db | 864KB | 11 | 64 | 1 | ok* 0s | ok 0s | PASS 0s | 15607 | - |
| 5 | DisabilityCompByCounty.db | 192KB | 1 | 14 | 1 | ok* 0s | FAIL rc=1 1s | - | - | error: copying into DisabilityCompByCounty: ERROR: COPY from stdin failed: unable to encode "Unknown" into binary format for int4 (OID 23):  |
| 6 | sakila.db | 5MB | 16 | 89 | 17 | ok* 0s | ok 0s | PASS 0s | 46273 | - |
| 7 | northwind_small.sqlite | 284KB | 13 | 89 | 1 | ok* 0s | ok 1s | PASS 0s | 3308 | - |
| 8 | sample-dates.sqlite | 8KB | 1 | 11 | 3 | ok* 0s | ok 0s | PASS 0s | 5 | - |
| 9 | sample-implicit-fk.sqlite | 12KB | 2 | 4 | 0 | ok 0s | ok 1s | PASS 0s | 6 | - |
| 10 | sample-large.sqlite | 1MB | 1 | 4 | 1 | ok* 0s | ok 0s | PASS 1s | 100000 | - |
| 11 | sample-numeric-text.sqlite | 8KB | 1 | 5 | 0 | ok 0s | ok 0s | PASS 0s | 5 | - |
| 12 | sample-type-mismatch.sqlite | 8KB | 1 | 3 | 1 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into products: ERROR: COPY from stdin failed: unable to encode "lots-of-it" into binary format for int4 (OID 23): cannot find |
| 13 | sample-types.sqlite | 8KB | 1 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 3 | - |
| 14 | sample-uuids.sqlite | 8KB | 1 | 3 | 0 | ok 0s | ok 0s | PASS 0s | 3 | - |
| 15 | sample-varchar.sqlite | 12KB | 2 | 6 | 2 | ok* 0s | ok 0s | PASS 0s | 5 | - |
| 16 | NTAD_Aviation_Facilities_698356094499483505.geodatabase | 8MB | 1 | 92 | 0 | ok 1s | ok 0s | PASS 0s | 19426 | - |
| 17 | SchoolSites2425_-4255819620268625087.geodatabase | 7MB | 1 | 97 | 0 | ok 0s | ok 0s | PASS 1s | 9982 | - |
| 18 | bigendianwal.db | 4KB | 0 | 0 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 19 | collision.db | 8KB | 1 | 4 | 0 | ok 0s | ok 0s | PASS 0s | 1 | - |
| 20 | companies.db | 7MB | 1 | 10 | 0 | ok 0s | ok 1s | PASS 0s | 55991 | - |
| 21 | demo01.db | 504KB | 31 | 140 | 7 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into config: ERROR: COPY from stdin failed: unable to encode "1" into binary format for bytea (OID 17): cannot find encode pl |
| 22 | dow-health-taxis.db | 996KB | 3 | 20 | 0 | ok 0s | ok 1s | PASS 0s | 7356 | - |
| 23 | employee.db | 228MB | 7 | 28 | 2 | ok* 4s | ok 18s | PASS 14s | 3919026 | - |
| 24 | iris.db | 16KB | 1 | 5 | 0 | ok 0s | ok 1s | PASS 0s | 150 | - |
| 25 | iso10383-mic.db | 452KB | 1 | 17 | 0 | ok 0s | ok 0s | PASS 0s | 2662 | - |
| 26 | kjvbible-u16be.db | 8MB | 3 | 8 | 0 | ok 0s | ok 1s | PASS 0s | 31168 | - |
| 27 | kjvbible-u8.db | 4MB | 3 | 8 | 0 | ok 0s | ok 0s | PASS 0s | 31168 | - |
| 28 | littleendianwal.db | 4KB | 0 | 0 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 29 | manyblobs-4k.db | 24MB | 2 | 6 | 0 | ok 0s | ok 1s | PASS 0s | 4102 | - |
| 30 | multilinetext.db | 632KB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 8 | - |
| 31 | neh-grants.db | 9MB | 1 | 33 | 2 | ok* 0s | ok 1s | PASS 0s | 9402 | - |
| 32 | random-json.db | 53MB | 2 | 5 | 0 | ok 0s | ok 2s | PASS 0s | 3844 | - |
| 33 | rt5i.db | 44MB | 5 | 21 | 0 | ok 2s | ok 3s | PASS 5s | 1166436 | - |
| 34 | sample.db | 16KB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 10 | - |
| 35 | sqliterepo.db | 105MB | 36 | 158 | 3 | ok* 1s | FAIL rc=1 1s | - | - | error: copying into config: ERROR: COPY from stdin failed: unable to encode "1" into binary format for bytea (OID 17): cannot find encode pl |
| 36 | ssb-small.db | 4MB | 5 | 58 | 6 | ok* 0s | FAIL rc=1 1s | - | - | error: adding foreign key: ERROR: insert or update on table "lineorder" violates foreign key constraint "fk_lineorder_lo_commitdate" (SQLSTA |
| 37 | sunspots.db | 16KB | 1 | 2 | 0 | ok 0s | ok 0s | PASS 0s | 313 | - |
| 38 | superheroes.db | 1MB | 1 | 7 | 0 | ok 0s | ok 0s | PASS 0s | 6895 | - |
| 39 | test_pk.db | 12KB | 2 | 3 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 40 | titanic.db | 72KB | 1 | 12 | 2 | ok* 0s | ok 0s | PASS 0s | 891 | - |
| 41 | TPC-H-small.db | 10MB | 8 | 61 | 1 | ok* 1s | ok 0s | PASS 0s | 86805 | - |
| 42 | type-mismatch.db | 8KB | 1 | 3 | 1 | ok* 0s | FAIL rc=1 1s | - | - | error: copying into products: ERROR: COPY from stdin failed: unable to encode "lots-of-it" into binary format for int4 (OID 23): cannot find |
| 43 | beets_library.db | 1415MB | 5 | 156 | 15 | ok* 36s | skipped (size) | skipped | - | profile-only, 1415MB |

## Summary

- verified clean: **35**
- verify FAILED: **0**
- profile/load errors or skips: **7**

Work dir: `/private/tmp/claude-501/-Users-barrettclark-Projects-sqlite2pg-exploration-sqlite2pg/e40c9cf6-6454-4360-a006-b767d1aad7cd/scratchpad/campaign-pr2` (logs + per-db verify reports)
