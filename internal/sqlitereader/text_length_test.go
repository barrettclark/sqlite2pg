package sqlitereader

import "testing"

func TestMaxTextLength_ReturnsLongestValue(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (name TEXT);`)
	db.Exec(`INSERT INTO t (name) VALUES ('short'), ('a much longer value'), (NULL)`)

	max, ok, err := MaxTextLength(db, "t", "name")
	if err != nil {
		t.Fatalf("MaxTextLength: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true, at least one non-NULL row present")
	}
	if max != len("a much longer value") {
		t.Errorf("expected max=%d, got %d", len("a much longer value"), max)
	}
}

func TestMaxTextLength_NotOKWhenEveryValueIsNull(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (name TEXT);`)
	db.Exec(`INSERT INTO t (name) VALUES (NULL), (NULL)`)

	_, ok, err := MaxTextLength(db, "t", "name")
	if err != nil {
		t.Fatalf("MaxTextLength: %v", err)
	}
	if ok {
		t.Error("expected ok=false when every row is NULL")
	}
}

func TestMaxTextLength_CountsCharactersNotBytesForABlobRow(t *testing.T) {
	// A BLOB row in a column otherwise holding TEXT (SQLite's dynamic
	// typing permits this — the same shape issue #83 found) must still be
	// measured in characters, matching what Postgres's varchar(N) would
	// actually enforce, not in raw bytes (Copilot PR #96 finding). Without
	// CAST(... AS TEXT), LENGTH() on this BLOB returns 3 (its byte count);
	// with it, 1 (its single-character content) — the value the test
	// checks for.
	db := openTestDB(t, `CREATE TABLE t (v);`)
	// x'e282ac' is the 3-byte UTF-8 encoding of '€' — 1 character.
	db.Exec(`INSERT INTO t (v) VALUES (x'e282ac')`)

	max, ok, err := MaxTextLength(db, "t", "v")
	if err != nil {
		t.Fatalf("MaxTextLength: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if max != 1 {
		t.Errorf("expected max=1 (character count of the BLOB row, not its 3-byte length), got %d", max)
	}
}

func TestMaxTextLength_CountsCharactersNotBytesForMultibyteText(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (name TEXT);`)
	// 5 multi-byte characters — Postgres's varchar(N) counts characters,
	// matching SQLite's LENGTH() on TEXT (not the byte count octet_length
	// would give).
	db.Exec(`INSERT INTO t (name) VALUES ('héllo')`)

	max, ok, err := MaxTextLength(db, "t", "name")
	if err != nil {
		t.Fatalf("MaxTextLength: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if max != 5 {
		t.Errorf("expected max=5 (character count), got %d", max)
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
