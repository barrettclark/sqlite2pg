# Audit cycle 3 — Phase C property/fuzz results

## Regression gate — the four existing fuzz files

All nine existing targets pass their **seed corpora** (`go test -run Fuzz`)
and a **45 s `-fuzz` burst** each (2026-09-02), no new failures, no
corpus-repro regressions:

| target | package | 45 s execs | result |
|---|---|---:|---|
| `FuzzNumericTextToInteger` | copywriter | 1.54 M | PASS |
| `FuzzFitsRangeMatchesStrconv` | copywriter | 1.47 M | PASS |
| `FuzzIso8601ToDate` | copywriter | 1.70 M | PASS |
| `FuzzEpochScaleTransforms` | copywriter | 1.29 M | PASS |
| `FuzzNumericMatchSortKeyConsistency` | pipeline | 1.30 M | PASS |
| `FuzzParseColumnCollations` | sqlitereader | 1.57 M | PASS |
| `FuzzColumnCollationsRoundTrip` | sqlitereader | 77 k | PASS |
| `FuzzDisambiguateNamesInvariants` | ddl | — | PASS |
| `FuzzQuoteIdentRoundTrips` | ddl | — | PASS |

`knownIssue65Gap` / the empty-quoted-identifier corpus exemptions from
cycle 2 still hold; nothing regressed them.

Note `FuzzParseColumnCollations` still logs a high "new interesting" count
(108 in 45 s) — the collation-parser input space is large and the harness
keeps finding new coverage. It stayed green, but that churn is consistent
with Phase A's M1 / L4 (the parser has untested edges: quote/comment-blind
`matchingParen`, doubled-quote table names, `CREATE VIRTUAL TABLE`). A
longer dedicated `-fuzztime` run once M1/L4 are fixed is worth doing to
confirm the fixes hold.

## New fuzz targets — deferred to Phase E

The plan calls for new targets on `readAnswerWithDeadline`,
`columnListOpenParen`/`parseColumnCollations`, and the #81 TUI
integer-preview path. Phase A already found the bugs those targets would
exercise **analytically** (L4 for `columnListOpenParen`; M1 for
`matchingParen`; M2 + L3 + issue #81 for the TUI preview float64 path;
`readAnswerWithDeadline` got a clean bill — all six scripted-stdin shapes
walked and correct). The new harnesses are therefore best written as the
failing tests for those fixes (TDD), not as standalone detection now.
