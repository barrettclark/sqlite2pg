# PR 1 (verify fixes #60/#61/#62/#63/#65/#66/#67) — regression campaign

Full `scripts/verify-all-fixtures.sh` run against the PR 1 build.

**37 verified clean, 0 verify FAILURES.** None of PR 1's seven changes to
`verify_load.go` / the verify wiring newly false-fails any real database —
the jsonb, timestamp, PK-transform and cross-type-numeric comparison
changes all hold across the full local set.

Notes on the deltas from Phase B's baseline (35 clean / 7 non-passing):

- **`DisabilityCompByCounty.db` (row 5, the real 192 KB fixture)** loaded
  and verified clean this run where Phase B had it as a load failure —
  pure sample variance (the ~16% of profile runs whose random 500-row
  sample includes the single `"Unknown"` row, so `sentinel_null` flags
  `FIPS code` and the script accepts it). Issue #69 (PR 2) is the real fix
  for the ~84% of runs where the sample misses it.
- **Row 22, `DisabilityCompByCounty.db` at 0 KB**, is the stray zero-byte
  file that appeared in `../more data/` during this session (flagged in
  `audit-cycle2-campaign-results.md`). It contributes nothing (0 tables)
  and should be deleted.
- The 6 non-passing loads are the same rubber-stamp casualties Phase B
  documented (`atomic_database`, `sample-type-mismatch`, `type-mismatch`,
  `demo01`, `sqliterepo` — script force-accepts a correctly-flagged
  `needs_review` column) plus `ssb-small`'s known source-data FK
  violation. No tool regression.

Scale: `employee.db` 3.9M rows load 13s / verify 14s; `rt5i.db` 1.17M rows
verify 4s.

---

PG_URL: `postgres://localhost:5432/?sslmode=disable`  |  sample-size: 500  |  profile-only over: 1200MB

