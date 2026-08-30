package pipeline

import "testing"

func TestVerifyTransformAgainstFullTable_OKWhenEveryValueConvertsCleanly(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO t (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10'),
		(NULL)`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "mb_id", "uuid_format", "uuid")
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true, got a violation on %q", badValue)
	}
}

func TestVerifyTransformAgainstFullTable_FindsAViolationOutsideTheSample(t *testing.T) {
	// The real bug (issue #13): a sample-based heuristic can look
	// entirely clean while a full-table scan finds a genuine exception.
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO t (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('811171'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10')`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "mb_id", "uuid_format", "uuid")
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if ok {
		t.Fatal("expected a violation to be found")
	}
	if badValue != "811171" {
		t.Errorf("expected the offending value 811171 reported, got %q", badValue)
	}
}

func TestVerifyTransformAgainstFullTable_OKWhenTransformIsEmpty(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT);`)
	db.Exec(`INSERT INTO t (name) VALUES ('anything at all')`)

	ok, _, err := verifyTransformAgainstFullTable(db, "t", "name", "", "text")
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if !ok {
		t.Error("expected ok=true when there's no transform to verify")
	}
}

func TestVerifyTransformAgainstFullTable_FindsAnInt4OverflowAgainstTheTargetType(t *testing.T) {
	// Issue #15: the transform itself never errors on a value like
	// "9999999999" (it converts cleanly to an int64), but that value
	// overflows a Postgres "integer" (int4) target and would fail at COPY
	// time. The verifier must range-check the produced value against the
	// target type, not just check whether the transform returned an error.
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, legacy_id TEXT);`)
	db.Exec(`INSERT INTO t (legacy_id) VALUES ('100'), ('9999999999')`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "legacy_id", "numeric_text_to_integer", "integer")
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if ok {
		t.Fatal("expected an int4-range violation to be found")
	}
	if badValue != "9999999999" {
		t.Errorf("expected the offending value 9999999999 reported, got %q", badValue)
	}
}

func TestVerifyTransformAgainstFullTable_OKForBigintTargetWithOutOfInt4RangeValues(t *testing.T) {
	// The int4-range check must only apply when the target type is
	// "integer" — a bigint target legitimately holds values outside int4
	// range, and that's not a violation.
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, legacy_id TEXT);`)
	db.Exec(`INSERT INTO t (legacy_id) VALUES ('100'), ('9999999999')`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "legacy_id", "numeric_text_to_integer", "bigint")
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if !ok {
		t.Errorf("expected ok=true for a bigint target, got a violation on %q", badValue)
	}
}
