# Audit cycle 4 — Phase B load-test campaign results

Ran `scripts/verify-all-fixtures.sh` (2026-09-03 13:43) against the full
local set: 19 `testdata/fixtures/` (17 prior + the 2 new cycle-4 fixtures)
+ 27 `../more data/` + `beets_library.db` (profile-only, 1.4 GB).
`sqlite2pg` built from `audit-cycle-4-scaffolding` (= `main` + the plan
doc + the 2 fixtures).

## Summary

| | count |
|---|---:|
| verified clean (`sqlite2pg verify` PASS, 0 mismatches) | **38** |
| `sqlite2pg verify` FAILURES | **0** |
| profile/load errors or skips | 8 |

**No regression.** 38 clean vs cycle 3's 36 — the +2 is exactly the two
new purpose-built fixtures, both verifying clean:

| fixture | tables | result | covers |
|---|---|---|---|
| `sample-varchar-pk.sqlite` | `file_index` (varchar(80) PK), `legacy_codes` (TEXT→integer transformed PK) | **PASS**, 17 rows | #77 (`isTextTargetType`/`COLLATE "C"` on `varchar(N)`), #60 (transformed-PK order-independent comparison) |
| `sample-geojson.sqlite` | `parcels` (GeoJSON text → jsonb) | **PASS**, 4 rows | #61 (both sides canonicalized before comparison) |

Cycle 3 had noted these three findings' fix paths never fired in the
campaign because no corpus database had the triggering column shape.
They now do, and `verify` passes — the fixes hold end to end through the
full `profile → rubber-stamp → load → verify` flow, not just the targeted
integration tests.

`employee.db` (3.92M rows) verified clean in 14 s; `rt5i.db` (1.17M rows)
in 4 s. No comparison-engine change in PRs #124–#138 false-fails any real
database.

## The 8 non-passing — identical to cycle 3, all accounted for

| database | outcome | classification |
|---|---|---|
| `atomic_database.db` | load FAIL, `"K"` → int4 | rubber-stamp casualty (script force-accepts a `needs_review` column) |
| `DisabilityCompByCounty.db` | load FAIL, `"Unknown"` → int4 | rubber-stamp casualty — the post-#69 deterministic `FIPS code` flag |
| `sample-type-mismatch.sqlite` / `type-mismatch.db` | load FAIL, `"lots-of-it"` → int4 | rubber-stamp casualty (issue #16 fixture) |
| `demo01.db` / `sqliterepo.db` | load FAIL, `"1"` → bytea | rubber-stamp casualty |
| `ssb-small.db` | load FAIL, FK constraint violation | known source-data FK violation (cycle-2 documented) |
| `corrupt001.db` | profile fails cleanly | intentionally-malformed fixture; `error: … database disk image is malformed`, exit 1, no panic |

No genuine new bugs. `verify-all-fixtures-results.md` was copied to the
repo root as expected (the issue #115 fix working).
