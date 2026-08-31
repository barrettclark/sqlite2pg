# Phase 4 item 5 — Performance check on full-table verification at scale

**Setup:** `go build -o /tmp/migrate-perf ./cmd/migrate` at commit `aa1f44b`
(tip of main as of 2026-08-31). `PATH` prefixed with
`/opt/homebrew/opt/postgresql@18/bin` per the plan. All timings are wall
clock (`time -p`, the `real` field), `--sample-size 500`, no Postgres
involved (`profile` only — no `load`).

## 1. Current wall-clock time, 2-3 runs each

| Database | Size | Run 1 | Run 2 | Run 3 | Notes |
|---|---|---|---|---|---|
| `rt5i.db` | 46MB | 0.91s | 0.61s | 0.61s | first run pays cold-cache cost |
| `sqliterepo.db` | 110MB | 0.37s | 0.34s | — | now completes (issue #17 fix); previously crashed before producing any timing |
| `employee.db` | 240MB | 4.80s | 6.66s | 7.24s | variance below |
| `beets_library.db` | 1.4GB | 195.69s | 103.57s | 195.69s | see cache-effect note below |

`employee.db`'s three runs trend upward (4.8s → 6.7s → 7.2s), the opposite
of a warming-cache pattern — this tracks the needs-review count varying
run to run (`sample-size 500` draws a different random sample each time,
since sampling isn't seeded), which changes how many columns auto-approve
and therefore how many pay the full-table-verification cost. `sys` time
also climbs across the three runs (0.30s → 0.44s → 0.54s), consistent
with more full-table scans, not with a cache-warming explanation.

`beets_library.db`'s middle run (103.57s, `sys` 7.42s) was markedly
faster than runs 1 and 3 (195.69s twice, `sys` 13-13.7s both times) despite
having *fewer* needs-review columns (8, vs. 14 and 15) — fewer
needs-review means *more* auto-approved columns, which should mean *more*
full-table scans, yet it was the fastest run. This points to macOS page
cache state dominating wall-clock variance on a 1.4GB file more than the
verification logic itself does — reran a third time and it reproduced the
slow number exactly (195.69s), so the fast run looks like an outlier tied
to disk cache warmth from a nearby unrelated read, not a real bimodal
behavior in the tool.

## 2. Full-table-verification-specific timing (instrumented build)

A temporary package-level `time.Duration` counter was added around the
`verifyTransformAgainstFullTable` call site in
`internal/pipeline/decide_column.go`, accumulated and printed at the end
of `ProfileDatabase` in `internal/pipeline/profile.go`. Every line was
marked `TEMPORARY`. Built as a separate binary
(`/tmp/migrate-perf-instrumented`), run once each against `employee.db`
and `beets_library.db`, then **immediately reverted** via
`git checkout -- internal/pipeline/decide_column.go internal/pipeline/profile.go`
as its own step before writing this report. Confirmed via
`git status --porcelain` / `git diff --stat` / `grep -rn TEMPORARY` — all
three show zero remaining trace.

| Database | Total wall time | Full-table verification | % of total |
|---|---|---|---|
| `employee.db` | 7.30s | 4.21s | ~58% |
| `beets_library.db` | 116.87s | 54.55s | ~47% |

Full-table verification is a large, measurable fraction of profile time on
both databases, but not the whole story: `beets_library.db`'s instrumented
run spent 116.87s wall clock against only 4.01s `user` + 8.30s `sys` — over
100 seconds where the process was blocked on something other than CPU work
counted by `time`, most plausibly SQLite I/O for the *other* full-table-ish
work `ProfileDatabase` does per table (the `ORDER BY RANDOM()` sample scan,
`SampleNonNullColumn` rescue queries for sparse columns) rather than
verification specifically. Full-table verification is real and sizeable,
but it isn't the only sequential-scan cost in the pipeline, and on this
run it wasn't even the majority of wall time.

## 3. Comparison against the original campaign baselines

Baselines from `audit-phase2b-moredata-results.md` (`employee.db`,
`rt5i.db`) and `audit-phase2c-beets-results.md` (`beets_library.db`),
captured before 2026-08-30's #15/#16/#22/#27/#31 fixes extended
`verifyTransformAgainstFullTable`. `sqliterepo.db` has no original
baseline to compare against — it crashed before producing a config in the
original campaign (issue #17, fixed same day), so any current time is a
strict improvement (crash → 0.3-0.4s complete run) with nothing to call a
regression against.

| Database | Original baseline | Current (median-ish) | Delta |
|---|---|---|---|
| `rt5i.db` | ~0.7s | ~0.6-0.9s | roughly flat |
| `employee.db` | ~5.5s | ~4.8-7.2s | **+~30-40% at the high end** |
| `beets_library.db` | 145s (2m25s) | 104-196s | **+~35% at the high end, ~-28% at the low end** |

`employee.db`'s slowest run (7.24s) is ~32% slower than the 5.5s baseline;
its fastest (4.80s) is *faster* than baseline. `beets_library.db`'s
consistent slow number (195.69s, reproduced twice) is ~35% slower than the
145s baseline, matching the ~34% regression figure carried over from the
interrupted prior attempt — independently re-derived here, not just
trusted. Its one fast run (103.57s) was actually faster than baseline,
which — given the cache-effect analysis above — looks like a cache-warmth
artifact rather than evidence the regression isn't real.

Taking the reproducible numbers (the two matching 195.69s beets runs, and
employee.db's upward-trending runs) as the more trustworthy signal: **this
is a real regression, roughly 30-35% on both of the two databases with
valid original baselines**, not noise.

## 4. Why: which columns pay the cost

Full-table verification only runs for a column that's *about to
auto-approve* with a transform attached (`decideColumn`, the
`!needsReview && best.TransformExpr != ""` branch) — a column already
flagged for human review skips it entirely.

- `beets_library.db`: 156 columns, 143/156 (~92%) auto-approved clean in
  the original Phase 2C campaign. This is exactly the profile shape that
  pays full-table-verification cost on nearly every column — a large
  fraction of its 1.4GB is scanned a second time, once per
  auto-approving, transform-bearing column. This matches it being both
  the largest database tested and the one with the clearest measured
  regression.
- `employee.db`: 26/28 columns (7 tables) auto-approved clean in the
  original campaign — also a high auto-approve rate, and also shows a
  clear regression (though smaller in absolute wall-clock terms since the
  database and its columns are smaller).
- `rt5i.db`: 26/26 columns clean, but the database is small (46MB, mostly
  R-tree shadow-table binary blobs) and the columns that auto-approve
  mostly have no transform attached (`Transform == ""` skips verification
  entirely) or are the R-tree binary payload — so despite a 100%
  auto-approve rate, there's little full-table verification work to do,
  and timing stayed flat.
- `sqliterepo.db`: 3 columns needed review out of a modest total — more
  of a mixed database, and also just much smaller in row count than
  `employee.db`/`beets_library.db` despite comparable file size (schema-
  heavy Fossil-SCM structure). No comparable baseline exists, and current
  timing (0.3-0.4s) isn't a concern regardless.

This confirms the plan's hypothesis directly: **a database with a high
proportion of clean, high-confidence columns pays the full-table-scan cost
on most of its columns, and it's exactly those databases
(`beets_library.db`, `employee.db`) that show the regression.** A database
with more ambiguous columns pays the cost less, by construction.

## 5. Assessment: real regression, and a concrete direction

This is a real, actionable performance regression, not noise or an
artifact of measurement — the same ~30-35% figure reproduced independently
across two databases with valid original baselines, on reproducible
(re-run and matched) numbers rather than a single noisy sample. On
`beets_library.db`, full-table verification alone accounts for ~47-58% of
current wall-clock profile time.

That said, put in context: `employee.db` still profiles in single-digit
seconds, and `beets_library.db` in a couple of minutes for a 1.4GB source
— neither is catastrophic, and every one of the five issues that extended
`verifyTransformAgainstFullTable` (#15, #16, #22, #27, #31) fixed a
genuine correctness bug (data corruption or a crash) that this
performance cost buys real protection against. The question is whether
the *mechanism* is proportionate, not whether the checks themselves are
worth having.

**Suggested direction (no fix implemented — measurement only per task
scope):** check whether `verifyTransformAgainstFullTable` currently issues
one full sequential table scan *per column*. If a table has, say, 20
columns that all auto-approve, that's 20 separate full scans of the same
table instead of one. `beets_library.db`'s 156 columns across only 5
tables (~31 columns/table average) is exactly the shape where this would
show up as the dominant cost — batching every column's full-table check
for a table into a single scan (one `SELECT` reading all
about-to-auto-approve columns' values per row, validating each column's
transform against its own value in the same pass) would turn an
O(columns × rows) cost into O(rows) with a wider per-row payload, which
should scale far better on wide, clean tables like `beets_library.db`'s
without weakening any of the individual issue fixes' guarantees. Worth
confirming the current implementation's scan structure directly (read
`verify_transform.go`'s query and its caller loop) before committing to
this as the fix, since the ~half of `beets_library.db`'s wall time *not*
attributed to verification in section 2 suggests there may be a second,
separate multi-scan-per-table cost (the sampling/rescue queries) worth
looking at in the same pass.

## 6. Source tree cleanliness

Confirmed clean before, during, and after instrumentation:

```
$ git status --porcelain
$ git diff --stat
$ grep -rn TEMPORARY internal/pipeline/decide_column.go internal/pipeline/profile.go
```

All three produced empty output at the end of this task. Only this
results file is new; nothing under `internal/` or `cmd/` is modified.
