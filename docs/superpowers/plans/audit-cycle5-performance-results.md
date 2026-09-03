# Audit cycle 5 — Phase D performance regression results

`profile` timed at `db35d39` (the `migrate`-named binary) vs `main` tip
(`sqlite2pg` built from `audit-cycle-5-scaffolding` = `v0.3.1` + plan
doc), on `employee.db` (228 MB) and `beets_library.db` (1.4 GB),
interleaved OLD/NEW warm-cache, 3 runs each. `load` + `verify` on `main`
tip only, `employee.db` (3.92 M rows), 2 runs. Plus a `load --resume`
timing on `chinook.db` for finding L6. Wall-clock via `time.time()`, whole
command, quiet machine (Phases B/C already finished).

## `profile`

| database | db35d39 | main tip | median Δ |
|---|---|---|---|
| `employee.db` (3 runs) | 4756 / 4731 / 4691 ms | 5232 / 5209 / 5215 ms | **+10 %** (4731 → 5215 ms, ≈ +484 ms) |
| `beets_library.db` (3 runs) | 4545 / 4741 / 4366 ms | 4373 / 4338 / 5436 ms | flat (4545 → 4373 ms, within noise) |

**`employee.db`: unchanged from cycle 3's accepted regression.** Cycle 3
recorded +11 % on this DB, attributed to the #69/#84 no-transform
full-table fit check and `9cfec0c`'s VARCHAR-widening scan. This cycle
measures +10 % against the same `db35d39` baseline — the same regression,
not a new one. The `d0cb219..HEAD` diff touches no `profile`-path code
(`fitsTemporalRange` only runs during transform *verification*, and only
for a `time.Time`; `employee.db` has no temporal transforms). No new
cost.

## `load` + `verify` (`main` tip, `employee.db`, 3.92 M rows)

| run | load | verify |
|---|---|---|
| 1 | 10458 ms | 13137 ms |
| 2 | 10842 ms | 12859 ms |

`load` median ~10.6 s, `verify` median ~13.0 s — consistent with cycles 3
and 4. The #148 `normalizeNarrowNumeric` type switch, added at the top of
every `valuesMatch` / `sortKeyFor` call on the `verify` hot path, adds
**nothing measurable** across 3.9 M rows × ~28 columns. That was the key
thing this cycle needed to confirm.

## L6 — `load --resume` FK re-validation (`chinook.db`, ~13 FK constraints)

| step | time |
|---|---|
| initial `load` | 336 ms |
| `load --resume` #1 | 45 ms |
| `load --resume` #2 | 41 ms |
| `load --resume` #3 | 52 ms |

On `chinook` (11 tables, few thousand rows) the now-unconditional FK step
on a `--resume` costs **~45 ms total** — negligible. The cost is real but
scales with child-table row count: Phase A's direct Postgres-18
measurement on a 200 000-row child table put a single
`DROP CONSTRAINT IF EXISTS … , ADD` re-validation at ~2.2 s (vs ~1.0 s for
the first application), all FKs held under `ACCESS EXCLUSIVE` in one
transaction. So a `--resume` against a fully-loaded large FK-heavy
database (a `beets`-scale schema with real foreign keys) now does
seconds-to-minutes of constraint re-validation and lock-holding that the
old `FKsApplied` gate skipped. Invisible here; a genuine regression at
scale. Recorded as L6 — the #142 fix itself is correct; the tradeoff is
lock footprint and repeated validation work on the resume path.

## Verdict

No performance regression from the `d0cb219..HEAD` diff on the standard
`profile` / `load` / `verify` paths. The one cycle-3 `profile` regression
on `employee.db` has not grown. L6 (`--resume` FK re-validation cost at
scale) is the only perf-relevant finding and is filed as a Low.
