# Audit cycle 3 — Phase B load-test campaign results

Ran `scripts/verify-all-fixtures.sh` (2026-09-02 21:18) against the full
local set: 17 `testdata/fixtures/` + 27 `../more data/` + `beets_library.db`
(1.4 GB, profile-only over the 1200 MB gate). `migrate` built from
`worktree-audit-cycle-3` (= `main` tip `b2b7561` + the cycle-3 plan doc).

Env: `PG_URL=postgres://localhost:5432/?sslmode=disable`, sample-size 500.

## Summary

| | count |
|---|---:|
| verified clean (`migrate verify` PASS, 0 mismatches) | **36** |
| `migrate verify` FAILURES | **0** |
| profile/load errors or skips | 8 |

**No regressions.** Every database that loaded also verified clean, with
zero row/value mismatches anywhere — including `employee.db` (3.92M rows
verified in 34 s) and `rt5i.db` (1.17M rows, 19 s). The comparison-engine
changes in PRs #95–#101 (`isTextTargetType`/`varchar(N)`, the `[]byte`
text-column fix #83, the numeric quartet) do not false-fail any real
database in the corpus.

## New databases this cycle

Pulled from `https://sqlite.org/test-dbs` into `../more data/` (not checked
in). Most of the sqlite.org set was already present from a prior cycle;
genuinely new:

| database | size | result | note |
|---|---:|---|---|
| `manyblobs-512.db` | 17 MB | **PASS** (4102 rows) | 512-byte page size, `blob(n INTEGER PK, x BLOB)` + index on the blob; BLOB volume / small-page stress |
| `corrupt001.db` | 9 KB | **fails cleanly** | intentionally malformed; `migrate profile` exits 1 with `error: reading schema: listing tables: database disk image is malformed` — no panic, no stack trace. Graceful-degradation behavior confirmed. |

Already-present sqlite.org databases re-confirmed clean this cycle:
`random-json.db` (53 MB, jsonb path, PASS 3844 rows), `kjvbible-u8.db` /
`kjvbible-u16be.db` (UTF-8 / UTF-16BE text, PASS 31168 rows each),
`manyblobs-4k.db` (PASS), `multilinetext.db` (embedded newlines through
COPY, PASS), `TPC-H-small.db` (8 tables, DECIMAL money columns, PASS 86805
rows), `bigendianwal.db` / `littleendianwal.db` (PASS, 0 rows).

## The 8 non-passing — all accounted for, 0 genuine new bugs

| # | database | outcome | classification |
|---|---|---|---|
| 1 | `atomic_database.db` | load FAIL — `"K"` → int4 | **review-gate casualty** (cycle-2 documented): the script rubber-stamps every column, force-accepting a correctly-flagged `needs_review` column. 2 need-review cols. |
| 5 | `DisabilityCompByCounty.db` | load FAIL — `"Unknown"` → int4 | **review-gate casualty, post-#69**: the `FIPS code` column the cycle-2 fix for #69 made *deterministically* flag as `needs_review` (was ~84 % non-deterministic before). The rubber-stamp script force-accepts the flag and COPY then fails — this is the fix working. 1 need-review col. |
| 12 | `sample-type-mismatch.sqlite` | load FAIL — `"lots-of-it"` → int4 | **review-gate casualty**: issue #16 fixture, `products.qty` correctly flagged `needs_review`. |
| 21 | `corrupt001.db` | profile fails cleanly | **new fixture, expected** — see above. |
| 22 | `demo01.db` | load FAIL — `"1"` → bytea | **review-gate casualty** (cycle-2 documented). 7 need-review cols. |
| 37 | `sqliterepo.db` | load FAIL — `"1"` → bytea | **review-gate casualty** (cycle-2 documented). 3 need-review cols. |
| 38 | `ssb-small.db` | load FAIL — FK constraint `fk_lineorder_lo_commitdate` | **known source-data FK violation** (cycle-2 documented) — the source SQLite data itself violates the FK; not a tool bug. |
| 44 | `type-mismatch.db` | load FAIL — `"lots-of-it"` → int4 | **review-gate casualty** — the external repro `sample-type-mismatch.sqlite` mirrors. |

The five `"unable to encode …"` COPY failures are the review gate doing
exactly its job: the profiler flagged the column, and the campaign script
(which simulates a human accepting every suggestion) overrode the flag.
See the `verify-all-fixtures-script` memory.

## Not exercised — purpose-built fixtures deferred

Phase A's H1 (Consequence A) and M1 both cause a whole-table false-fail /
dead-guard on column shapes no corpus database has (a non-midnight
`DATETIME` column driving `iso8601_to_timestamptz`; a `COLLATE NOCASE` PK
with an unbalanced paren in a string/comment). Same situation cycle 2 hit
with #60/#61. The two purpose-built fixtures the plan calls for
(`VARCHAR(N)` / transformed-PK; geojson/`jsonb`) were **not built in this
detection pass** — they are regression coverage for the Phase E fixes and
are best authored alongside those fixes (failing-test-first), against the
full finding set rather than guessed at now.

## Work dir

`…/scratchpad/campaign-work/` (per-db configs, logs, verify reports —
`KEEP_WORK=1` was set).
