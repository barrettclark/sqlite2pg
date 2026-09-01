# sqlite2pg

A `pgloader` replacement for migrating SQLite databases (including Esri File
Geodatabases) into PostgreSQL, with automatic data profiling and an
interactive terminal UI for reviewing ambiguous type decisions.

Sample databases the test suite exercises live in `testdata/fixtures/`
within this project, so the tests don't depend on anything outside the repo.
See `testdata/fixtures/README.md` for where they came from.

## Why

`pgloader` handles the SQLite→Postgres mechanics fine, but does no data
profiling: comma-formatted numbers, Unix epoch timestamps, 0/1 booleans,
GeoJSON text, Esri Julian Day Numbers, and Esri's custom SQLite type names
(`int32`, `realdate`, `geometryblob`, ...) all require hand-written `.load`
files and `AFTER LOAD DO` SQL, discovered by trial and error per database.
`sqlite2pg` samples the source data first, proposes a type for every column
with a confidence score, and only asks a human when it's genuinely ambiguous
(e.g. a 0/1 integer column — is it a boolean, or a numeric code that happens
to only take those two values?).

## How it works

The common case — a human at the terminal, watching it happen — is one command:

```
migrate run <source.db> --pg <postgres-url>
```

This profiles the source, then opens an in-terminal review screen: pick a
table, then see every column's real sample data and its type decision
together in one grid — select a column and press enter to change its type
from a list filtered to only the types the sampled data actually validates
as. Press `f` to finish and import — generates the DDL and streams every
table into Postgres via COPY; press `c` to cancel — nothing touches
Postgres and the draft config is deleted. Either raises a Yes/No
confirmation before doing anything irreversible.

The grid always shows the *raw* source value, not what it converts to —
by design, so review reflects exactly what's in the source rather than a
preview of the transform. This can look odd for a column heading to
`integer`: a source like `year_founded` exported from pandas (which
upcasts a whole integer column to float the moment any row is missing a
value) stores `1998.0` as text, and that's what the grid shows — but the
loaded Postgres value is the clean integer `1998`, not `1998.0`.

For scripted or staged use — profile now, review later, load in CI — the
same steps are available as three separate commands. Note that the review
step itself is interactive-only (it needs a real terminal for the TUI) and
can't be scripted or run non-interactively:

```
migrate profile  <source.db>   # sample + profile every column, write a draft config
migrate review   <config.yaml>  # open the terminal review UI to approve/override ambiguous columns
migrate load     <config.yaml> --pg <postgres-url>   # generate DDL, stream rows via COPY
migrate verify   <source.db> <config.yaml> --pg <postgres-url>   # confirm the load was correct
```

- **`run`** is `profile` + `review` + `load` collapsed into one command, with
  a Confirm/Cancel gate in the review screen controlling whether `load` runs
  at all.
  `--keep-config` keeps the generated `<source>.migration.yaml` afterward
  instead of deleting it (useful for inspecting exactly what was loaded, or
  for a later `--resume`).
  After a successful load, `run` can also run the same checks as `migrate
  verify` immediately, without a separate invocation — see **Automatic
  post-load verification** below. This works the same regardless of
  `--keep-config`.
- **`profile`** never touches Postgres. It reads the SQLite schema, samples
  rows per column, runs every registered heuristic, and writes a draft
  `*.migration.yaml` config. Columns below the confidence threshold (default
  0.9) are written to an `unresolved_report.yaml` alongside it, and `profile`
  exits non-zero pointing at it.
- **`review`** opens the terminal review UI directly in your current
  session and blocks until you finish or cancel. Every approve/override
  commits straight through to the config file on disk — quitting the
  terminal never loses progress made before that point. (For standalone
  `review`, finishing just ends the review and unblocks the CLI — the
  actual load is a separate `migrate load` step; only `run` loads
  immediately after.)
- **`load`** refuses to run if unreviewed columns remain above the confidence
  gate (override with `--force`), or if the source file has changed since the
  config was generated (schema drift, detected via a SHA-256 hash). Use
  `--dry-run` to print the generated DDL without connecting to Postgres, or
  `--resume` to skip tables a prior run already completed (tracked in
  `<config>.state.json`).
  After a successful load, `load` can also run the same checks as `migrate
  verify` immediately, without a separate invocation — see **Automatic
  post-load verification** below.
- **`resolve --apply resolutions.yaml`** merges human- (or Claude Code-)
  supplied answers for an `unresolved_report.yaml` back into the config, for
  cases no heuristic could confidently resolve on its own.
