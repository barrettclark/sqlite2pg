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
