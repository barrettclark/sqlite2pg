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
