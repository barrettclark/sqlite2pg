package sqlitereader

import "testing"

// TestColumnCollations_DetectsExplicitNonBinaryCollation reproduces the
// missing piece behind the ORDER BY collation-mismatch regression
// (internal/pipeline/verify_load.go's verifyTableOrdered assumes every
// text primary-key column is BINARY-collated): a column explicitly
// declared COLLATE NOCASE must be reported as such, not silently treated
// as BINARY.
func TestColumnCollations_DetectsExplicitNonBinaryCollation(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE users (
			name TEXT PRIMARY KEY COLLATE NOCASE,
			email TEXT COLLATE RTRIM,
			bio TEXT
		);
	`)

	got, err := ColumnCollations(db, "users")
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	want := map[string]string{"name": "NOCASE", "email": "RTRIM", "bio": "BINARY"}
	for col, wantCollation := range want {
		if got[col] != wantCollation {
			t.Errorf("ColumnCollations()[%q] = %q, want %q (full map: %+v)", col, got[col], wantCollation, got)
		}
	}
}

// TestColumnCollations_DefaultsToBinaryWhenUnspecified confirms a column
// with no explicit COLLATE clause reports SQLite's actual default,
// BINARY — the common case, which must not be misdetected as something
// else and needlessly trigger the unordered-comparison fallback.
func TestColumnCollations_DefaultsToBinaryWhenUnspecified(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE items (
			id INTEGER PRIMARY KEY,
			label TEXT
		);
	`)

	got, err := ColumnCollations(db, "items")
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	if got["label"] != "BINARY" {
		t.Errorf("ColumnCollations()[\"label\"] = %q, want \"BINARY\"", got["label"])
	}
	if got["id"] != "BINARY" {
		t.Errorf("ColumnCollations()[\"id\"] = %q, want \"BINARY\"", got["id"])
	}
}

// TestColumnCollations_HandlesQuotedColumnNames confirms the CREATE TABLE
// text parser correctly associates a COLLATE clause with a
// double-quoted column name, not just a bare identifier.
func TestColumnCollations_HandlesQuotedColumnNames(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE weird (
			"my col" TEXT COLLATE NOCASE,
			normal TEXT
		);
	`)

	got, err := ColumnCollations(db, "weird")
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	if got["my col"] != "NOCASE" {
		t.Errorf(`ColumnCollations()["my col"] = %q, want "NOCASE" (full map: %+v)`, got["my col"], got)
	}
	if got["normal"] != "BINARY" {
		t.Errorf(`ColumnCollations()["normal"] = %q, want "BINARY"`, got["normal"])
	}
}
