package sqlitereader

import (
	"strings"
	"testing"
)

// TestMatchingParen_IgnoresParensInStringLiteralsAndComments is issue
// #104's (M1) regression. PR #101 fixed where the column-definition list
// *starts* (columnListOpenParen); nothing fixed where it *ends*.
// matchingParen counted every '(' and ')' byte, including ones inside a
// string literal or a -- / /* */ comment — so a DEFAULT ')' or an
// unbalanced paren in a comment truncated the parsed body, dropping a
// later column's COLLATE clause and reporting it as BINARY.
func TestMatchingParen_IgnoresParensInStringLiteralsAndComments(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		{"paren in string literal", `(note TEXT DEFAULT ')', id TEXT)`},
		{"paren in line comment", "(note TEXT, -- free-form (see notes\n id TEXT)"},
		{"paren in CR-terminated line comment", "(note TEXT, -- free-form (see notes\r id TEXT)"},
		{"paren in CRLF-terminated line comment", "(note TEXT, -- free-form (see notes\r\n id TEXT)"},
		{"paren in block comment", "(note TEXT, /* a ( here */ id TEXT)"},
		{"quoted identifier containing a paren", `("we(ird" TEXT, id TEXT)`},
	}
	for _, c := range cases {
		close := matchingParen(c.s, 0)
		if close != len(c.s)-1 {
			t.Errorf("%s: matchingParen(%q, 0) = %d, want %d (the final ')')", c.name, c.s, close, len(c.s)-1)
		}
	}
}

// TestColumnCollations_UnbalancedParenInStringOrCommentDoesNotHideACollation
// is issue #104's end-to-end consequence: a COLLATE NOCASE primary key
// misreported as BINARY makes primaryKeyOrderingIsSafe return true, and
// verifyTableOrdered then walks the two sides in genuinely different
// orders (Postgres byte order vs SQLite NOCASE).
func TestColumnCollations_UnbalancedParenInStringOrCommentDoesNotHideACollation(t *testing.T) {
	cases := map[string]string{
		"string literal default": `
			CREATE TABLE t (
				note TEXT DEFAULT ')',
				id   TEXT PRIMARY KEY COLLATE NOCASE
			);`,
		"line comment": `
			CREATE TABLE t (
				note TEXT,          -- free-form (see the import notes
				id   TEXT PRIMARY KEY COLLATE NOCASE
			);`,
	}
	for label, ddl := range cases {
		db := openTestDB(t, ddl)
		got, err := ColumnCollations(db, "t")
		if err != nil {
			t.Fatalf("%s: ColumnCollations: %v", label, err)
		}
		if got["id"] != "NOCASE" {
			t.Errorf("%s: ColumnCollations()[\"id\"] = %q, want \"NOCASE\"", label, got["id"])
		}
	}
}

// TestLeadingIdentifier_HandlesDoubledQuoteEscape is issue #113's (L4)
// correctness hole: SQLite spells an embedded quote in a quoted identifier
// by doubling it, so `"foo""(bar"` is the identifier foo"(bar.
// leadingIdentifier stopped at the first inner '"', returning name `foo`
// and leaving `"(bar" ...` as the remainder — so columnListOpenParen then
// found the '(' inside the name again (issue #91 reproduced for a
// doubled-quote name).
func TestLeadingIdentifier_HandlesDoubledQuoteEscape(t *testing.T) {
	cases := []struct {
		s, wantName, wantRest string
	}{
		{`"foo""bar" TEXT`, `foo"bar`, ` TEXT`},
		{`"foo""(bar" TEXT`, `foo"(bar`, ` TEXT`},
		{"`a``b` INT", "a`b", " INT"},
		{`"plain" REST`, "plain", " REST"},
	}
	for _, c := range cases {
		name, rest, ok := leadingIdentifier(c.s)
		if !ok || name != c.wantName || rest != c.wantRest {
			t.Errorf("leadingIdentifier(%q) = (%q, %q, %v), want (%q, %q, true)",
				c.s, name, rest, ok, c.wantName, c.wantRest)
		}
	}
}

// TestColumnCollations_TableNameWithDoubledQuoteAndParen is #113's
// end-to-end case: a table named foo"(bar must not confuse the
// column-list parser.
func TestColumnCollations_TableNameWithDoubledQuoteAndParen(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE "foo""(bar" (
			name TEXT COLLATE NOCASE,
			bio  TEXT
		);
	`)
	got, err := ColumnCollations(db, `foo"(bar`)
	if err != nil {
		t.Fatalf("ColumnCollations: %v", err)
	}
	if got["name"] != "NOCASE" {
		t.Errorf("ColumnCollations()[\"name\"] = %q, want \"NOCASE\"", got["name"])
	}
	if got["bio"] != "BINARY" {
		t.Errorf("ColumnCollations()[\"bio\"] = %q, want \"BINARY\"", got["bio"])
	}
}

// TestColumnListOpenParen_ReturnsNoListForVirtualTable is #113's contract
// point: a CREATE VIRTUAL TABLE statement has no column-definition list —
// the parens after USING <module> are the module's argument list, not
// column definitions. columnListOpenParen must return -1 so
// parseColumnCollations doesn't parse the module args as columns.
// (Supersedes the old TestColumnListOpenParen_HandlesVirtualTableWithParenInName,
// which locked in the previous "return the fts5( paren" behavior.)
func TestColumnListOpenParen_ReturnsNoListForVirtualTable(t *testing.T) {
	for _, s := range []string{
		`CREATE VIRTUAL TABLE docs USING fts5(title, body)`,
		`CREATE VIRTUAL TABLE "foo(bar)" USING fts5(name, bio)`,
		`CREATE VIRTUAL TABLE t USING rtree(id, minX, maxX)`,
	} {
		if got := columnListOpenParen(s); got != -1 {
			t.Errorf("columnListOpenParen(%q) = %d, want -1 (virtual table, no column list)", s, got)
		}
	}
}

// TestColumnListOpenParen_StillFindsListForOrdinaryTables guards the
// virtual-table change against over-reach.
func TestColumnListOpenParen_StillFindsListForOrdinaryTables(t *testing.T) {
	cases := []string{
		`CREATE TABLE t (a INT, b TEXT)`,
		`CREATE TABLE "t" (a INT)`,
		`CREATE TABLE IF NOT EXISTS main.t (a INT)`,
		`CREATE TABLE "foo(bar)" (a INT)`,
		"CREATE TABLE t -- note: (ignore this paren)\n(a INT)",
		"CREATE TABLE t /* and ( this one */ (a INT)",
	}
	for _, s := range cases {
		got := columnListOpenParen(s)
		want := strings.Index(s, "(a") // the '(' that opens the column list
		if got != want {
			t.Errorf("columnListOpenParen(%q) = %d, want %d", s, got, want)
		}
	}
}
