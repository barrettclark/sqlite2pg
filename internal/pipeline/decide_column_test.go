package pipeline

import (
	"testing"

	"sqlite2pg/internal/sqlitereader"
)

func TestDecideColumn_FlagsForReviewWhenFullTableCheckFindsAViolationOutsideTheSample(t *testing.T) {
	// Real bug (issue #13): a small sample can look entirely UUID-shaped
	// while the full table has a genuine exception the sample never drew.
	// The sample passed in here deliberately omits the violating row
	// ("811171", present in the table itself) — exactly what a real
	// random sample missing a rare exception looks like from
	// decideColumn's point of view.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO items (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10'),
		('811171')`)

	col := sqlitereader.ColumnInfo{Name: "mb_id", DeclaredType: "TEXT"}
	sample := []any{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "uuid" {
		t.Errorf("expected the suggested type uuid to still be shown, got %q", cc.TargetType)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once a violation was found, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once the full-table check found a violation")
	}
}

func TestDecideColumn_AutoApprovesWhenTheFullTableCheckFindsNoViolation(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO items (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10')`)

	col := sqlitereader.ColumnInfo{Name: "mb_id", DeclaredType: "TEXT"}
	sample := []any{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "uuid" {
		t.Errorf("expected uuid, got %q", cc.TargetType)
	}
	if cc.Confidence < 0.9 {
		t.Errorf("expected the original confidence preserved when the full-table check confirms the sample, got %f", cc.Confidence)
	}
	if unresolved != nil {
		t.Errorf("expected no UnresolvedCase, got %+v", unresolved)
	}
}

func TestDecideColumn_FlagsForReviewWhenFullTableHasAnInt4OverflowTheSampleMissed(t *testing.T) {
	// Issue #15: numeric_text's int4-vs-int8 sizing (sawOutOfInt4Range) is
	// decided from the sample alone. A sample that only shows int4-range
	// values auto-approves the column as "integer" even when the full
	// table contains a value that would overflow it and fail at COPY time.
	// The sample here deliberately omits the overflowing row, mirroring
	// the uuid case above.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, legacy_id TEXT);`)
	db.Exec(`INSERT INTO items (legacy_id) VALUES ('100'), ('200'), ('9999999999')`)

	col := sqlitereader.ColumnInfo{Name: "legacy_id", DeclaredType: "TEXT"}
	sample := []any{"100", "200"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "integer" {
		t.Errorf("expected the suggested type integer to still be shown, got %q", cc.TargetType)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once the int4 overflow was found, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once the full-table check found an int4 overflow")
	}
}

func TestDecideColumn_SkipsTheFullTableCheckWhenAlreadyFlaggedForReview(t *testing.T) {
	// A column already below threshold (or with disagreeing heuristics)
	// gets no benefit from a full-table check — it's already headed to
	// review, so there's no point paying for the extra scan.
	db, _ := openTestDB(t, `CREATE TABLE bikes (id INTEGER PRIMARY KEY, comp INTEGER);`)
	db.Exec(`INSERT INTO bikes (comp) VALUES (0), (1), (0)`)

	col := sqlitereader.ColumnInfo{Name: "comp", DeclaredType: "INTEGER"}
	sample := []any{int64(0), int64(1), int64(0)}

	cc, unresolved, err := decideColumn(db, "bikes", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected the ambiguous boolean01 confidence to stay below threshold, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase")
	}
}

func TestDecideColumn_FlagsForReviewWhenDefaultPassthroughSampleMixesIncompatibleStorageClasses(t *testing.T) {
	// Issue #16: SQLite's dynamic typing legally allows an
	// INTEGER-declared column to hold TEXT-storage values in any row
	// (e.g. atomic_database.db's XRAY_ENERGIES.Inner/Outer, declared INT
	// but containing subshell codes like "K"/"L1"/"M3" alongside real
	// integers). No heuristic claims this column, so it used to fall
	// straight through to default_passthrough at a blind 0.99 confidence
	// based only on the declared type / majority sample type — crashing
	// at COPY time on the first text value. A sample containing both
	// numeric and non-numeric-text values must not auto-approve.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, qty INTEGER);`)
	db.Exec(`INSERT INTO items (qty) VALUES (1), (2), ('lots-of-it')`)

	col := sqlitereader.ColumnInfo{Name: "qty", DeclaredType: "INTEGER"}
	sample := []any{int64(1), int64(2), "lots-of-it"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once the sample showed a non-numeric value in an integer-targeted column, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once default_passthrough's own sample contradicted its declared-type guess")
	}
}

func TestDecideColumn_ReviewedAlwaysStartsFalseRegardlessOfConfidence(t *testing.T) {
	// Reviewed means "a human confirmed this," a separate concept from
	// confidence/needs-review — profiling never sets it, whether a column
	// auto-approves or not.
	db, _ := openTestDB(t, `CREATE TABLE bikes (id INTEGER PRIMARY KEY, num INTEGER);`)
	db.Exec(`INSERT INTO bikes (num) VALUES (3)`)

	col := sqlitereader.ColumnInfo{Name: "num", DeclaredType: "INTEGER"}
	cc, _, err := decideColumn(db, "bikes", col, []any{int64(3)}, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Reviewed {
		t.Error("expected Reviewed=false even for a clean default_passthrough decision")
	}
}
