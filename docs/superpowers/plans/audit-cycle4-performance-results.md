# Audit cycle 4 — Phase D performance regression results

`profile` timed at `db35d39` (the `migrate`-named binary, pre-PRs
#124–#138) vs `main` tip (`sqlite2pg` built from
`audit-cycle-4-scaffolding` = `main` + plan doc + fixtures), on
`employee.db` (228 MB) and `beets_library.db` (1.4 GB). `load` + `verify`
timed on `main` tip only, `employee.db` (3.92 M rows), 2 runs. Wall-clock
via `time.time()`, whole command. Same host, immediately after Phase C.

## `profile`

| database | db35d39 | main tip | median Δ |
|---|---|---|---|
| `employee.db` (3 runs) | 5654 / 5851 / 5471 ms | 5765 / 5614 / 5230 ms | **−0.7 %** (5654 → 5614 ms) — flat |
| `beets_library.db` (4 runs, interleaved, warm cache) | 5091 / 5708 / 4388 / 4141 ms | 5949 / 5169 / 4726 / 4557 ms | **+4 %** (≈ 4740 → 4950 ms, ≈ +200 ms) — within noise |

**`employee.db`: no further regression.** Cycle 3 accepted a +11 %
`profile` regression on this DB (`4710 → 5248 ms` at `b2b7561`),
attributed to the #69/#84 no-transform full-table fit check and
`9cfec0c`'s VARCHAR-widening scan. This cycle's db35d39 baseline itself
measured higher (5654 ms vs cycle 3's 4710 for the same commit — host /
cache variance between sessions), and `main` tip is level with it. The
PRs #124–#138 diff adds no measurable `profile` cost. The absolute number
is worth a cleaner cross-session re-measure someday, but the
cycle-over-cycle delta is zero.

**`beets_library.db`: not a clean comparison the first time; re-measured.**
The initial 3-runs-each pass (part of `run-perf-c4.sh`) showed db35d39 at
12 / 23 / 42 s *monotonically increasing* and `main` at 55–65 s — a pure
environmental artifact (cold 1.4 GB file cache plus memory pressure from
profiling it six times back-to-back right after the fuzz burst), not a
code effect. An interleaved warm-cache A/B (4× OLD, 4× NEW alternating)
settled it: both binaries land at ~4.7–5.0 s, NEW ~200 ms slower at the
median, comfortably inside run-to-run spread. No regression.

## `load` + `verify` (`main` tip, `employee.db`, 3.92 M rows)

| run | load | verify |
|---|---|---|
| 1 | 15794 ms | 15249 ms |
| 2 | 13823 ms | 13232 ms |

`load` median ~14.8 s, `verify` median ~14.2 s. Consistent with cycle 3
(`load` 10–13 s, `verify` 14 s). The #128 FK DDL change (DROP + ADD as
two subcommands per FK, `CREATE INDEX IF NOT EXISTS`), the #131
`varcharFinding` change, and the `errWriter` wrap on `verify --out` add
nothing measurable — `employee.db` has no foreign keys and no
`varchar(N)` columns to exercise the first two, and the `errWriter` is
one branch per `fmt.Fprint`. `verify` at 3.9 M rows in ~14 s clean is
unchanged.

## Verdict

No performance regression of the #55 class, and no growth on cycle 3's
one accepted `profile` slowdown. `main` tip is level with `db35d39` on
both databases for `profile`, and `load`/`verify` are unchanged. Nothing
to file.
