package sqlitereader

import "testing"

// These tests were written against the former MaxTextLength (singular),
// deleted in issue #118 (L9) as dead outside its own tests. They now
// exercise the same behaviours through MaxTextLengths, the only form
// production code calls — absence from the result map is the "not ok"
// (all-NULL) signal the singular form returned as a bool.

func TestMaxTextLengths_ReturnsLongestValue(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (name TEXT);`)
	db.Exec(`INSERT INTO t (name) VALUES ('short'), ('a much longer value'), (NULL)`)

	got, err := MaxTextLengths(db, "t", []string{"name"})
	if err != nil {
		t.Fatalf("MaxTextLengths: %v", err)
	}
	if got["name"] != len("a much longer value") {
		t.Errorf("expected name=%d, got %d", len("a much longer value"), got["name"])
	}
}

func TestMaxTextLengths_NoEntryWhenEveryValueIsNull(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (name TEXT);`)
	db.Exec(`INSERT INTO t (name) VALUES (NULL), (NULL)`)

	got, err := MaxTextLengths(db, "t", []string{"name"})
	if err != nil {
		t.Fatalf("MaxTextLengths: %v", err)
	}
	if _, ok := got["name"]; ok {
		t.Errorf("expected no entry for an all-NULL column, got %d", got["name"])
	}
}

func TestMaxTextLengths_CountsCharactersNotBytesForABlobRow(t *testing.T) {
	// A BLOB row in a column otherwise holding TEXT (SQLite's dynamic
	// typing permits this — the same shape issue #83 found) must still be
	// measured in characters, matching what Postgres's varchar(N) would
	// actually enforce, not in raw bytes (Copilot PR #96 finding). Without
	// CAST(... AS TEXT), LENGTH() on this BLOB returns 3 (its byte count);
	// with it, 1 (its single-character content).
	db := openTestDB(t, `CREATE TABLE t (v);`)
	// x'e282ac' is the 3-byte UTF-8 encoding of '€' — 1 character.
	db.Exec(`INSERT INTO t (v) VALUES (x'e282ac')`)

	got, err := MaxTextLengths(db, "t", []string{"v"})
	if err != nil {
		t.Fatalf("MaxTextLengths: %v", err)
	}
	if got["v"] != 1 {
		t.Errorf("expected v=1 (character count of the BLOB row, not its 3-byte length), got %d", got["v"])
	}
}

func TestMaxTextLengths_CountsCharactersNotBytesForMultibyteText(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (name TEXT);`)
	// 5 multi-byte characters — Postgres's varchar(N) counts characters,
	// matching SQLite's LENGTH() on TEXT (not the byte count octet_length
	// would give).
	db.Exec(`INSERT INTO t (name) VALUES ('héllo')`)

	got, err := MaxTextLengths(db, "t", []string{"name"})
	if err != nil {
		t.Fatalf("MaxTextLengths: %v", err)
	}
	if got["name"] != 5 {
		t.Errorf("expected name=5 (character count), got %d", got["name"])
	}
}

func TestMaxTextLengths_ComputesEveryColumnInOneScan(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (a TEXT, b TEXT, c TEXT);`)
	db.Exec(`INSERT INTO t (a, b, c) VALUES ('short', 'a much longer value', NULL)`)
	db.Exec(`INSERT INTO t (a, b, c) VALUES ('bit longer', 'x', NULL)`)

	got, err := MaxTextLengths(db, "t", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("MaxTextLengths: %v", err)
	}
	want := map[string]int{"a": len("bit longer"), "b": len("a much longer value")}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for col, n := range want {
		if got[col] != n {
			t.Errorf("column %q: got %d, want %d", col, got[col], n)
		}
	}
	if _, ok := got["c"]; ok {
		t.Errorf("expected no entry for all-NULL column c, got %d", got["c"])
	}
}

func TestMaxTextLengths_EmptyColumnListReturnsEmptyMap(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (a TEXT);`)
	got, err := MaxTextLengths(db, "t", nil)
	if err != nil {
		t.Fatalf("MaxTextLengths: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}
