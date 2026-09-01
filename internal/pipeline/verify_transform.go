package pipeline

import (
	"database/sql"
	"errors"
	"fmt"

	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/sqlitereader"
)

// columnVerifySpec is one column's request into
// verifyTransformsAgainstFullTable: the transform that will run at real
// COPY time, the Postgres target type its result must fit, and whether a
// transform-produced NULL is itself a violation (see rejectNull on
// verifyTransformAgainstFullTable below — same meaning here).
type columnVerifySpec struct {
	Column     string
	Transform  string
	TargetType string
	RejectNull bool

	// CheckFallbackFit asks for a full-table scan even though Transform is
	// empty (issue #69): a default_passthrough decision to a concrete
	// non-text type has no transform, but a rare row whose storage class
	// can't be stored as TargetType — one the 500-row sample missed —
	// still crashes COPY. When set, every row's raw value is checked with
	// fallbackValueFitsTarget (and fitsTargetType's range check), the
	// full-table form of fallbackSampleMismatch.
	CheckFallbackFit bool
}

// verifyResult is one column's outcome from
// verifyTransformsAgainstFullTable: OK true means every value converted
// cleanly, fit the target type, and (when the spec's RejectNull was set)
// never came out NULL. BadValue is the first offending raw value's string
// form when OK is false.
type verifyResult struct {
	OK       bool
	BadValue string
}

// errAllColumnsResolved is a sentinel used only to short-circuit
// sqlitereader.StreamTable once every requested column has already found
// its first violation — there's nothing left for the remaining rows to
// tell any of them. Never returned to verifyTransformsAgainstFullTable's
// own caller.
var errAllColumnsResolved = errors.New("full-table verification: every requested column already resolved")

// verifyTransformsAgainstFullTable is verifyTransformAgainstFullTable
// (see its docs below for the full rationale) generalized to check many
// columns of the same table in a single sequential scan instead of one
// scan per column (issue #55): a table with N auto-approving,
// transform-bearing columns used to pay N full scans of the same rows.
// Every spec with a non-empty Transform is checked against the SAME
// row-by-row cursor; each column keeps its own independent "first
// violation" state, exactly as if it had been checked alone — a column
// that finds its violation early stops being re-checked on later rows
// (there's nothing more its own check needs), but other columns in the
// same row keep being checked until each has either found its own first
// violation or the scan completes. The scan itself only stops early once
// every requested column has resolved.
//
// A spec whose Transform is empty resolves to OK immediately without
// being included in the shared query — unless it sets CheckFallbackFit
// (issue #69), which asks for a full-table storage-class check of the raw
// values even with no transform to run.
func verifyTransformsAgainstFullTable(db *sql.DB, table string, specs []columnVerifySpec) (map[string]verifyResult, error) {
	results := make(map[string]verifyResult, len(specs))

	active := make([]columnVerifySpec, 0, len(specs))
	columns := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.Transform == "" && !s.CheckFallbackFit {
			results[s.Column] = verifyResult{OK: true}
			continue
		}
		active = append(active, s)
		columns = append(columns, s.Column)
	}
	if len(active) == 0 {
		return results, nil
	}

	remaining := len(active)
	streamErr := sqlitereader.StreamTable(db, table, columns, func(row []profiler.Value) error {
		for i, s := range active {
			if _, done := results[s.Column]; done {
				continue
			}
			raw := row[i]
			bad := false
			if s.Transform == "" {
				// issue #69: no transform — check the raw value's storage
				// class fits the target directly, plus the int4/int8 range
				// check every integer-shaped value gets.
				switch {
				case s.RejectNull && raw == nil:
					bad = true
				case !fallbackValueFitsTarget(raw, s.TargetType):
					bad = true
				case !fitsTargetType(raw, s.TargetType):
					bad = true
				}
			} else {
				val, err := copywriter.Transform(s.Transform, raw)
				switch {
				case err != nil:
					bad = true
				case s.RejectNull && val == nil:
					bad = true
				case !fitsTargetType(val, s.TargetType):
					bad = true
				}
			}
			if bad {
				results[s.Column] = verifyResult{OK: false, BadValue: badValueString(raw)}
				remaining--
			}
		}
		if remaining == 0 {
			return errAllColumnsResolved
		}
		return nil
	})
	if streamErr != nil && !errors.Is(streamErr, errAllColumnsResolved) {
		return nil, fmt.Errorf("verifying %s against the full table: %w", table, streamErr)
	}

	// Every active column that never hit a violation across the whole
	// scan (or the scan stopped early because every OTHER column already
	// had) verified clean.
	for _, s := range active {
		if _, ok := results[s.Column]; !ok {
			results[s.Column] = verifyResult{OK: true}
		}
	}
	return results, nil
}

