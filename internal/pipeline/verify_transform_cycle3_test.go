package pipeline

import "testing"

// TestVerifyTransformAgainstFullTable_FindsANonStringRowInADateTimeColumn
// is issue #103's regression at the full-table-check level. The
// iso8601_timestamp heuristic that assigns iso8601_to_timestamptz skips a
// non-string, non-time sample with `continue` rather than disqualifying
// the column, so a DATETIME column can be assigned this transform and
// still hold a rare BLOB row (SQLite's dynamic typing permits it) outside
// the 500-row sample. Before the fix, iso8601_to_timestamptz's
// `if !ok { return raw, nil }` passed that row straight through, so this
// full-table check could never fail for the column (issue #13's guard
// inert, exactly as #22/#42 for other transforms) and the load only broke
// once COPY itself rejected the value. After the fix the arm errors on the
// non-string value and the check catches it here.
func TestVerifyTransformAgainstFullTable_FindsANonStringRowInADateTimeColumn(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, ts DATETIME);`)
	db.Exec(`INSERT INTO t (ts) VALUES
		('2019-03-04 14:22:00'),
		('2020-11-30 09:00:00'),
		(x'00ff')`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "ts", "iso8601_to_timestamptz", "timestamptz", false)
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if ok {
		t.Fatal("expected the BLOB row to be found as a violation")
	}
	if badValue == "" {
		t.Error("expected the offending value to be reported")
	}
}

// TestVerifyTransformAgainstFullTable_OKForRealDateTimeColumn guards the
// #103 fix against over-rejecting: an ordinary non-midnight DATETIME
// column (scanned into time.Time by modernc.org/sqlite) must still verify
// clean.
func TestVerifyTransformAgainstFullTable_OKForRealDateTimeColumn(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, ts DATETIME);`)
	db.Exec(`INSERT INTO t (ts) VALUES
		('2019-03-04 14:22:00'),
		('2020-11-30 09:00:00'),
		('2021-07-15 23:59:59'),
		(NULL)`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "ts", "iso8601_to_timestamptz", "timestamptz", false)
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true for a clean DATETIME column, got a violation on %q", badValue)
	}
}
