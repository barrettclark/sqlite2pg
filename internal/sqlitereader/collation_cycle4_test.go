package sqlitereader

import "testing"

// issue #145 (audit cycle 4 L3): stripSQLComments keeps quoted spans
// verbatim and parseColumnCollations then searches the whole column-def
// remainder for a COLLATE clause — so a COLLATE keyword inside a CHECK
// expression or a string-literal DEFAULT is misread as the column's own
// collation. A genuinely BINARY column then reports NOCASE, and
// primaryKeyOrderingIsSafe drops the table off the streaming PK-ordered
// verify path into the in-memory unordered one.
func TestColumnCollations_IgnoresCollateInsideCheckOrStringLiteral(t *testing.T) {
	cases := map[string]string{
		"COLLATE in a CHECK expression": `
			CREATE TABLE t (
				id    INTEGER PRIMARY KEY,
				name  TEXT CHECK (name = upper(name) COLLATE NOCASE)
			);`,
		"COLLATE in a string-literal default": `
			CREATE TABLE t (
				id    INTEGER PRIMARY KEY,
				name  TEXT DEFAULT 'COLLATE NOCASE'
			);`,
	}
	for label, ddl := range cases {
		db := openTestDB(t, ddl)
		got, err := ColumnCollations(db, "t")
		if err != nil {
			t.Fatalf("%s: ColumnCollations: %v", label, err)
		}
		if got["name"] != "BINARY" {
			t.Errorf("%s: ColumnCollations()[\"name\"] = %q, want \"BINARY\" (a COLLATE inside CHECK/DEFAULT is not the column's collation)", label, got["name"])
		}
	}
}

// The masking must not hide a real top-level COLLATE clause that happens
// to sit after a CHECK constraint or alongside a parenthesised default.
func TestColumnCollations_StillFindsRealCollateAfterACheckConstraint(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE t (
			id   INTEGER PRIMARY KEY,
			name TEXT CHECK (length(name) > 0) COLLATE NOCASE
		);`)
	got, err := ColumnCollations(db, "t")
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	if got["name"] != "NOCASE" {
		t.Errorf("ColumnCollations()[\"name\"] = %q, want \"NOCASE\"", got["name"])
	}
}

// SQLite accepts a single-quoted collation name (COLLATE 'NOCASE') and
// really applies it, so the string-literal masking must keep a '...' that
// is the operand of a top-level COLLATE (Copilot review, PR #151).
func TestColumnCollations_SingleQuotedCollationName(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE t (
			id   TEXT PRIMARY KEY COLLATE 'NOCASE',
			note TEXT DEFAULT 'COLLATE NOCASE'
		);`)
	got, err := ColumnCollations(db, "t")
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	if got["id"] != "NOCASE" {
		t.Errorf("ColumnCollations()[\"id\"] = %q, want \"NOCASE\" (COLLATE 'NOCASE' is a real clause)", got["id"])
	}
	if got["note"] != "BINARY" {
		t.Errorf("ColumnCollations()[\"note\"] = %q, want \"BINARY\" (COLLATE inside a string DEFAULT is not a clause)", got["note"])
	}
}
