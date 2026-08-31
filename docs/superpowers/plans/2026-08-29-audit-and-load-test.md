# Codebase Audit + Full Load-Test Campaign

**Goal:** Find and fix real bugs via (1) a whole-codebase correctness
review by a different model than the one that wrote most of this code,
and (2) profiling and loading every locally available real-world SQLite
database against real Postgres.

**Why now:** Every real bug found so far this project (issues #4, #8-#13)
came from dogfooding against real data, not from writing more unit tests
in isolation. This session's model (Sonnet) wrote nearly all of the
implementation — a fresh Opus pass, and a much larger real-data surface
than has been tried in one sweep before, are both likely to catch a
different class of issue than incremental review during implementation
did.

## Phase 1 — Opus codebase audit

Dispatch a fresh `general-purpose` subagent on `model: opus` (not a fork —
it should NOT inherit this session's context/assumptions) with:
- Full read access to `internal/` and `cmd/migrate/` (no test files needed
  as review targets, though it may read them for context).
- Instructions to look for **correctness bugs**, not style: heuristic
  logic errors, confidence/resolver arbitration bugs, DDL/COPY generation
  bugs, off-by-ones, error-handling gaps, and specifically any case where
  a heuristic's `AppliesTo`/`Evaluate` could disagree with what its paired
  `Transform` actually does (the exact shape of the day-first/UUID/numeric
  bugs fixed this session).
- Told to write a findings report to a file (not just prose back), one
  finding per item: file, line, the concrete failure scenario, severity.
- No fix authority — audit only. I triage afterward (Phase 3).

Chosen over `/code-review` against a diff because there's no single diff
to point it at — the target is the whole `internal/` tree as it stands
today, not what changed recently.

## Phase 2 — Full load-test campaign

**Column-level scope:** this pass evaluates every column's *type decision*,
not just whether the database loads. For every table in every database,
record what the resolver picked, its confidence, and whether that pick is
actually correct given the real data — a load that "succeeds" with a
wrong-but-parseable type (e.g. a UUID stored as `text` instead of `uuid`,
or a 0/1 code column silently auto-approved as `boolean`) is exactly the
class of bug this campaign is meant to surface, not a pass.

**Three open GH issues bear directly on this column-level review** and
should be checked against real data as it's encountered, not treated as
separate work:

- **#1** — ambiguous-column heuristics for 0/1 int and Esri custom types.
  Every 0/1 integer column hit during the campaign is a data point: does
  the current confidence/review-gate behavior actually surface it for
  human judgment, or does it get auto-approved wrong? Same question for
  any Esri custom type names encountered in the geodatabase fixtures.
- **#3** — additional target types (`uuid`, PostGIS geometry,
  `serial`/`bigserial`). Any column that's semantically a UUID, a
  geometry/GeoJSON blob, or an auto-incrementing surrogate key but gets
  mapped to a generic type (`text`, `bytea`, `integer`) is evidence for
  this issue — note the concrete column as a real example rather than
  reasoning about it abstractly.
- **#12** — `uuid[]` for NUL-joined multi-value UUID columns (e.g.
  `composers_ids` in `beets_library.db`). Re-check this specific column
  and any others like it found in other databases during the campaign.

Findings that reproduce one of these three get logged in this plan's
Results section against the issue number (real example, database, column)
rather than filed as new issues — they sharpen existing backlog items
instead of duplicating them. A genuinely new type-decision gap that
doesn't fit #1/#3/#12 still gets its own new issue per Phase 3's rules.

Every local SQLite file, in this order (cheapest/most-likely-to-reveal-
issues first):

**A. Checked-in fixtures** (`testdata/fixtures/`, 15 files) — already
covered by golden tests, but re-running end-to-end against real Postgres
catches anything a golden test's narrower assertions miss.

**B. `../more data/`** (~24 files, outside the repo) — includes several
that look like deliberately adversarial/edge-case fixtures by name alone
(`type-mismatch.db`, `collision.db`, `bigendianwal.db`/`littleendianwal.db`,
`kjvbible-u16be.db`/`kjvbible-u8.db` — encoding/byte-order torture tests),
plus real-world data (`companies.db` already fixed, `neh-grants.db`,
`titanic.db`, `TPC-H-small.db`, etc.) not yet run against this tool at
all.