| # | database | size | tables | cols | need-review | profile | load | verify | rows cmp | notes |
|---|---|---:|---:|---:|---:|---|---|---|---:|---|
| 1 | atomic_database.db | 7MB | 12 | 58 | 2 | ok* 1s | FAIL rc=1 0s | - | - | error: copying into XRAY_ENERGIES: ERROR: COPY from stdin failed: unable to encode "K" into binary format for int4 (OID 23): cannot find enc |
| 2 | AustinRoadConstruction.db | 4MB | 1 | 26 | 6 | ok* 0s | ok 0s | PASS 0s | 3538 | - |
| 3 | bikes.db | 284KB | 1 | 14 | 7 | ok* 0s | ok 0s | PASS 0s | 2509 | - |
| 4 | chinook.db | 864KB | 11 | 64 | 1 | ok* 0s | ok 0s | PASS 0s | 15607 | - |
| 5 | DisabilityCompByCounty.db | 192KB | 1 | 14 | 1 | ok* 0s | ok 1s | PASS 0s | 3148 | - |
| 6 | sakila.db | 5MB | 16 | 89 | 17 | ok* 0s | ok 0s | PASS 0s | 46273 | - |
| 7 | northwind_small.sqlite | 284KB | 13 | 89 | 1 | ok* 0s | ok 1s | PASS 0s | 3308 | - |
| 8 | sample-dates.sqlite | 8KB | 1 | 11 | 3 | ok* 0s | ok 0s | PASS 0s | 5 | - |
| 9 | sample-implicit-fk.sqlite | 12KB | 2 | 4 | 0 | ok 0s | ok 0s | PASS 0s | 6 | - |
| 10 | sample-large.sqlite | 1MB | 1 | 4 | 1 | ok* 0s | ok 1s | PASS 0s | 100000 | - |
| 11 | sample-numeric-text.sqlite | 8KB | 1 | 5 | 0 | ok 0s | ok 0s | PASS 0s | 5 | - |
| 12 | sample-type-mismatch.sqlite | 8KB | 1 | 3 | 1 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into products: ERROR: COPY from stdin failed: unable to encode "lots-of-it" into binary format for int4 (OID 23): cannot find |
| 13 | sample-types.sqlite | 8KB | 1 | 6 | 0 | ok 0s | ok 1s | PASS 0s | 3 | - |
| 14 | sample-uuids.sqlite | 8KB | 1 | 3 | 0 | ok 0s | ok 0s | PASS 0s | 3 | - |
| 15 | sample-varchar.sqlite | 12KB | 2 | 6 | 2 | ok* 0s | ok 0s | PASS 0s | 5 | - |
| 16 | NTAD_Aviation_Facilities_698356094499483505.geodatabase | 8MB | 1 | 92 | 0 | ok 0s | ok 1s | PASS 0s | 19426 | - |
| 17 | SchoolSites2425_-4255819620268625087.geodatabase | 7MB | 1 | 97 | 0 | ok 0s | ok 0s | PASS 1s | 9982 | - |
| 18 | bigendianwal.db | 4KB | 0 | 0 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 19 | collision.db | 8KB | 1 | 4 | 0 | ok 0s | ok 0s | PASS 0s | 1 | - |
| 20 | companies.db | 7MB | 1 | 10 | 0 | ok 0s | ok 0s | PASS 1s | 55991 | - |
| 21 | demo01.db | 504KB | 31 | 140 | 7 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into config: ERROR: COPY from stdin failed: unable to encode "1" into binary format for bytea (OID 17): cannot find encode pl |
| 22 | DisabilityCompByCounty.db | 0KB | 0 | 0 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 23 | dow-health-taxis.db | 996KB | 3 | 20 | 0 | ok 0s | ok 0s | PASS 0s | 7356 | - |
| 24 | employee.db | 228MB | 7 | 28 | 2 | ok* 5s | ok 13s | PASS 14s | 3919026 | - |
| 25 | iris.db | 16KB | 1 | 5 | 0 | ok 0s | ok 1s | PASS 0s | 150 | - |
| 26 | iso10383-mic.db | 452KB | 1 | 17 | 0 | ok 0s | ok 0s | PASS 0s | 2662 | - |
| 27 | kjvbible-u16be.db | 8MB | 3 | 8 | 0 | ok 0s | ok 0s | PASS 0s | 31168 | - |
| 28 | kjvbible-u8.db | 4MB | 3 | 8 | 0 | ok 0s | ok 0s | PASS 0s | 31168 | - |
| 29 | littleendianwal.db | 4KB | 0 | 0 | 0 | ok 0s | ok 1s | PASS 0s | 0 | - |
| 30 | manyblobs-4k.db | 24MB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 4102 | - |
| 31 | multilinetext.db | 632KB | 2 | 6 | 0 | ok 0s | ok 0s | PASS 0s | 8 | - |
| 32 | neh-grants.db | 9MB | 1 | 33 | 2 | ok* 0s | ok 1s | PASS 0s | 9402 | - |
| 33 | random-json.db | 53MB | 2 | 5 | 0 | ok 0s | ok 2s | PASS 0s | 3844 | - |
| 34 | rt5i.db | 44MB | 5 | 21 | 0 | ok 1s | ok 3s | PASS 4s | 1166436 | - |
| 35 | sample.db | 16KB | 2 | 6 | 0 | ok 0s | ok 1s | PASS 0s | 10 | - |
| 36 | sqliterepo.db | 105MB | 36 | 158 | 3 | ok* 0s | FAIL rc=1 1s | - | - | error: copying into config: ERROR: COPY from stdin failed: unable to encode "1" into binary format for bytea (OID 17): cannot find encode pl |
| 37 | ssb-small.db | 4MB | 5 | 58 | 6 | ok* 1s | FAIL rc=1 0s | - | - | error: adding foreign key: ERROR: insert or update on table "lineorder" violates foreign key constraint "fk_lineorder_lo_commitdate" (SQLSTA |
| 38 | sunspots.db | 16KB | 1 | 2 | 0 | ok 0s | ok 0s | PASS 0s | 313 | - |
| 39 | superheroes.db | 1MB | 1 | 7 | 0 | ok 0s | ok 0s | PASS 0s | 6895 | - |
| 40 | test_pk.db | 12KB | 2 | 3 | 0 | ok 0s | ok 0s | PASS 0s | 0 | - |
| 41 | titanic.db | 72KB | 1 | 12 | 2 | ok* 0s | ok 0s | PASS 0s | 891 | - |
| 42 | TPC-H-small.db | 10MB | 8 | 61 | 1 | ok* 0s | ok 1s | PASS 0s | 86805 | - |
| 43 | type-mismatch.db | 8KB | 1 | 3 | 1 | ok* 0s | FAIL rc=1 0s | - | - | error: copying into products: ERROR: COPY from stdin failed: unable to encode "lots-of-it" into binary format for int4 (OID 23): cannot find |
| 44 | beets_library.db | 1415MB | 5 | 156 | 16 | ok* 44s | skipped (size) | skipped | - | profile-only, 1415MB |

## Summary

- verified clean: **37**
- verify FAILED: **0**
- profile/load errors or skips: **6**

Work dir: `/private/tmp/claude-501/-Users-barrettclark-Projects-sqlite2pg-exploration-sqlite2pg/e40c9cf6-6454-4360-a006-b767d1aad7cd/scratchpad/campaign-pr1` (logs + per-db verify reports)
