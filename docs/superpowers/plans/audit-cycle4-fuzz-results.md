# Audit cycle 4 — Phase C property/fuzz results

## Regression gate — all 12 existing fuzz targets

Seed corpora (`go test -run Fuzz ./...`) all PASS. A `-fuzz` burst each
(2026-09-03), no new failures, no corpus-repro regressions:

| target | package | burst | execs | new interesting | result |
|---|---|---:|---:|---:|---|
| `FuzzNumericTextToInteger` | copywriter | 45 s | 1.69 M | 5 | PASS |
| `FuzzFitsRangeMatchesStrconv` | copywriter | 45 s | 1.55 M | 1 | PASS |
| `FuzzIso8601ToDate` | copywriter | 45 s | 1.87 M | 10 | PASS |
| `FuzzEpochScaleTransforms` | copywriter | 45 s | 1.26 M | 2 | PASS |
| `FuzzTransformArmsNeverSilentlyPassThrough` | copywriter | 45 s | 1.55 M | 224 | PASS |
| `FuzzNumericMatchSortKeyConsistency` | pipeline | 45 s | 1.31 M | 1 | PASS |
| `FuzzColumnCollationsRoundTrip` | sqlitereader | 45 s | 74 k | 25 | PASS |
| `FuzzColumnCollationsRoundTripWithNoise` | sqlitereader | 45 s | 80 k | 13 | PASS |
| `FuzzMatchingParenNeverCountsQuotedOrCommentedParens` | sqlitereader | 45 s | 1.49 M | 22 | PASS |
| `FuzzDisambiguateNamesInvariants` | ddl | 45 s | 4.30 M | 0 | PASS |
| `FuzzQuoteIdentRoundTrips` | ddl | 45 s | 4.45 M | 0 | PASS |
| `FuzzParseColumnCollations` | sqlitereader | **181 s** | 5.28 M | 23 | PASS |

That is the 9 from cycle 2 plus the 3 added in cycle 3
(`FuzzTransformArmsNeverSilentlyPassThrough`,
`FuzzColumnCollationsRoundTripWithNoise`,
`FuzzMatchingParenNeverCountsQuotedOrCommentedParens`). Cycle 2's
`knownIssue65Gap` / empty-quoted-identifier corpus exemptions still hold.

## Dedicated long run — `FuzzParseColumnCollations`

3 min, 5.28 M execs, PASS. "New interesting" settled to 23 over the run
(vs. cycle 3's 108 in 45 s against the pre-#125 parser) — the rebuilt
comment-aware scanner has a much smaller reachable-but-untested surface
than the version cycle 3 exercised. The one residual parser gap Phase A
found (L3 — `collateClauseRe` matching a `COLLATE` inside a string
literal / CHECK expression) is a regex-scope issue in
`parseColumnCollations` itself, not in the byte-level scanner this target
drives, so it is out of this harness's reach; it will get a direct
failing test in Phase E.

## New fuzz targets — deferred to Phase E

Phase A's findings are all analytic (M1–M4, L1–L7); none needs a new
detection harness. Where a fix wants property coverage — e.g. L6
(`sortKeyFor` vs `valuesMatch` for `int` / `float32`) — the harness is
best written as that fix's failing test (TDD), not as standalone
detection now.

## Verdict

No fuzz regression. Every target green on both seed corpus and burst; the
reworked collation scanner holds under a 3-minute dedicated run.
