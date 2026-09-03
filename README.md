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
sqlite2pg run <source.db> --pg <postgres-url>
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
sqlite2pg profile  <source.db>   # sample + profile every column, write a draft config
sqlite2pg review   <config.yaml>  # open the terminal review UI to approve/override ambiguous columns
sqlite2pg load     <config.yaml> --pg <postgres-url>   # generate DDL, stream rows via COPY
sqlite2pg verify   <source.db> <config.yaml> --pg <postgres-url>   # confirm the load was correct
```

- **`run`** is `profile` + `review` + `load` collapsed into one command, with
  a Confirm/Cancel gate in the review screen controlling whether `load` runs
  at all. `--keep-config` keeps the generated `<source>.migration.yaml`
  afterward instead of deleting it (useful for inspecting exactly what was
  loaded, or for a later `--resume`).
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
  actual load is a separate `sqlite2pg load` step; only `run` loads
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
- **`verify <source.db> <config.yaml> --pg <postgres-url>`** streams every
  row and every included column from *both* sides and confirms the Postgres
  copy is byte-for-byte correct — not a spot check. It reads the database
  name to connect to from `<config>.state.json` (the same file `--resume`
  uses), so it needs a completed `sqlite2pg load` (or `load --resume`) against
  that exact config first. Run it after every load as the real pass/fail
  gate on data integrity — exit code is non-zero on any mismatch.
  `--out <path>` writes the detailed report to a file instead of stdout; a
  clean run reports 0 mismatches, a dirty one lists every mismatching
  column with up to 20 examples plus the true total count even when capped.
  When the table has a primary key, both sides are read in genuine
  `ORDER BY <primary key>` order (byte-order-collated on both sides, even
  for text keys), so mismatches are a real, deterministic row-by-row match —
  this ordering was added specifically because a bare sequential scan proved
  unsafe: Postgres 18 doesn't reliably return a freshly-COPY'd table's rows
  in insertion order, and a locale-aware Postgres collation can disagree
  with SQLite's default byte-order text comparison, either of which used to
  produce false-positive mismatches. Without a usable primary key, `verify`
  instead compares each column as a sorted value multiset — still
  exhaustive, but a reported example is a position in the sorted comparison,
  not a source row. See the doc comment on `internal/pipeline.VerifyTable`
  for the full detail on both paths.
- **Automatic post-load verification**: `run` and `load` both accept
  `--verify` (run verification unconditionally after a successful load) and
  `--noverify` (never run it); passing both is a usage error. With neither
  flag, they prompt interactively (`Run sqlite2pg verify now? [y/N]:`) and
  default to *not* verifying on a bare Enter, since verification re-streams
  every row and can take a while. This runs against the in-memory config and
  connection the load itself just used, not by re-reading files from disk,
  so it works for `run` whether or not `--keep-config` was passed. A
  mismatch found here is reported as a distinct, serious finding (the load
  already succeeded) and exits non-zero, mirroring standalone `verify`. See
  `cmd/sqlite2pg/postload_verify.go`'s doc comments for the full detail.

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

## Known limitations

- **Sampling can miss rare rows.** Type decisions start from a bounded
  sample (`--sample-size`, default 500), not a full table scan. Before any
  auto-approved decision is trusted, it's re-verified by running the real
  transform against *every* row in the full table — a value the sample
  never saw that would break the transform, overflow the target column's
  range, or NULL out a primary key drops confidence and routes the column
  to human review instead of silently reaching COPY (see
  `internal/pipeline/verify_transform.go`). Two things this doesn't catch:
  a decision a human explicitly approves is taken on trust, and a transform
  that can never itself error (a bare pass-through) only catches what its
  own underlying validity check covers — e.g. `text_to_jsonb` verifies
  well-formed JSON, but a transform with no such check has nothing for the
  full-table pass to catch. For small tables, a `--sample-size` at or above
  the row count avoids the distinction entirely.
- **No transform for a genuinely per-row-polymorphic column.** SQLite's
  dynamic typing allows a single column to legitimately hold different
  storage classes per row (e.g. Fossil SCM's `config.value`, mixing
  integers, blobs, and text by design). There's no `copywriter` transform
  that can losslessly migrate that into one static Postgres column type —
  a deliberate limitation, since Postgres has no equivalent to per-value
  dynamic typing. Such a table needs manual handling.

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
cmd/sqlite2pg/          CLI entrypoint (run, profile, review, load, verify, resolve subcommands)
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