// verifyTransformAgainstFullTable streams every value in table.column and
// runs copywriter.Transform against it — the exact function that will run
// at real COPY time — stopping at the first value it can't actually
// convert. A sample-based heuristic can look entirely clean (issue #13: a
// rare exception well outside a 500-row sample) while the full table
// contains a genuine violation; this is how ProfileDatabase confirms an
// auto-approved decision before trusting it, without inventing a
// per-heuristic verification query. It's a plain sequential scan (no
// sort), unlike the ORDER BY RANDOM() sampling this project already found
// impractically slow at large scale, so it's cheap even on huge tables —
// deliberately run only for columns about to auto-approve, not every
// column profiled.
//
// A transform can convert a value cleanly and still produce something the
// target column type can't hold — e.g. numeric_text_to_integer turning
// "9999999999" into a perfectly valid int64 that overflows a Postgres
// "integer" (int4) column and fails at COPY time (issue #15). targetType
// lets verifyTransformAgainstFullTable range-check the *produced* value
// against the target, not just detect a transform error.
//
// rejectNull additionally rejects any value the transform maps to NULL
// (issue #31, widened by issue #40): uuid_format, numeric_text_to_integer,
// numeric_text_to_double, and nullif_empty all map an empty string to NULL
// by design — a reasonable default for an ordinary nullable column, but
// internal/ddl/generate.go emits a NOT NULL constraint (either the inline
// PRIMARY KEY (...) clause for any PrimaryKeySeq > 0 column, or an explicit
// NOT NULL for any column with ColumnConfig.NotNull set) — so a
// transform-produced NULL on either kind of column would abort the whole
// COPY with a not-null violation instead of merely losing one column's
// value. Callers pass col.PrimaryKeySeq > 0 || col.NotNull.
//
// ok is true when every value converted cleanly, fit the target type, and
// (when rejectNull is set) never came out NULL — or transform is empty
// (nothing to check). badValue is the first offending raw value's string
// form when ok is false. err is a real I/O/query failure, distinct from a
// found violation.
//
// This single-column form is now implemented in terms of
// verifyTransformsAgainstFullTable (issue #55) — kept alongside it because
// its tests, and the shape of a caller checking exactly one column,
// benefit from not having to build a one-element spec slice by hand.
func verifyTransformAgainstFullTable(db *sql.DB, table, column, transform, targetType string, rejectNull bool) (ok bool, badValue string, err error) {
	results, err := verifyTransformsAgainstFullTable(db, table, []columnVerifySpec{{
		Column:     column,
		Transform:  transform,
		TargetType: targetType,
		RejectNull: rejectNull,
	}})
	if err != nil {
		return false, "", err
	}
	r := results[column]
	return r.OK, r.BadValue, nil
}

// fitsTargetType reports whether a value a transform produced actually
// fits the Postgres target column type — "smallint" (int2), "integer"
// (int4), and "bigint" (int8) all have a range narrower than (or, for
// bigint, exactly matching) the int64 a transform like
// numeric_text_to_integer naturally produces, so this range-checks those
// three cases via copywriter.FitsRange (issue #27 extended this beyond
// integer-only); every other target type is left to the transform's own
// error handling.
func fitsTargetType(val any, targetType string) bool {
	n, ok := asInt64(val)
	if !ok {
		// Not an integer-shaped value at all; nothing for this check to say.
		return true
	}
	return copywriter.FitsRange(n, targetType)
}

// badValueString renders the offending raw value for a verifyResult /
// needs-review rationale. A raw SQL NULL is spelled "NULL" rather than
// Go's "<nil>" (Copilot PR #73) — this is the case a RejectNull spec hits.
func badValueString(raw profiler.Value) string {
	if raw == nil {
		return "NULL"
	}
	return fmt.Sprintf("%v", raw)
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}
