package sqlitereader

import (
	"strings"
	"testing"
)

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

// TestLeadingIdentifier_RejectsAnEmptyQuotedIdentifier covers issue #70:
// an empty quoted identifier ("", ``, []) is not a valid leading
// identifier — leadingIdentifier used to return ok=true with an empty
// name, letting parseColumnCollations emit a ""-keyed entry from
// malformed CREATE TABLE text.
func TestLeadingIdentifier_RejectsAnEmptyQuotedIdentifier(t *testing.T) {
	for _, s := range []string{`""COLLATE 0`, "``rest", `[]rest`, `""`, "``", `[]`} {
		if name, _, ok := leadingIdentifier(s); ok {
			t.Errorf("leadingIdentifier(%q) = (%q, _, true), want ok=false for an empty quoted identifier", s, name)
		}
	}
}

// TestLeadingIdentifier_StillAcceptsNonEmptyQuotedIdentifiers is the guard:
// a normal quoted name must still parse.
func TestLeadingIdentifier_StillAcceptsNonEmptyQuotedIdentifiers(t *testing.T) {
	cases := map[string]string{
		`"a b" TEXT`: "a b",
		"`x` INT":    "x",
		`[y] REAL`:   "y",
	}
	for s, want := range cases {
		if name, _, ok := leadingIdentifier(s); !ok || name != want {
			t.Errorf("leadingIdentifier(%q) = (%q, _, %v), want (%q, _, true)", s, name, ok, want)
		}
	}
}

// TestParseColumnCollations_NoEmptyKeyFromMalformedInput is the
// end-of-issue-#70 check on the corpus repro: parseColumnCollations must
// not produce a ""-keyed entry.
func TestParseColumnCollations_NoEmptyKeyFromMalformedInput(t *testing.T) {
	got := parseColumnCollations(`(""COLLATE 0`)
	if _, hasEmpty := got[""]; hasEmpty {
		t.Errorf("parseColumnCollations produced a \"\"-keyed entry: %+v", got)
	}
}

// TestColumnCollations_TableNameContainingAParenDoesNotConfuseTheParser is
// issue #91's (audit finding L5) regression: parseColumnCollations used to
// find the column-definition list's opening '(' by searching for the
// first '(' anywhere in the CREATE TABLE text — but a table literally
// named with one (a valid, if unusual, quoted identifier) makes that
// search match the '(' inside the quoted table name instead, parsing the
// column-definition body from the wrong offset and silently leaving every
// column at its BINARY default.
func TestColumnCollations_TableNameContainingAParenDoesNotConfuseTheParser(t *testing.T) {
	db := openTestDB(t, `
		CREATE TABLE "foo(bar)" (
			name TEXT COLLATE NOCASE,
			bio TEXT
		);
	`)

	got, err := ColumnCollations(db, "foo(bar)")
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

// TestColumnListOpenParen_HandlesVirtualTableWithParenInName is a
// regression test for Copilot's PR #101 review finding: ColumnCollations'
// own sqlite_master query (type = 'table') matches virtual tables too —
// SQLite gives them type='table' there, not a separate type — so a CREATE
// VIRTUAL TABLE statement can reach columnListOpenParen just as a plain
// one can. Without matching that preamble shape, the paren-in-table-name
// bug this helper exists to avoid (issue #91) reproduces identically for
// a virtual table literally named with one.
func TestColumnListOpenParen_HandlesVirtualTableWithParenInName(t *testing.T) {
	sql := `CREATE VIRTUAL TABLE "foo(bar)" USING fts5(name, bio)`
	want := strings.Index(sql, "USING fts5(") + len("USING fts5")
	got := columnListOpenParen(sql)
	if got != want {
		t.Errorf("columnListOpenParen(%q) = %d, want %d (the '(' after USING fts5, not the one inside the quoted table name)", sql, got, want)
	}
}
