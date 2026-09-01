# Audit cycle 2 — Phase C: property / fuzz tests for the comparison-logic hot spots

Go's built-in fuzzing (`go test -fuzz`), no new dependency. Four new test
files, nine fuzz targets. Each target run 40s / 8 workers on this pass;
seeds cover the historically-broken cases.

| target | file | execs (40s) | result |
|---|---|---|---|
| `FuzzNumericMatchSortKeyConsistency` | `internal/pipeline/verify_load_fuzz_test.go` | seed-caught | **FAIL — sharpens #65** |
| `FuzzParseColumnCollations` | `internal/sqlitereader/collation_fuzz_test.go` | ~3.5k | **FAIL — new #70** |
| `FuzzColumnCollationsRoundTrip` | `internal/sqlitereader/collation_fuzz_test.go` | 83k | clean |
| `FuzzNumericTextToInteger` | `internal/copywriter/transform_fuzz_test.go` | 1.4M | clean |
| `FuzzFitsRangeMatchesStrconv` | `internal/copywriter/transform_fuzz_test.go` | 1.3M | clean |
| `FuzzIso8601ToDate` | `internal/copywriter/transform_fuzz_test.go` | 1.2M | clean |
| `FuzzEpochScaleTransforms` | `internal/copywriter/transform_fuzz_test.go` | 1.2M | clean |
| `FuzzDisambiguateNamesInvariants` | `internal/ddl/identifiers_fuzz_test.go` | 4.3M | clean |
| `FuzzQuoteIdentRoundTrips` | `internal/ddl/identifiers_fuzz_test.go` | 2.5M | clean |

## Findings

### 1. Numeric match/sort-key divergence — sharpens issue #65 (not a new issue)

`FuzzNumericMatchSortKeyConsistency` checks `sortKeyFor`'s documented
invariant: `valuesMatch(x,y) == true ⇒ sortKeyFor(x) == sortKeyFor(y)`
(the ordered verify path decides equality with `valuesMatch`, the
unordered path purely by sorting on `sortKeyFor` — if they disagree, the
same data passes with a PK and fails without one).

It fails on the seed `int64(9007199254740993)` vs `float64(9007199254740992)`:
`valuesMatch` calls them equal (it converts the int64 through float64,
which rounds `2^53+1` down to `2^53`), while `sortKeyFor` keeps the int64
keyed by its exact decimal text. **This is issue #65, but reproduced with
a far smaller value than the `math.MaxInt64` case originally filed** — any
`int64` above `2^53` compared against a lossy-equal `float64` triggers it.
Also surfaces the deeper question (commented on #65): a NUMERIC column
value above `2^53` loaded into `double precision` genuinely loses
precision, and `valuesMatch` calling the result "equal" means verify
*masks* that loss. Left for Phase E.

The fuzz test carries this as an explicit `knownIssue65Gap` exemption so
the suite is green; remove the exemption when #65 is fixed.

### 2. `leadingIdentifier` accepts an empty quoted identifier — issue #70 (new, Low)

`FuzzParseColumnCollations` (corpus entry `401b74f9169be375`, input
`(""COLLATE 0`): `leadingIdentifier` returns `ok=true` with `name == ""`
for an empty quoted identifier, so `parseColumnCollations` produces a
map entry keyed by `""`. `ColumnCollations` discards it (it only trusts
names `readColumns` already confirmed), so no user-visible impact today —
parser-robustness gap, Low. The fuzz test `continue`s past empty names
with a note to tighten once #70 lands.

## Clean bills (property held under fuzzing)

- **`numeric_text_to_integer` / `parseWholeNumberText`** — 1.4M execs
  against `math/big`: no precision loss, no wrong value, no saturation.
  Issue #15's exact-integer parse is solid.
- **`iso8601_to_date`** — 1.2M execs: never accepted a value with a
  non-midnight time-of-day, never panicked. Issue #42 holds.
- **`FitsRange`** — matches `int16`/`int32` bounds exactly across the full
  `int64` range.
- **epoch-scale transforms** (`unix_epoch_{seconds,millis,micros}`,
  `excel_serial_to_timestamptz`) — never panic, always return `time.Time`
  or a typed error, over the full `int64` range and float serials.
- **`ColumnCollations` round-trip** — 83k real-SQLite execs: every
  cleanly-declared `COLLATE` (bare or quoted `"..."` / `` `...` `` /
  `[...]`, any case) reported back exactly. The ordered-verify collation
  guard's detection is sound for straightforwardly-declared columns.
- **`disambiguateNames`** — 4.3M execs: every output ≤ 63 bytes, distinct
  identities never share an output, mapping is order-independent (the
  cross-run stability the doc comment promises). The identifier
  truncation/collision path — patched three times for the same shape
  (#21/#43/#44) — held.
- **`quoteIdent`** — 2.5M execs: always a well-formed double-quoted
  identifier that decodes back to the input (issue #26's DDL-vs-COPY
  divergence stays closed). NUL bytes excluded — `pgx.Identifier.Sanitize`
  strips them consistently on both the DDL and COPY sides.
