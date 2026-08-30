# Phase 2C Results — `~/Downloads/beets_library.db`

Database: ~1.4GB, 5 tables (`albums`, `items`, `album_attributes`, `item_attributes`, `migrations`).

## Timing

- `profile --sample-size 500`: **2m25s** wall clock.
- `load` (after review): **56s** wall clock.

## Column counts

- 156 columns total across 5 tables.
- **143 clean** (auto-approved, confidence ≥ 0.9).
- **13 needs-review** (confidence 0.4–0.55, correctly gated below the 0.90 auto-approve threshold):
  - `albums.comp`, `items.comp` — boolean01 heuristic, confidence 0.55 (0/1-only sample).
  - `albums.mb_albumid`, `albums.mb_albumartistid`, `albums.mb_releasegroupid` — uuid_format demoted to 0.4 by full-table check.
  - `items.mb_albumid`, `items.mb_albumartistid`, `items.mb_artistid`, `items.mb_releasegroupid`, `items.mb_releasetrackid`, `items.mb_trackid` — same, demoted to 0.4.
  - `items.composers_ids`, `items.lyricists_ids` — uuid_format demoted to 0.4 (full-table check found NUL-separator-joined multi-value strings).

No crash during profile or load.

## Review outcome (simulating a correct human reviewer)

- `comp` columns: accepted `boolean` (correct — beets' `comp` field genuinely is a compilation-album flag; 0.55 confidence just reflects that 0/1-only samples are inherently ambiguous without domain knowledge. Review gate behaving as designed, not a bug.)
- The 11 demoted `mb_*id`/`composers_ids`/`lyricists_ids` columns: corrected `uuid` → `text` before loading, since the full-table check had already found concrete non-UUID values (legacy numeric IDs like `"811171"`, `"252121"`, `"26296"`, and NUL-joined multi-value strings). Loading with the original (wrong) `uuid` suggestion was also tried first and failed loudly and safely at COPY time (see Findings) rather than corrupting data — confirming the review gate is a real safety net, not just cosmetic.

## Load + spot-check

Loaded successfully into a scratch Postgres DB after correcting the 11 columns above. Verified:
- Row counts match source exactly for all 5 tables (`albums` 13629, `items` 224834, `album_attributes` 37729, `item_attributes` 1876587, `migrations` 11).
- `comp`: boolean true/false counts match sqlite 0/1 counts exactly (890 / 223944).
- `mb_workid`, `acoustid_id` (clean uuid columns): values match source exactly.
- `mb_albumid` etc. (corrected to text): malformed legacy values (e.g. `"811171"`) preserved byte-for-byte, no truncation/coercion.
- `composers_ids`/`lyricists_ids`: NUL-separator-joined multi-value strings preserved byte-for-byte (verified via hex comparison — separator is the 3-byte UTF-8 sequence for U+2400 prefixed by a literal backslash, i.e. `\␀`, not a raw NUL byte).
- `items.path` (bytea): matches source blob byte-for-byte (hex comparison, case-insensitive).
- `items.added` / `items.length` (double precision): match source to full float64 precision (sqlite3 CLI just displays fewer digits by default).
- `albums` sample rows (`album`, `year`, `mb_albumid`, `mb_releasegroupid`) match source exactly, including a `NULL`/`0`-year album with empty MB ids handled correctly.
- Schema (`\d items`) matches the resolved config's target types exactly.

Scratch Postgres databases dropped and `/tmp/audit-p2c` removed after the run.

## Findings

**No genuine tool bugs found.** All flagged columns were correctly gated for review (not silently auto-approved wrong), and the load either succeeded correctly (clean columns) or failed loudly at COPY time rather than corrupting data (when a demoted `uuid` suggestion was loaded without correction, first attempt).

Two items worth logging as **evidence for issue #12** (not new issues, not regressions):

- **#12 evidence, reconfirmed**: `items.composers_ids` and `items.lyricists_ids` — full-table check correctly caught NUL-separator-joined multi-value UUID strings and demoted the column to `text`/needs-review rather than corrupting data by force-casting to `uuid`. This is the exact known #12 case, still open (no `uuid[]` support yet), behaving as expected (review gate, not silent wrong-approval).
- **#12 evidence, new observation**: the *plural* sibling columns — `items.mb_albumartistids` (17093 multi-value rows), `items.mb_artistids` (11621), `items.arrangers_ids` (155), `items.remixers_ids` (29) — hold the same `\␀`-joined multi-value UUID data but were **never flagged at all**: they passed straight through as `text` at confidence 0.99 (`default_passthrough`), because their 500-row sample apparently already contained a multi-value entry, so they failed the sample-level "looks like a single UUID" check before ever reaching the uuid_format heuristic or the full-table verification path. A human reviewing the config would see these as unremarkable 99%-confidence plain-text columns and never learn they're semantically UUID lists — arguably a sharper version of the #12 gap than the demoted `composers_ids`/`lyricists_ids` case, since it produces no review signal whatsoever. Worth folding into #12's eventual fix (once `uuid[]`/multi-value detection exists, it should also catch multi-value samples directly, not only ones a small sample happens to render single-valued).

## Regression check

**No regression.** Both the #13 full-table verification and #12-adjacent detection behaved exactly as expected on real data:
- #13 (full-table verification): correctly caught 11 `uuid`-suggested columns whose declared type didn't hold across the full table, demoting them to needs-review with the specific offending value quoted in the rationale — matching this database's known prior stress-test results.
- #12 (NUL-joined multi-value UUID, still open/unimplemented): `composers_ids`/`lyricists_ids` still correctly fall back to needs-review/text rather than being silently mishandled; no new crash or corruption.
