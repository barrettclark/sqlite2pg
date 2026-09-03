# Audit cycle 3 — Phase D performance regression results

`migrate profile` timed at `db35d39` (pre-PRs #95–#102) vs `main` tip
(`b2b7561`), 3 runs each, on `employee.db` (228 MB) and `beets_library.db`
(1.4 GB). `load` + `verify` timed on `main` tip only, `employee.db`, 2 runs.
Machine: same host, run immediately after the Phase B campaign (page cache
warm for both binaries). Wall-clock via `time.time()`, whole command.

## `profile`

| database | db35d39 (3 runs) | main tip (3 runs) | median Δ |
|---|---|---|---|
| `employee.db` | 4751 / 4690 / 4710 ms | 5720 / 5196 / 5248 ms | **+11 %** (4710 → 5248 ms, ≈ +540 ms) |
| `beets_library.db` | 18835 / 13505 / 8780 ms | 6027 / 4938 / 6393 ms | −55 % (13505 → 6027 ms) — **see caveat** |

**`employee.db`: a small, real regression.** ~11 % / half a second on a
228 MB database. The cause is expected and was judged correctly implemented
in Phase A's clean bill: `9cfec0c` adds one `MaxTextLengths` scan per
table whose VARCHAR lengths vary, and the #69/#84 fix adds a full-table
type-fit scan for no-transform passthrough columns targeting a concrete
numeric type. Both are one extra pass per *qualifying* table, not
per-column — not the O(columns × rows) shape of #55. This is a deliberate
correctness-for-latency trade (it is what makes `DisabilityCompByCounty`'s
`FIPS code` deterministic). Worth a cleaner re-measure or an explicit
"accept" in triage; it is not a bug.

**`beets_library.db`: not a clean comparison.** The `db35d39` runs show
huge variance (18.8 s cold → 8.8 s warm) because they ran first, before the
filesystem cache was fully warm for the 1.4 GB file; the `main` runs all
executed against a hot cache. The honest read is "no catastrophic
regression on the large database" (nothing near #55's 152 s), not a real
2× speedup. A cold-cache A/B rerun would settle it if the number matters.

## `load` + `verify` (`main` tip, `employee.db`, 3.92M rows)

| run | load | verify |
|---|---|---|
| 1 | 10641 ms | 14018 ms |
| 2 | 12591 ms | 14205 ms |

Consistent with the Phase B campaign (`load` 10 s, `verify` 34 s under
concurrent campaign load). `verify` at 3.9M rows in ~14 s clean is cheap —
no regression from the batched full-table verification (`311fb25`, cycle 2)
or the #83/#84/#61 comparison changes.

## Verdict

No performance regression of the #55 class. One minor, explained
`profile` slowdown on `employee.db` (+540 ms / +11 %), attributable to two
intentionally-added correctness scans, each properly batched to one pass
per qualifying table. Recommend: accept, or re-measure cold-cache if a
tighter number is wanted.
