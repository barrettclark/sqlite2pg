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
// ok is true when every non-nil value converted cleanly, or transform is
// empty (nothing to check). badValue is the first offending raw value's
// string form when ok is false. err is a real I/O/query failure, distinct
// from a found violation.
func verifyTransformAgainstFullTable(db *sql.DB, table, column, transform string) (ok bool, badValue string, err error) {
	if transform == "" {
		return true, "", nil
	}

	streamErr := sqlitereader.StreamTable(db, table, []string{column}, func(row []profiler.Value) error {
		if _, err := copywriter.Transform(transform, row[0]); err != nil {
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
