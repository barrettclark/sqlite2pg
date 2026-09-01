# Audit cycle 2 — Phase D: performance regression check

Same method as issue #55's discovery: `/usr/bin/time -p` wall clock,
`--sample-size 500`, `PATH` prefixed with `/opt/homebrew/opt/postgresql@18/bin`.
Two builds:

- **`aa1f44b`** — tip of `main` *before* PRs #57/#58/#59 (before `migrate
  verify` existed, before `311fb25` "Batch per-column full-table
  verification into one scan per table" — the fix for issue #55).
- **`main`** — tip (`63f6b34`), i.e. after all 33 reviewed commits.

Databases: the two largest local sources, `employee.db` (228 MB, 7 tables,
3.9 M rows) and `beets_library.db` (1.4 GB, 5 tables, 156 columns).

> First run of the harness had a broken timing capture (`/usr/bin/time -p`
> writes to stderr; the wrapper discarded it). Re-run with the fix; numbers
> below are from the corrected run.

## 1. `migrate profile` — `aa1f44b` vs `main`, 3 runs each

| Database | `aa1f44b` (real s) | `main` (real s) | Δ median |
|---|---|---|---|
| `employee.db` | 4.94 / 4.91 / 4.64 — median **4.91** | 4.37 / 4.24 / 4.26 — median **4.26** | **−13%** |
| `beets_library.db` | 110.9 / 186.3 / 152.1 — median **152.1** | 35.5 / 26.0 / 38.6 — median **35.5** | **−77% (≈4.3× faster)** |

**No regression — a large improvement**, concentrated exactly where issue
#55 predicted: `beets_library.db`'s 156 columns across only 5 wide tables
is the worst case for the old per-column full-table scan, and `311fb25`'s
one-scan-per-table batching collapses it. `main`'s ~35 s median is well
below even the pre-#55-regression baseline the 2026-08-31 Phase 4 perf
check recorded (~145 s). `employee.db` improves modestly (−13%).

`user`/`sys` corroborate: `beets` profile `sys` dropped from ~12–14 s
(`aa1f44b`) to ~2–3 s (`main`) — far fewer sequential scans of the 1.4 GB
file.

## 2. `migrate load` + `migrate verify` — `main` only

(`load`/`verify` don't exist at `aa1f44b`, so there's no before/after —
this is a baseline for future cycles.)

| Database | load (real s) | verify (real s) | rows |
|---|---|---|---|
| `employee.db` run 1 | 12.08 | 4.77 | 3,919,026 |
| `employee.db` run 2 | 12.08 | 4.73 | 3,919,026 |
| `beets_library.db` | *not cleanly measured — see below* | | |

`employee.db`: load and verify are both stable across runs. Verify costs
~39% of load wall-clock for a full exhaustive row/column re-read — in
proportion for what it does (a second full scan of both sides).

**`beets_library.db` load/verify could not be measured** on this pass:
`migrate profile` correctly flags 16 columns as `needs_review` (its
full-table check finds real non-UUID values like `"811171"` in columns
whose 500-row sample was all canonical UUIDs — issues #15/#16/#22/#31
working as intended), and the perf harness blindly marks every column
reviewed, so `migrate load` crashes at COPY (`albums.mb_albumid:
uuid_format: "811171": cannot parse UUID`). This is the same
rubber-stamp-casualty pattern Phase B documented, not a perf or
correctness regression. A clean beets load/verify timing needs a
hand-resolved config (override the 16 flagged `uuid`/`uuid[]` columns to
`text`); deferred as a measurement gap, not a finding.

## 3. Assessment

The core Phase D question — did this cycle's 33 commits regress
performance — is answered: **no. Profile is substantially faster** (issue
#55's regression is gone and then some), and `migrate verify` at real
scale is cheap (`employee.db`: 3.9 M rows in <5 s). No new issue filed.

Standing item for the plan template: re-run this before/after every
future cycle. When #69's fix lands (it adds a full-table type-fit scan
for no-transform numeric passthrough), re-check `profile` timing against
this `main` baseline specifically.
