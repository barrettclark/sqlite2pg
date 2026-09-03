# Audit cycle 5 — Phase C property/fuzz results

## Regression gate — all 12 existing fuzz targets

Seed corpora (`go test -run Fuzz ./...`) all PASS. A `-fuzz` burst each
(2026-09-03), no new failures, no corpus-repro regressions:

| target | package | burst | execs | new interesting | result |
|---|---|---:|---:|---:|---|
| `FuzzNumericTextToInteger` | copywriter | 45 s | 1.69 M | 0 | PASS |
| `FuzzFitsRangeMatchesStrconv` | copywriter | 45 s | 1.45 M | 1 | PASS |
| `FuzzIso8601ToDate` | copywriter | 45 s | 1.79 M | 5 | PASS |
| `FuzzEpochScaleTransforms` | copywriter | 45 s | 1.29 M | 2 | PASS |
| `FuzzTransformArmsNeverSilentlyPassThrough` | copywriter | 45 s | 1.45 M | 77 | PASS |
| `FuzzNumericMatchSortKeyConsistency` | pipeline | 45 s | 1.27 M | 1 | PASS |
| `FuzzColumnCollationsRoundTrip` | sqlitereader | 45 s | 68 k | 11 | PASS |
| `FuzzColumnCollationsRoundTripWithNoise` | sqlitereader | 45 s | 81 k | 15 | PASS |
| `FuzzMatchingParenNeverCountsQuotedOrCommentedParens` | sqlitereader | 45 s | 1.53 M | 4 | PASS |
| `FuzzDisambiguateNamesInvariants` | ddl | 45 s | 4.41 M | 0 | PASS |
| `FuzzQuoteIdentRoundTrips` | ddl | 45 s | 4.66 M | 0 | PASS |
| `FuzzParseColumnCollations` | sqlitereader | **181 s** | 5.90 M | 71 | PASS |

## Dedicated long run — `FuzzParseColumnCollations`

3 min, 5.90 M execs, PASS. The `collation.go` parser changed again this
cycle (#145 `maskParensAndStringLiterals` + the Copilot
`precededByCollateKeyword` follow-up). "New interesting" settled to 71
over the run — higher churn than cycle 4's 23, consistent with the added
masking branch widening the reachable coverage surface, but the
round-trip property (`FuzzColumnCollationsRoundTrip`, 244 corpus entries)
and the paren-invariant property held throughout.

Phase A's L3 (`DEFAULT "COLLATE NOCASE"` — SQLite's double-quoted-string
misfeature — still yields a false NOCASE) is a *semantic* gap the
round-trip fuzzers can't surface: both the parser and any oracle would
have to agree it's a string literal, and neither models SQLite's
identifier-vs-string resolution. It fails safe (over-reports non-BINARY),
same as the residual cycle 4 accepted.

## New targets

None added. Phase A's findings are all analytic; M1 is best reproduced by
a unit test on `previewValueForType("1.5e3", "bigint")`, not a fuzzer.

## Verdict

No fuzz regression. Every target green on both seed corpus and burst; the
twice-reworked collation scanner holds under a 3-minute dedicated run.