- **`verify <source.db> <config.yaml> --pg <postgres-url>`** streams every
  row and every included column from *both* sides and confirms the Postgres
  copy is byte-for-byte correct — not a spot check. It reads the database
  name to connect to from `<config>.state.json` (the same file `--resume`
  uses), so it always needs a completed `migrate load` (or `load --resume`)
  against that exact config first. Run it after every load as the real
  pass/fail gate on data integrity — exit code is non-zero on any mismatch.
  `--out <path>` writes the detailed report to a file instead of stdout; a
  clean run reports 0 mismatches, a dirty one lists every mismatching
  column with up to 20 examples plus the true total count even when
  capped. How precisely those examples correspond to source rows depends
  on whether the table has a primary key:
    - **With a primary key**, both SQLite and Postgres are read genuinely
      `ORDER BY <primary key>` — not a bare sequential scan on either side
      — so the comparison is a real, deterministic row-by-row match. This
      closes a gap found during this feature's own development: without an
      explicit `ORDER BY`, Postgres 18 was observed, directly and
      reproducibly (on real fixtures — `bikes.db`, `chinook.db`'s `tracks`
      table), not to reliably return a freshly-COPY'd, entirely untouched
      table's rows in insertion order on a plain sequential scan — which
      could make `verify` report a mismatch that was really just
      scan-order drift, not corrupted data. Ordering both sides by the
      primary key sidesteps that risk entirely (as does a table modified
      since its load, e.g. by an `UPDATE`, which rewrites a row as a new
      physical tuple not guaranteed to land back in its old scan position —
      the same fix covers that case too). For a text-typed (or partially
      text) primary key, "genuinely `ORDER BY <primary key>`" specifically
      means byte-order on both sides: SQLite's default text comparison is
      `BINARY`, while a bare Postgres `ORDER BY` uses whatever collation
      the target database happens to be configured with (e.g.
      `en_US.UTF-8`, locale-aware) — these routinely disagree (`"Makefile.in"`
      sorts before `"aclocal.m4"` under `BINARY` but after it under
      `en_US.UTF-8`), which would otherwise silently misalign the two
      sides' row order and produce false-positive mismatches despite the
      data being identical (found during a validation campaign against
      `sqliterepo.db`'s `vcache` table — 1,424 of 1,525 rows falsely
      flagged). `verify` closes this by appending `COLLATE "C"` (Postgres's
      byte-order collation) to every text-typed primary-key column in its
      `ORDER BY`. Each reported example names the exact row (source value,
      expected transformed value, actual Postgres value).
    - **Without a primary key**, there's no column set to order by that's
      both guaranteed unique and safe to trust for this — so `verify`
      instead compares each included column as a value *multiset*: every
      value from both sides is collected and sorted into the same
      canonical order, then compared position by position in that sorted
      order. This still exhaustively catches any genuine value difference
      with no scan-order false positives, but a reported example is a
      position in the *sorted comparison*, not a source row — the report
      says so explicitly rather than implying row-position precision it
      doesn't have. This path also holds a full column's worth of values
      from both sides in memory to sort them, more expensive than the
      primary-key path's streaming comparison — an accepted tradeoff, paid
      only by tables that lack a primary key.
  See the doc comment on `internal/pipeline.VerifyTable` for the full
  detail on both paths.

### Automatic post-load verification

`run` and `load` both accept two mutually exclusive flags controlling
whether verification (the same engine and report `migrate verify` uses)
runs automatically right after a successful load, without a separate
`migrate verify` invocation:

- **`--verify`** — run verification unconditionally after a successful
  load, no prompt.
- **`--noverify`** — never run verification after a successful load, no
  prompt.
- **Neither flag** (the default) — after the load finishes, prompt
  interactively: `Run migrate verify now? [y/N]:`. Only an explicit `y` or
  `yes` (case-insensitive) runs it; anything else, including a bare Enter,
  skips it. This defaults to *not* verifying because verification re-streams
  every row of the data just loaded and can take a while on a large import —
  a bare Enter shouldn't silently commit you to that.
- Passing both `--verify` and `--noverify` together is a usage error.

Verification runs against the in-memory config and Postgres connection the
load itself just used — never by re-reading `<config>.migration.yaml` or
`<config>.state.json` from disk — so for `run` it works correctly whether
or not `--keep-config` was passed, even though `run` deletes both files
right after a successful load unless `--keep-config` is given.

