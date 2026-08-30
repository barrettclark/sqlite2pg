package pipeline

import (
	"database/sql"
	"errors"
	"fmt"

	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/sqlitereader"
)

// errFullTableViolation is a sentinel used only to short-circuit
// sqlitereader.StreamTable at the first offending row — never returned to
// verifyTransformAgainstFullTable's own caller.
var errFullTableViolation = errors.New("full-table verification found a value the transform can't convert")

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
// ok is true when every non-nil value converted cleanly and fit the
// target type, or transform is empty (nothing to check). badValue is the
// first offending raw value's string form when ok is false. err is a real
// I/O/query failure, distinct from a found violation.
func verifyTransformAgainstFullTable(db *sql.DB, table, column, transform, targetType string) (ok bool, badValue string, err error) {
	if transform == "" {
		return true, "", nil
	}

	streamErr := sqlitereader.StreamTable(db, table, []string{column}, func(row []profiler.Value) error {
		val, err := copywriter.Transform(transform, row[0])
		if err != nil {
			badValue = fmt.Sprintf("%v", row[0])
			return errFullTableViolation
		}
		if !fitsTargetType(val, targetType) {
			badValue = fmt.Sprintf("%v", row[0])
			return errFullTableViolation
		}
		return nil
	})
	if streamErr == nil {
		return true, "", nil
	}
	if errors.Is(streamErr, errFullTableViolation) {
		return false, badValue, nil
	}
	return false, "", fmt.Errorf("verifying %s.%s against the full table: %w", table, column, streamErr)
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
