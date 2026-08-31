package pipeline

import "testing"

// TestProfileDatabase_BatchesFullTableVerificationIntoOneScanPerTable is
// the crux test for issue #55: a table with several auto-approving,
// transform-bearing columns must pay exactly one full-table scan, not one
// per column. A test that only inspected the final decided config
// (unchanged either way) wouldn't prove this — so this counts the actual
// `SELECT ... FROM "table"` queries sent to SQLite via a counting driver.
func TestProfileDatabase_BatchesFullTableVerificationIntoOneScanPerTable(t *testing.T) {
	db, path, qc := openCountingTestDB(t, `
		CREATE TABLE widgets (
			id INTEGER PRIMARY KEY,
			owner_id TEXT,
			batch_id TEXT,
			source_id TEXT
		);
	`)
	db.Exec(`INSERT INTO widgets (owner_id, batch_id, source_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10', 'e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10', '11111111-1111-1111-1111-111111111111'),
		('22222222-2222-2222-2222-222222222222', '33333333-3333-3333-3333-333333333333', '44444444-4444-4444-4444-444444444444')`)

	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	widgets := result.Config.Tables["widgets"]
	for _, col := range []string{"owner_id", "batch_id", "source_id"} {
		cc := widgets.Columns[col]
		if cc.TargetType != "uuid" {
			t.Errorf("%s: expected uuid, got %q", col, cc.TargetType)
		}
		if cc.Transform != "uuid_format" {
			t.Errorf("%s: expected uuid_format transform, got %q", col, cc.Transform)
		}
		if cc.NeedsReview {
			t.Errorf("%s: expected auto-approved (NeedsReview=false), got true", col)
		}
	}

	if got := qc.countFor("widgets"); got != 1 {
		t.Errorf("expected exactly 1 full-table scan of widgets (3 auto-approving transform columns batched into one scan), got %d", got)
	}
}

// TestVerifyTransformsAgainstFullTable_TracksEachColumnIndependentlyInOneScan
// proves per-column correctness survives batching: column A's violation
// and column B's clean pass, both discovered in the SAME shared table
// scan, must be reported independently — not conflated, and not requiring
// two scans to find.
func TestVerifyTransformsAgainstFullTable_TracksEachColumnIndependentlyInOneScan(t *testing.T) {
	db, _, qc := openCountingTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT, b TEXT);`)
	db.Exec(`INSERT INTO t (a, b) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10', '11111111-1111-1111-1111-111111111111'),
		('811171', '22222222-2222-2222-2222-222222222222'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10', '33333333-3333-3333-3333-333333333333')`)

	results, err := verifyTransformsAgainstFullTable(db, "t", []columnVerifySpec{
		{Column: "a", Transform: "uuid_format", TargetType: "uuid"},
		{Column: "b", Transform: "uuid_format", TargetType: "uuid"},
	})
	if err != nil {
		t.Fatalf("verifyTransformsAgainstFullTable: %v", err)
	}

	if results["a"].OK {
		t.Error("expected column a's violation ('811171') to be found")
	}
	if results["a"].BadValue != "811171" {
		t.Errorf("expected badValue 811171 for column a, got %q", results["a"].BadValue)
	}
	if !results["b"].OK {
		t.Errorf("expected column b to verify clean, got violation on %q", results["b"].BadValue)
	}

	if got := qc.countFor("t"); got != 1 {
		t.Errorf("expected exactly 1 full-table scan covering both columns, got %d", got)
	}
}

// TestVerifyTransformsAgainstFullTable_AllColumnsCleanInOneScan is the
// all-clean counterpart: several columns, all verify OK, still one scan.
func TestVerifyTransformsAgainstFullTable_AllColumnsCleanInOneScan(t *testing.T) {
	db, _, qc := openCountingTestDB(t, `CREATE TABLE t (id INTEGER PRIMARY KEY, a TEXT, b TEXT, c TEXT);`)
	db.Exec(`INSERT INTO t (a, b, c) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10', '11111111-1111-1111-1111-111111111111', '55555555-5555-5555-5555-555555555555'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10', '22222222-2222-2222-2222-222222222222', '66666666-6666-6666-6666-666666666666')`)

	results, err := verifyTransformsAgainstFullTable(db, "t", []columnVerifySpec{
		{Column: "a", Transform: "uuid_format", TargetType: "uuid"},
		{Column: "b", Transform: "uuid_format", TargetType: "uuid"},
		{Column: "c", Transform: "uuid_format", TargetType: "uuid"},
	})
	if err != nil {
		t.Fatalf("verifyTransformsAgainstFullTable: %v", err)
	}
	for _, col := range []string{"a", "b", "c"} {
		if !results[col].OK {
			t.Errorf("%s: expected ok=true, got violation on %q", col, results[col].BadValue)
		}
	}
	if got := qc.countFor("t"); got != 1 {
		t.Errorf("expected exactly 1 full-table scan, got %d", got)
	}
}