**C. `~/Downloads/beets_library.db`** — already stress-tested multiple
times this session; one more full run at the end confirms nothing in
Phase 3's fixes regressed it.

**Per-database procedure:**
1. `migrate profile --sample-size 500 <db>` — record: clean/needs-review
   count, wall-clock time, any crash.
2. Review the *resolved* config for every table, not just the unresolved
   report — for each column, sanity-check the chosen type against its
   name and real sample values. Flag anything that looks wrong given the
   column name/sample, not just "needs review": the review gate working
   as designed isn't a bug; a wrong suggestion the human would
   rubber-stamp without noticing is. Specifically watch for the #1/#3/#12
   shapes: 0/1 int columns, Esri custom types, UUID-shaped text, geometry
   blobs, surrogate-key integers, and NUL-joined multi-value UUID columns.
3. Mark every column reviewed, `migrate load` against a scratch local
   Postgres database.
4. Spot-check the loaded schema/data for a handful of columns per table
   against the source (`\d`, a few `SELECT`s) — this is where the real
   bugs hide (an integer that silently truncated, a timestamp shifted by
   a timezone, a column that loaded as the wrong type despite "success").
5. `dropdb` the scratch database immediately after.
6. Log the outcome (pass / found issue — filed as #N / fixed inline) in
   this plan file's Results section as I go, so a resumed session doesn't
   redo work.

Anything that looks like a genuine tool bug (not just a review-worthy
ambiguous column) gets its own TDD cycle: failing test, fix, real-Postgres
re-verification, commit — same process as every fix this session.

## Phase 3 — Triage and fix

1. Read Phase 1's report. For each finding: reproduce it concretely
   (construct the exact input that breaks) before trusting the write-up —
   a model reviewing code it didn't write can misread intent. Discard
   anything that doesn't reproduce.
2. Merge with Phase 2's findings into one fix backlog.
3. Fix each via the established workflow: failing test first, minimal
   fix, real Postgres/pty verification where relevant, one focused commit
   per fix (not one giant commit for everything found).
4. File a GitHub issue only for anything deliberately deferred (not
   fixed in this pass) — everything fixed gets fixed and committed
   directly, matching this session's practice.

## Explicitly out of scope

- Style/simplification-only findings from the Opus review (no behavior
  change) — noted but not acted on unless they're cheap and obviously
  right.
- The `.geodatabase` files' Esri-specific schema quirks are already
  covered by existing fixtures/tests; not re-litigated here unless a
  concrete new issue turns up.
- No new heuristics/features — this is a bug-finding pass, not feature
  work. A genuinely missing case found along the way gets filed as an
  issue, not implemented inline, unless it's a one-line fix in the same
  spirit as this session's other fixes.

## Results

All four sub-reports are in this directory:
`audit-phase1-opus-findings.md` (23 findings, code review only),
`audit-phase2a-fixtures-results.md` (15 dbs),
`audit-phase2b-moredata-results.md` (25 dbs),
`audit-phase2c-beets-results.md` (1 db, 156 columns).

### Consolidated fix backlog (Phase 3 triage), most-impactful first

**Tier 1 — high severity, reproduced against real data:**

1. **DATE-only values auto-approved as `timestamptz`, shifting the calendar
   date on load.** `iso8601_to_timestamptz`/`iso8601_timestamp` heuristic
   doesn't distinguish a pure date (no time-of-day) from a real timestamp;
   assumes UTC midnight, which rolls back a day outside UTC sessions.
   Reproduced independently 4x: `employee.db`, `neh-grants.db`,
   `sunspots.db`, `TPC-H-small.db` (industry-standard TPC-H schema).
   Highest real-world-impact finding of the whole campaign. → issue #3
   territory (needs a plain `date` target distinct from `timestamptz`).
2. **`numeric_text_to_integer`/`_double` silently corrupt large integers.**
   Parses via `strconv.ParseFloat` then casts — loses precision beyond
   float64's ~15-17 significant digits. Reproduced in `bikes.db.legacy_id`
   (19-digit IDs, wrong by dozens of units, no error). Opus's audit
   independently derived the same bug from reading the code.
3. **`default_passthrough` trusts declared SQLite type affinity over
   sampled data**, handing 0.99 confidence to columns whose sample plainly
   contains non-conforming values. Reproduced 2x: `atomic_database.db`
   (`XRAY_ENERGIES.Inner`/`Outer`, INT-declared but mixed int/text) and
   `type-mismatch.db` (`qty`, INTEGER-declared with a literal
   `'lots-of-it'` row in a 3-row table — sampled and ignored). Both crash
   `load`.
4. **Profile crashes on implicit-column FKs** (`REFERENCES table` with no
   explicit column list — a common, valid SQLite form). `PRAGMA
   foreign_key_list` returns NULL for the `to` column; the schema reader
   scans it into a non-nullable string and panics. Reproduced 2x on
   independent real Fossil-SCM databases (`demo01.db`, `sqliterepo.db`).
5. **TUI type picker clears the column's transform even when the human
   re-confirms the *same* type** — confirming a correct decision breaks the
   COPY (`internal/tui/typepicker.go`). Found by Opus's code audit; not
   independently load-tested (TUI interaction, out of Phase 2's scripted
   scope) but the failure mechanism is exact and easy to verify directly.
6. **`migrate load --resume` provisions a brand-new database every run**,
   so a resumed load silently lands in an empty database missing every
   table the state file claims is "already completed." Code-audit finding;
   not exercised by Phase 2 (no run in the campaign hit a load failure
   requiring `--resume`).
7. **Resolver's `disagreementMargin` (0.1) is wider than every gap in the
   confidence ladder** (0.99/0.95/0.90/0.85), so a heuristic explicitly
   tuned to "must win outright" (`yyyymmdd_date` at 0.95 vs `numeric_text`
   at 0.90) never does — forces unnecessary review on every YYYYMMDD-named
   date column. Compounding: the resulting "heuristics disagreed" signal
   is never persisted to the config, so `load` (no `--force`) and the TUI's
   `NeedsReview` both ignore it and would load the ambiguous pick anyway.
   Fix these two together (#4/#5 in the Opus report).
8. **Issue #1, concrete real-data case:** a 0/1 flag column with **TEXT**
   declared affinity (`sakila.db.customer.active`, `CHAR(1)` storing
   `'0'`/`'1'`) never reaches `boolean01` (which only looks at
   INTEGER-affinity columns) — instead `numeric_text` claims it and
   auto-approves `integer` at 0.90, just past the threshold, with zero
   review. The identically-shaped `staff.active` (SMALLINT affinity)
   correctly gets flagged. Fix: cross-check between `numeric_text` and
   `boolean01` for any all-0/1 text column, not just integer-affinity ones.

**Tier 2 — medium severity, real but narrower blast radius:**

9. DDL crash on Postgres 63-byte identifier-truncation collisions
   (`collision.db`, two long column names identical in their first 63
   bytes) — no disambiguation.
10. Full-table verification (issue #13) is a no-op for every transform
    that can't itself return an error (`text_to_jsonb`, `esri_typename`,
    `nullif_empty`, and the pass-through branches of several others) — the
    "verified against the whole table" guarantee silently doesn't apply to
    exactly the transforms with no validation logic.
11. `comma_formatted_number` accepts decimal values (`"1,234.56"`) but
    targets `integer` + `strip_commas`, which can't parse a decimal point.
12. `julian_day_to_date` truncates (`int64(f)`) instead of rounding
    (`math.Floor(f+0.5)`), giving the wrong calendar day for any Esri
    `realdate` value with an afternoon/evening time-of-day (half of all
    possible fractional values). The campaign's own geodatabase fixtures
    happened to only exercise exact `.5` (midnight) values, which are
    correct — the bug is real but unexercised by Phase 2's actual data.
13. Mixed-storage columns (numeric + one sentinel string row) on a
    `NUMERIC`/`DECIMAL`-declared column fall through to a numeric type
    instead of `text`, because `SentinelNull.AppliesTo` doesn't recognize
    those declared-type names. `DisabilityCompByCounty.db`'s FIPS-code
    column is the same shape but declared plain `TEXT`, which is why it
    was correctly gated in Phase 2A — the gap is specifically the
    NUMERIC/DECIMAL declared-type case.
14. DDL identifier quoting uses Go's `%q` (not SQL quoting) in several
    places, while `LoadTable`'s COPY path correctly uses
    `pgx.Identifier.Sanitize`. An identifier with an embedded `"` produces
    invalid DDL that disagrees with what COPY would have named it. Not
    reproduced against real data this pass (none of the ~40 databases
    happened to have a quote-containing identifier) but the project's own
    `DisabilityCompByCounty.db` header naming style makes this plausible.
15. Int4-vs-int8 sizing for `numeric_text` is decided from the *sample*;
    the full-table verifier runs the transform (which has no idea the
    target is `int4`) so it can't catch a full-table value that overflows
    int4 the sample never showed. Same root cause as #2/#7 above — worth
    fixing together with the `numeric_text_to_integer` rewrite.
16. New type-decision gap, not #1/#3/#12: a declared-`JSON` SQLite column
    (`random-json.db`) resolves to `text` at 0.99 confidence instead of
    `jsonb` — the declared type is an unambiguous, unused signal. Data
    loads intact; this is a missed opportunity, not corruption. Recommend
    folding into issue #3 (additional target types) rather than a new
    issue, since it's the same class of gap as uuid/PostGIS/serial.
17. Issue #12 sharpened by real data: beyond the already-known
    `composers_ids`/`lyricists_ids` NUL-joined case (correctly demoted to
    review), sibling plural columns in the same database
    (`mb_albumartistids`, `mb_artistids`, `arrangers_ids`, `remixers_ids`)
    hold identical multi-value data but were **never flagged at all** —
    their 500-row sample happened to already contain a multi-value entry,
    so they read as ordinary text from the start and never reached
    `uuid_format`/the full-table check. Worth folding into #12's eventual
    fix: multi-value detection needs to work from the sample directly, not
    only via the single-value-then-violated path.
18. TUI picker's validity filter tests candidate types against the *raw*
    value rather than post-transform, so it can hide the correct type
    (offers `timestamptz` only because it's already selected) and offer
    genuinely invalid ones (`smallint` for a value like `70000`).
19. `TableSource`'s producer goroutine can block forever if pgx stops
    pulling after a mid-COPY server error (channel leak), and a second
    `Next()` call after `Err()` returns can clobber a real recorded error
    with `nil`.
20. `ReadSchema` silently drops any table whose column read fails (not just
    the intended "unsupported virtual table" case) — migration completes
    with exit 0 while a user table is simply missing, no warning.
21. `GenerateCreateTable` emits syntactically invalid SQL (`CREATE TABLE
    "t" ();`) for a table with zero included columns (e.g. an Esri table
    whose only column is a dropped `geometryblob`).
22. Transforms that map `""` → `NULL` (`uuid_format`, `numeric_text_to_*`,
    `nullif_empty`) can null out a primary-key column and abort the load
    with a not-null-constraint violation — same class of gap as full-table
    verification not checking target-appropriateness (#10/#15).
23. Issue #3, corroborating evidence only (no fix needed beyond the issue
    itself): recurring `AUTOINCREMENT`/Esri `OBJECTID` surrogate keys
    mapping to plain `integer` instead of `serial`/`bigserial`, seen across
    most fixtures with a primary key.

**Tier 3 — low severity / polish, fix opportunistically or defer:**

24. `--dry-run` prints `CREATE TABLE` statements in random map-iteration
    order (the real `load` path already sorts; the dry-run branch was
    missed).
25. `Load` never checks `cfg.ConfigVersion` against the current schema
    version despite the field existing for exactly that purpose.
26. Source `NOT NULL` is read (`ColumnInfo.NotNull`) and then never used
    anywhere — silently dropped instead of emitted or explicitly declared
    out of scope.
27. `FilterSystemTables` drops any user table named `st_*` unconditionally,
    not just on genuine Spatialite/Esri databases.
28. Generated FK-constraint/index names aren't length-checked against
    Postgres's 63-byte `NAMEDATALEN`; a long table + composite key can
    silently truncate-collide.
29. `formatSampleValue` truncates the review grid's preview by byte count,
    which can split a multi-byte UTF-8 character mid-codepoint.
30. `migrate run` deletes the generated config on *any* exit path,
    including a failed load — defeating `--resume`/`--keep-config`'s
    stated purpose for exactly the case it exists for.

**Not bugs — review gate working as designed** (logged for completeness,
no action): `AustinRoadConstruction.db`'s 6 boolean01 flags,
`chinook.db.invoice_items.Quantity`, `ssb-small.db`'s 5 flagged date-flag
columns, `titanic.db.Survived`/`Age`, `TPC-H-small.db.O_SHIPPRIORITY`,
`iso10383-mic.db`'s two flagged date columns (resolver still picked
correctly despite the review gate firing), `beets_library.db`'s `comp`
columns, `ssb-small.db`'s FK-constraint failure (source data already
violates its own declared FK, SQLite just doesn't enforce it).

### GitHub issues filed for this backlog (2026-08-30)

Per explicit instruction, every actionable item above was filed as its own
GitHub issue (or, where it sharpens an existing issue rather than being
new, added as a comment) *before* any fix work started, so the audit's
reasoning has a durable paper trail independent of this plan file.

| Backlog item | Issue |
|---|---|
| 1. Date-only → timestamptz shift | [#14](https://github.com/barrettclark/sqlite2pg/issues/14) |
| 2. numeric_text_to_integer/_double precision loss (includes item 15, int4/int8 sizing) | [#15](https://github.com/barrettclark/sqlite2pg/issues/15) |
| 3. default_passthrough trusts declared type | [#16](https://github.com/barrettclark/sqlite2pg/issues/16) |
| 4. Implicit-column FK crash | [#17](https://github.com/barrettclark/sqlite2pg/issues/17) |
| 5. Type picker clears transform on same-type reselect | [#18](https://github.com/barrettclark/sqlite2pg/issues/18) |
| 6. `--resume` provisions a new database | [#19](https://github.com/barrettclark/sqlite2pg/issues/19) |
| 7. Resolver disagreement margin + unpersisted disagreement signal | [#20](https://github.com/barrettclark/sqlite2pg/issues/20) |
| 8. Issue #1's sakila.db `customer.active` case | comment on [#1](https://github.com/barrettclark/sqlite2pg/issues/1) |
| 9. Identifier-collision DDL crash | [#21](https://github.com/barrettclark/sqlite2pg/issues/21) |
| 10. Full-table verification no-op for no-fail transforms | [#22](https://github.com/barrettclark/sqlite2pg/issues/22) |
| 11. comma_formatted_number decimal/integer mismatch | [#23](https://github.com/barrettclark/sqlite2pg/issues/23) |
| 12. julian_day_to_date truncation | [#24](https://github.com/barrettclark/sqlite2pg/issues/24) |
| 13. NUMERIC/DECIMAL sentinel fallback | [#25](https://github.com/barrettclark/sqlite2pg/issues/25) |
| 14. DDL `%q` identifier quoting | [#26](https://github.com/barrettclark/sqlite2pg/issues/26) |
| 15. int4/int8 sizing from sample | folded into [#15](https://github.com/barrettclark/sqlite2pg/issues/15) |
| 16. Declared-JSON column not mapped to jsonb | comment on [#3](https://github.com/barrettclark/sqlite2pg/issues/3) |
| 17. #12's sibling plural mb_*ids columns never flagged | comment on [#12](https://github.com/barrettclark/sqlite2pg/issues/12) |
| 18. TUI picker validity filter ignores transform | [#27](https://github.com/barrettclark/sqlite2pg/issues/27) |
| 19. TableSource goroutine leak / Next() clobbers error | [#28](https://github.com/barrettclark/sqlite2pg/issues/28) |
| 20. ReadSchema silently drops tables | [#29](https://github.com/barrettclark/sqlite2pg/issues/29) |
| 21. Empty-column-set table emits invalid DDL | [#30](https://github.com/barrettclark/sqlite2pg/issues/30) |
| 22. Nulling transforms can break a PK column | [#31](https://github.com/barrettclark/sqlite2pg/issues/31) |
| 23. AUTOINCREMENT/OBJECTID → integer, not serial | comment on [#3](https://github.com/barrettclark/sqlite2pg/issues/3) |
| 24. `--dry-run` random table order | [#32](https://github.com/barrettclark/sqlite2pg/issues/32) |
| 25. ConfigVersion never checked | [#33](https://github.com/barrettclark/sqlite2pg/issues/33) |
| 26. NOT NULL dropped | [#34](https://github.com/barrettclark/sqlite2pg/issues/34) |
| 27. `st_*` filter applies unconditionally | [#35](https://github.com/barrettclark/sqlite2pg/issues/35) |
| 28. FK/index name length unchecked | [#36](https://github.com/barrettclark/sqlite2pg/issues/36) |
| 29. Sample-value byte truncation | [#37](https://github.com/barrettclark/sqlite2pg/issues/37) |
| 30. `migrate run` deletes config on failed load | [#38](https://github.com/barrettclark/sqlite2pg/issues/38) |

### Phase 3 execution — complete (2026-08-30)

All 25 filed issues (#14-#38) plus #1's TEXT/CHAR-boolean gap and #12's
uuid[] feature request were fixed via the established TDD cycle (failing
test → minimal fix → real Postgres re-verification), one focused commit
per issue, and closed. Only #3 (additional target types: PostGIS geometry,
serial/bigserial — partially narrowed by #12 landing uuid[]) remains open,
by design — it's a genuine feature-scope item, not a bug. #39, discovered
mid-fix (SQLite read-path `%q` quoting, the read-side analog of #26), was
filed and fixed the same day. Full commit range: `76ae27e..aa1f44b`.

## Phase 4 — Follow-up audit on today's changes (2026-08-30)

**Why a second pass, and why it doesn't repeat Phases 1-2 as-is:** today's
26 commits were each reviewed only by the implementer subagent that wrote
them — nobody has looked at the *combined* diff with fresh eyes, and
several fixes touched the same shared files sequentially (`decide_column.go`,
`internal/resolver/confidence.go`, `numeric_text.go`) — exactly the shape
that produces small inconsistencies (redundant checks, naming drift, an
edge case one fix's test covers that a later fix quietly narrows) that a
single-fix-at-a-time review process can't catch by construction.

1. **Whole-diff review of today's work** (`76ae27e..aa1f44b`, ~26 commits)
   by a fresh model pass — same idea as Phase 1, scoped to today's diff
   instead of the whole codebase. Highest-value item; not yet started.
2. ~~`go test -race ./...` as a standing pass~~ — **done**, run by Barrett
   directly: clean, no races, including `internal/pipeline` (the package
   with the most goroutine/channel-touching fixes today, 36.5s run).
3. **Targeted re-run of the Phase 2 load-test campaign** — not from
   scratch, but confirming none of today's 26 fixes regressed against the
   original 41 databases, and specifically re-checking `beets_library.db`
   and `sakila.db` since those are where #1 and #12's fixes have real,
   previously-broken data to prove themselves against beyond their own
   purpose-built test fixtures.
4. **Live pty/terminal smoke test of the TUI.** #18 and #27 both fixed
   real picker bugs, but every verification was unit tests or hand-built
   configs simulating the picker — nothing actually drove the terminal UI
   live, despite this project's own stated value on pty/expect testing
   catching what unit tests miss (a real startup panic was caught this way
   earlier in the project).
5. **Performance check on full-table verification at scale.** Issue #13's
   full-table verification (extended repeatedly today by #15/#16/#22/#27/#31)
   adds a second full sequential scan for every auto-approving column.
   Worth timing profile runs on the largest fixtures (`sqliterepo.db`
   110MB, `employee.db` 2.8M rows) before/after today's changes to confirm
   no meaningful slowdown crept in.
6. **`testdata/fixtures/README.md` provenance update.** Several new
   fixtures were added today (`sample-implicit-fk.sqlite`,
   `sample-type-mismatch.sqlite`, others) — confirm that file still
   accurately lists every fixture and its origin.

### Phase 4 results

*(filled in during execution)*
