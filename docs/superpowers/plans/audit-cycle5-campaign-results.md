# Audit cycle 5 — Phase B load-test campaign results

Ran `scripts/verify-all-fixtures.sh` (2026-09-03) against the full local
set: 19 `testdata/fixtures/` + 27 `../more data/` + `beets_library.db`
(profile-only, 1.4 GB). `sqlite2pg` built from `audit-cycle-5-scaffolding`
(= `main` at `v0.3.1` + the plan doc). Ran concurrently with the Phase C
fuzz bursts, so wall-clock timings are noisier than usual — Phase D
re-measures on a quiet machine.

## Summary

| | count |
|---|---:|
| verified clean (`sqlite2pg verify` PASS, 0 mismatches) | **38** |
| `sqlite2pg verify` FAILURES | **0** |
| profile/load errors or skips | 8 |

**No regression.** Identical to cycle 4: 38 clean, 0 verify failures. The
two cycle-4 purpose-built fixtures still pass end to end:

| fixture | result | covers |
|---|---|---|
| `sample-varchar-pk.sqlite` | **PASS**, 17 rows | #77 (`varchar(N)` PK → `COLLATE "C"`), #60 (transformed-PK order-independent compare) |
| `sample-geojson.sqlite` | **PASS**, 4 rows | #61 (both sides canonicalized before compare) |

`employee.db` (3.92 M rows) and `rt5i.db` (1.17 M rows) both verified
clean. No comparison-engine change in PRs #150–#154 false-fails any real
database — the #148 `normalizeNarrowNumeric` normalization and the #140
`fitsTemporalRange` check are transparent to every corpus DB.

## The 8 non-passing — identical to cycle 4, all accounted for

| database | outcome | classification |
|---|---|---|
| `atomic_database.db` | load FAIL, `"K"` → int4 | rubber-stamp casualty (script force-accepts a `needs_review` column) |
| `DisabilityCompByCounty.db` | load FAIL, `"Unknown"` → int4 | rubber-stamp casualty (post-#69 deterministic `FIPS code` flag) |
| `sample-type-mismatch.sqlite` / `type-mismatch.db` | load FAIL, `"lots-of-it"` → int4 | rubber-stamp casualty (issue #16 fixture) |
| `demo01.db` / `sqliterepo.db` | load FAIL, `"1"` → bytea | rubber-stamp casualty |
| `ssb-small.db` | load FAIL, FK constraint violation on `lineorder` | known source-data FK violation (cycle-2 documented) |
| `corrupt001.db` | profile crashes cleanly | intentionally-malformed fixture; `error: … database disk image is malformed`, exit 1, no panic |

No genuine new bugs. `verify-all-fixtures-results.md` copied to the repo
root as expected.
