package pipeline

import "testing"

func TestVerifyTransformAgainstFullTable_OKWhenEveryValueConvertsCleanly(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO t (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10'),
		(NULL)`)

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "mb_id", "uuid_format")
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

	ok, badValue, err := verifyTransformAgainstFullTable(db, "t", "mb_id", "uuid_format")
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

	ok, _, err := verifyTransformAgainstFullTable(db, "t", "name", "")
	if err != nil {
		t.Fatalf("verifyTransformAgainstFullTable: %v", err)
	}
	if !ok {
		t.Error("expected ok=true when there's no transform to verify")
	}
}
