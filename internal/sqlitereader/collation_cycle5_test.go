package sqlitereader

import "testing"

// issue #160 (audit cycle 5 L3): #145's masker blanked only '...' string
// literals. SQLite's double-quoted-string misfeature means a DEFAULT
// value can also be written "..." or `...`, and a COLLATE keyword inside
// one was still read as the column's collation. Now a "..."/`...` span
// that is NOT the operand of a top-level COLLATE is masked too.
func TestColumnCollations_IgnoresCollateInsideDoubleQuotedOrBacktickDefault(t *testing.T) {
	cases := map[string]string{
		"double-quoted string DEFAULT": `
			CREATE TABLE t (
				id   INTEGER PRIMARY KEY,
				name TEXT DEFAULT "COLLATE NOCASE"
			);`,
		"backtick string DEFAULT": `
			CREATE TABLE t (
				id   INTEGER PRIMARY KEY,
				name TEXT DEFAULT ` + "`COLLATE NOCASE`" + `
			);`,
	}
	for label, ddl := range cases {
		db := openTestDB(t, ddl)
		got, err := ColumnCollations(db, "t")
		if err != nil {
			t.Fatalf("%s: ColumnCollations: %v", label, err)
		}
		if got["name"] != "BINARY" {
			t.Errorf("%s: ColumnCollations()[\"name\"] = %q, want \"BINARY\"", label, got["name"])
		}
	}
}

// A genuine collation name in any of the four quote styles is still read.
func TestColumnCollations_StillReadsAllFourCollateQuoteStyles(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE t (
			a TEXT COLLATE NOCASE,
			b TEXT COLLATE "RTRIM",
			c TEXT COLLATE [NOCASE],
			d TEXT COLLATE `+"`RTRIM`"+`,
			e TEXT COLLATE 'NOCASE'
		);`)
	got, err := ColumnCollations(db, "t")
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	want := map[string]string{"a": "NOCASE", "b": "RTRIM", "c": "NOCASE", "d": "RTRIM", "e": "NOCASE"}
	for col, w := range want {
		if got[col] != w {
			t.Errorf("ColumnCollations()[%q] = %q, want %q", col, got[col], w)
		}
	}
}
