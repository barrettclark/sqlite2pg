package sqlitereader

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzParseColumnCollations hammers the hand-rolled CREATE TABLE parser
// (matchingParen / splitTopLevelCommas / leadingIdentifier / the COLLATE
// regex) with arbitrary text. It must never panic — a corrupt or
// surprising sqlite_master.sql should degrade to "no COLLATE found", never
// crash the profiler — and every collation it does report must be a
// non-empty, already-uppercased token (ColumnCollations stores these
// directly and compares them by ==).
func FuzzParseColumnCollations(f *testing.F) {
	f.Add("CREATE TABLE t (a TEXT COLLATE NOCASE, b INT)")
	f.Add("CREATE TABLE t (a TEXT COLLATE \"weird name\", b TEXT COLLATE [RTRIM])")
	f.Add("CREATE TABLE t (a TEXT CHECK (a COLLATE NOCASE <> ''), b TEXT)")
	f.Add("CREATE TABLE t (")
	f.Add("(((")
	f.Add("CREATE TABLE t (a TEXT COLLATE)")
	f.Add("CREATE TABLE \"t\" (\"a,b\" TEXT COLLATE RTRIM)")
	f.Add("")

	f.Fuzz(func(t *testing.T, createSQL string) {
		got := parseColumnCollations(createSQL) // must not panic

		for name, coll := range got {
			if name == "" {
				// leadingIdentifier now rejects an empty quoted identifier
				// (`""`, ``, `[]`), so parseColumnCollations must never
				// key an entry by the empty string (issue #70).
				t.Fatalf("parseColumnCollations returned an empty column name (collation %q) for input %q", coll, createSQL)
			}
			if coll == "" {
				t.Fatalf("parseColumnCollations returned an empty collation for column %q, input %q", name, createSQL)
			}
			if up := strings.ToUpper(coll); up != coll {
				t.Fatalf("parseColumnCollations returned a non-uppercased collation %q (want %q) for column %q, input %q",
					coll, up, name, createSQL)
			}
		}
	})
}

// FuzzColumnCollationsRoundTrip builds a real, valid CREATE TABLE from
// structured fuzz input, runs it through an actual SQLite database, and
// asserts ColumnCollations reports back exactly what was declared for
// every straightforwardly-declared column. This is the property
// verifyTableOrdered's correctness depends on: it forces Postgres to
// COLLATE "C" only for columns it believes are BINARY, so a
// mis-detected NOCASE/RTRIM column (reported as BINARY) would make verify
// compare the two sides in genuinely different orders.
func FuzzColumnCollationsRoundTrip(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{2, 2, 2, 2, 2, 2})
	f.Add([]byte{5, 6, 7, 4})
	f.Add([]byte{})

	// index -> (clause appended after the type, expected reported collation)
	variants := []struct {
		clause   string
		expected string
	}{
		{"", "BINARY"},
		{" COLLATE BINARY", "BINARY"},
		{" COLLATE binary", "BINARY"},
		{" COLLATE NOCASE", "NOCASE"},
		{" COLLATE nocase", "NOCASE"},
		{" COLLATE RTRIM", "RTRIM"},
		{` COLLATE "NOCASE"`, "NOCASE"},
		{" COLLATE [RTRIM]", "RTRIM"},
		{" COLLATE `NOCASE`", "NOCASE"},
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) == 0 {
			return
		}
		n := len(raw)
		if n > 24 {
			n = 24
		}

		var defs []string
		want := map[string]string{}
		for i := 0; i < n; i++ {
			col := fmt.Sprintf("col_%d", i)
			v := variants[int(raw[i])%len(variants)]
			defs = append(defs, fmt.Sprintf("%q TEXT%s", col, v.clause))
			want[col] = v.expected
		}
		// A table-level constraint clause exercises the parser's
		// "leading identifier isn't a real column" filter.
		if n%2 == 0 {
			defs = append(defs, `PRIMARY KEY ("col_0")`)
		}

		ddl := "CREATE TABLE roundtrip (\n  " + strings.Join(defs, ",\n  ") + "\n);"

		db := openTestDB(t, ddl)
		got, err := ColumnCollations(db, "roundtrip")
		if err != nil {
			t.Fatalf("ColumnCollations: %v\nddl:\n%s", err, ddl)
		}

		for col, wantColl := range want {
			if got[col] != wantColl {
				t.Fatalf("ColumnCollations()[%q] = %q, want %q\nddl:\n%s\nfull result: %+v",
					col, got[col], wantColl, ddl, got)
			}
		}
	})
}

// FuzzColumnCollationsRoundTripWithNoise is issue #104's (M1) property,
// through a real SQLite database: an unbalanced paren, comma, or COLLATE
// keyword sitting inside a string-literal DEFAULT or a -- / /* */ comment
// on an earlier column must not stop ColumnCollations from reporting a
// later column's genuine COLLATE clause. The noise is placed on col_0,
// which always has no real COLLATE; col_1 always carries COLLATE NOCASE
// and must round-trip.
func FuzzColumnCollationsRoundTripWithNoise(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(2)
	f.Add(3)
	f.Add(4)

	noises := []string{
		`DEFAULT ')'`,
		`DEFAULT '('`,
		`DEFAULT ',,,'`,
		`DEFAULT 'COLLATE RTRIM'`,
		`/* a ( ) , COLLATE RTRIM here */`,
	}
	// Line comments need a newline terminator; handled separately.
	lineComment := "-- trailing ( , COLLATE RTRIM to end of line\n"

	f.Fuzz(func(t *testing.T, sel int) {
		if sel < 0 {
			sel = -sel
		}
		noise := noises[sel%len(noises)]

		ddl := "CREATE TABLE rt (\n" +
			"  col_0 TEXT " + noise + ",\n" +
			"  " + lineComment +
			"  col_1 TEXT COLLATE NOCASE,\n" +
			"  col_2 TEXT\n" +
			");"

		db := openTestDB(t, ddl)
		got, err := ColumnCollations(db, "rt")
		if err != nil {
			t.Fatalf("ColumnCollations: %v\nddl:\n%s", err, ddl)
		}
		if got["col_1"] != "NOCASE" {
			t.Fatalf("ColumnCollations()[\"col_1\"] = %q, want \"NOCASE\" — noise on an earlier column hid a real COLLATE (issue #104)\nddl:\n%s\nfull result: %+v",
				got["col_1"], ddl, got)
		}
		if got["col_2"] != "BINARY" {
			t.Fatalf("ColumnCollations()[\"col_2\"] = %q, want \"BINARY\"\nddl:\n%s", got["col_2"], ddl)
		}
	})
}

// FuzzMatchingParenNeverCountsQuotedOrCommentedParens asserts the two
// structural invariants matchingParen must hold for any input (issue
// #104): its result is either -1 or an index that actually holds ')', and
// a '(' / ')' inside a quoted span or a comment never shifts that result.
func FuzzMatchingParenNeverCountsQuotedOrCommentedParens(f *testing.F) {
	f.Add("(a, b)")
	f.Add("(a DEFAULT ')', b)")
	f.Add("(a, -- )\n b)")
	f.Add("(a /* ) */ , b)")
	f.Add("(")
	f.Add(")")
	f.Add("")

	f.Fuzz(func(t *testing.T, s string) {
		open := strings.IndexByte(s, '(')
		if open < 0 {
			return
		}
		got := matchingParen(s, open) // must not panic
		if got == -1 {
			return
		}
		if got < 0 || got >= len(s) || s[got] != ')' {
			t.Fatalf("matchingParen(%q, %d) = %d, which is not the index of a ')'", s, open, got)
		}
	})
}