The load itself has already succeeded and the data is already in Postgres
by the time verification runs, so a verification mismatch found here is
reported as a distinct, serious finding — not treated as a failed import.
`run`/`load` print the mismatch report clearly (making explicit that the
*load* succeeded but *verification* found a problem) and exit non-zero,
mirroring standalone `migrate verify`'s own exit-code convention, so a
script driving `run --verify`/`load --verify` can detect it.

## Extending the profiler

Heuristics live one-per-file in `internal/profiler/heuristics/`, implementing:

```go
type Heuristic interface {
    Name() string
    AppliesTo(meta ColumnMeta) bool
    Evaluate(meta ColumnMeta, samples []Value) (Finding, bool)
}
```

Each self-registers via `init()`, so adding a new case — `sentinel_null` and
`yyyymmdd_date` were both added this way, after a golden-fixture test or
real-world dogfooding caught a gap — never requires touching an existing
heuristic. Every heuristic has a same-named `_test.go` with table-driven
unit tests and no I/O.

## Known limitation: sampling can miss rare rows

Type decisions start from a bounded sample (`--sample-size`, default 500),
not a full table scan. A rare edge-case value that falls outside the sample
(e.g. a single aggregate/catch-all row) can look fine at profiling time —
but before any such decision is auto-approved, it's re-verified by running
the real transform against *every* row in the full table, not just the
sample. A value the sample never saw that would break the transform (or
that overflows the target column's numeric range, or would NULL out a
primary-key column) drops the column's confidence and routes it to human
review instead of silently reaching COPY. This closed the original gap
for auto-approved decisions — see `internal/pipeline/verify_transform.go`.

What full-table verification does *not* catch: a decision a human
explicitly reviews and approves is taken on trust, since review is the
whole point of that gate. And a transform that can never itself return an
error (a bare pass-through) only catches what its underlying validity
check covers — e.g. `text_to_jsonb` verifies well-formed JSON, but a
transform with no such check of its own has nothing for the full-table
pass to catch. For small tables, passing a `--sample-size` at or above the
row count still avoids the sample/full-table distinction entirely. This
surfaced for real during development — see
`internal/pipeline/integration_test.go`'s comment on
`DisabilityCompByCounty.db`.

## Known limitation: no transform for a genuinely per-row-polymorphic column

SQLite's dynamic typing allows a single column to legitimately hold
different storage classes in different rows — e.g. Fossil SCM's
`config.value`, which holds integers, blobs, and text in different rows of
the same column, all by design, not as dirty data. There is currently no
`copywriter` transform that can losslessly migrate a truly mixed-type
column like this into one static Postgres column type. This is a
deliberate, known limitation rather than a bug: Postgres has no direct
equivalent to SQLite's per-value dynamic typing, and closing this gap would
mean either a `jsonb`-wrapping transform (won't preserve the exact source
type of every value) or a human-reviewed, table-specific approach — neither
of which this tool does automatically today. A table with a column like
this needs manual handling.

## Testing

```
go test ./...                              # Tiers 1-2: heuristics, schema reader,
                                            # resolver, config, and golden-fixture
                                            # tests against the 6 real databases —
                                            # no Postgres required

PGURL=postgres://user@localhost:5432/sqlite2pg_test?sslmode=disable \
  go test -tags integration ./internal/pipeline/... -run TestIntegration -v
                                            # Tier 3: real end-to-end loads
                                            # against a real Postgres instance
```

Tier 3 needs a scratch Postgres database (`createdb sqlite2pg_test`) and is
opt-in via the `integration` build tag — it's not part of the default
`go test ./...` run.

## Package layout

```
cmd/migrate/          CLI entrypoint (run, profile, review, load, verify, resolve subcommands)
internal/
  sqlitereader/        schema + streaming row reading (modernc.org/sqlite, no CGO)
  profiler/            heuristic interface + registry
  profiler/heuristics/ the type-inference heuristics, one file each
  resolver/            confidence scoring + escalation (Resolver interface)
  config/              the persisted MigrationConfig (YAML), versioning, drift detection
  pipeline/            wires sqlitereader + profiler + resolver into ProfileDatabase
  ddl/                 CREATE TABLE generation
  copywriter/          per-column value transforms + streaming pgx COPY pipeline
  review/              review-session core (state machine, decisions)
  tui/                 terminal review UI (tview): table list, per-table
                        data+type grid, filtered type picker
```

Not yet implemented: an LLM-backed `Resolver` (the interface has a `ctx`
parameter reserved for it — see `internal/resolver/escalation.go`); the
current `FileResolver` writes a report for a human (or a Claude Code session)
to resolve out of band instead.
