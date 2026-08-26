# sqlite2pg

A `pgloader` replacement for migrating SQLite databases (including Esri File
Geodatabases) into PostgreSQL, with automatic data profiling and an
interactive terminal wizard for reviewing ambiguous type decisions.

Background and design rationale live in the parent directory:
[`../PGLOADER_REWRITE_PLAN.md`](../PGLOADER_REWRITE_PLAN.md) (the original
architecture proposal) and
[`../PGLOADER_REWRITE_PLAN_V2.md`](../PGLOADER_REWRITE_PLAN_V2.md) (this
implementation's plan — wizard, resolver, test strategy). Per-database import
notes from the original hand-written `pgloader` migrations, which this tool's
heuristics encode, are in [`../IMPORT_NOTES.md`](../IMPORT_NOTES.md). The
sample databases the test suite exercises live in `testdata/fixtures/`
within this project, so the tests don't depend on the surrounding directory
layout.

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

This profiles the source, then opens an in-terminal review screen showing
every column's best-guess mapping (editable inline), and waits. Press `f`
then `y` to finish and import — generates the DDL and streams every table
into Postgres via COPY; press `c` then `y` to cancel — nothing touches
Postgres and the draft config is deleted.

For scripted or staged use — profile now, review later, load in CI — the
same steps are available as three separate commands:

```
migrate profile  <source.db>   # sample + profile every column, write a draft config
migrate review   <config.yaml>  # open the terminal review UI to approve/override ambiguous columns
migrate load     <config.yaml> --pg <postgres-url>   # generate DDL, stream rows via COPY
```

- **`run`** is `profile` + `review` + `load` collapsed into one command, with
  a Confirm/Cancel gate in the review screen controlling whether `load` runs
  at all.
  `--keep-config` keeps the generated `<source>.migration.yaml` afterward
  instead of deleting it (useful for inspecting exactly what was loaded, or
  for a later `--resume`).
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
- **`resolve --apply resolutions.yaml`** merges human- (or Claude Code-)
  supplied answers for an `unresolved_report.yaml` back into the config, for
  cases no heuristic could confidently resolve on its own.

## Extending the profiler

Heuristics live one-per-file in `internal/profiler/heuristics/`, implementing:

```go
type Heuristic interface {
    Name() string
    AppliesTo(meta ColumnMeta) bool
    Evaluate(meta ColumnMeta, samples []Value) (Finding, bool)
}
```

Each self-registers via `init()`, so adding a new case — the 8th heuristic,
`sentinel_null`, was added this way after a golden-fixture test caught a real
gap — never requires touching an existing heuristic. Every heuristic has a
same-named `_test.go` with table-driven unit tests and no I/O.

## Known limitation: sampling can miss rare rows

Type decisions are made from a bounded sample (`--sample-size`, default 500),
not a full table scan. A rare edge-case value that falls outside the sample
(e.g. a single aggregate/catch-all row) can be missed at profiling time and
then fail during the actual COPY, which reads every row. For small tables,
pass a `--sample-size` at or above the row count. This surfaced for real
during development — see `internal/pipeline/integration_test.go`'s comment on
`DisabilityCompByCounty.db`.

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
cmd/migrate/          CLI entrypoint (run, profile, review, load, resolve subcommands)
internal/
  sqlitereader/        schema + streaming row reading (modernc.org/sqlite, no CGO)
  profiler/            heuristic interface + registry
  profiler/heuristics/ the 8 type-inference heuristics, one file each
  resolver/            confidence scoring + escalation (Resolver interface)
  config/              the persisted MigrationConfig (YAML), versioning, drift detection
  pipeline/            wires sqlitereader + profiler + resolver into ProfileDatabase
  ddl/                 CREATE TABLE generation
  copywriter/          per-column value transforms + streaming pgx COPY pipeline
  review/               review-session core (state machine, decisions)
  tui/                  terminal review UI (Bubble Tea)
```

Not yet implemented: an LLM-backed `Resolver` (the interface has a `ctx`
parameter reserved for it — see `internal/resolver/escalation.go`); the
current `FileResolver` writes a report for a human (or a Claude Code session)
to resolve out of band instead.
