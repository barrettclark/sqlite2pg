package sqlitereader

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestSampleColumn_ReturnsUpToLimitValues(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY, last_reported INTEGER);`)
	for i := 1; i <= 10; i++ {
		if _, err := db.Exec(`INSERT INTO bikes (last_reported) VALUES (?)`, 1620000000+i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	samples, err := SampleColumn(db, "bikes", "last_reported", 5)
	if err != nil {
		t.Fatalf("SampleColumn: %v", err)
	}
	if len(samples) != 5 {
		t.Fatalf("expected 5 samples, got %d", len(samples))
	}
	if _, ok := samples[0].(int64); !ok {
		t.Errorf("expected sampled value to be int64, got %T", samples[0])
	}
}

func TestSampleColumn_ReturnsFewerThanLimitWhenTableIsSmaller(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY, last_reported INTEGER);`)
	if _, err := db.Exec(`INSERT INTO bikes (last_reported) VALUES (1620000001)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	samples, err := SampleColumn(db, "bikes", "last_reported", 500)
	if err != nil {
		t.Fatalf("SampleColumn: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
}

func TestSampleColumn_SamplesAcrossTheWholeTableNotJustTheStart(t *testing.T) {
	// Regression test for a real bug: a table physically sorted so that
	// early rows all share one value (e.g. chinook.db's playlist_track,
	// sorted by PlaylistId) used to produce a sample that was entirely
	// that one value with a plain `LIMIT`, misleading heuristics like
	// boolean01 into thinking a column with real variety was binary.
	db := openTestDB(t, `CREATE TABLE sorted (id INTEGER PRIMARY KEY, val INTEGER);`)
	const total = 10000
	const tailStart = 9000 // last 10% of rows carry the minority value
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 1; i <= total; i++ {
		val := 1
		if i > tailStart {
			val = 2
		}
		if _, err := tx.Exec(`INSERT INTO sorted (val) VALUES (?)`, val); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	samples, err := SampleColumn(db, "sorted", "val", 500)
	if err != nil {
		t.Fatalf("SampleColumn: %v", err)
	}
	if len(samples) != 500 {
		t.Fatalf("expected 500 samples, got %d", len(samples))
	}

	// With a plain `LIMIT 500` against rows inserted in order, every
	// sample would be 1 — the minority value only appears in the last
	// 10% of the table. A random sample of 500 drawn from a population
	// that's 10% minority-valued has essentially no chance (0.9^500) of
	// missing the minority value entirely if it's actually scanning the
	// whole table, so seeing at least one 2 proves this isn't a plain
	// prefix scan.
	var sawMinorityValue bool
	for _, v := range samples {
		if n, ok := v.(int64); ok && n == 2 {
			sawMinorityValue = true
			break
		}
	}
	if !sawMinorityValue {
		t.Error("expected the sample to include at least one row from the table's minority-valued tail — " +
			"got a sample that looks like a plain prefix LIMIT instead of a random sample across the whole table")
	}
}

func TestSampleNonNullColumn_ReturnsOnlyNonNullValues(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE sparse (id INTEGER PRIMARY KEY, val TEXT);`)
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// 995 NULL rows, 5 real values scattered at the very end — the shape
	// that made ordinary random sampling miss the real values entirely.
	for i := 1; i <= 995; i++ {
		if _, err := tx.Exec(`INSERT INTO sparse (val) VALUES (NULL)`); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	for i := 1; i <= 5; i++ {
		if _, err := tx.Exec(`INSERT INTO sparse (val) VALUES (?)`, "real-value"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	samples, err := SampleNonNullColumn(db, "sparse", "val", 500)
	if err != nil {
		t.Fatalf("SampleNonNullColumn: %v", err)
	}
	if len(samples) != 5 {
		t.Fatalf("expected all 5 non-NULL values (fewer than the limit), got %d", len(samples))
	}
	for _, v := range samples {
		if v == nil {
			t.Error("expected no NULL values in the result")
		}
		if s, ok := v.(string); !ok || s != "real-value" {
			t.Errorf("expected %q, got %v", "real-value", v)
		}
	}
}

func TestSampleNonNullColumn_ReturnsEmptyForAnAllNullColumn(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE allnull (id INTEGER PRIMARY KEY, val TEXT);`)
	for i := 0; i < 10; i++ {
		if _, err := db.Exec(`INSERT INTO allnull (val) VALUES (NULL)`); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	samples, err := SampleNonNullColumn(db, "allnull", "val", 500)
	if err != nil {
		t.Fatalf("SampleNonNullColumn: %v", err)
	}
	if len(samples) != 0 {
		t.Fatalf("expected no values, got %d", len(samples))
	}
}

func TestSampleRows_ReturnsUpToLimitCompleteRowsInColumnOrder(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY, station_id TEXT, last_reported INTEGER);`)
	for i := 1; i <= 10; i++ {
		if _, err := db.Exec(`INSERT INTO bikes (station_id, last_reported) VALUES (?, ?)`, "s"+string(rune('0'+i%10)), 1620000000+i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	rows, err := SampleRows(db, "bikes", []string{"station_id", "last_reported"}, 3)
	if err != nil {
		t.Fatalf("SampleRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for _, row := range rows {
		if len(row) != 2 {
			t.Fatalf("expected 2 columns per row, got %d: %v", len(row), row)
		}
		if _, ok := row[0].(string); !ok {
			t.Errorf("expected column 0 (station_id) to be a string, got %T", row[0])
		}
		if _, ok := row[1].(int64); !ok {
			t.Errorf("expected column 1 (last_reported) to be an int64, got %T", row[1])
		}
	}
}

func TestSampleRows_ReturnsFewerThanLimitWhenTableIsSmaller(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY, station_id TEXT);`)
	if _, err := db.Exec(`INSERT INTO bikes (station_id) VALUES ('only-row')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := SampleRows(db, "bikes", []string{"station_id"}, 10)
	if err != nil {
		t.Fatalf("SampleRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestCountRows_ReturnsTheTableRowCount(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY);`)
	for i := 0; i < 7; i++ {
		if _, err := db.Exec(`INSERT INTO bikes DEFAULT VALUES`); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	n, err := CountRows(db, "bikes")
	if err != nil {
		t.Fatalf("CountRows: %v", err)
	}
	if n != 7 {
		t.Errorf("expected 7, got %d", n)
	}
}

func TestStreamTable_YieldsEveryRowWithoutBufferingTheWholeTable(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY, last_reported INTEGER);`)
	const rowCount = 50
	for i := 1; i <= rowCount; i++ {
		if _, err := db.Exec(`INSERT INTO bikes (last_reported) VALUES (?)`, 1620000000+i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	var seen int
	err := StreamTable(db, "bikes", []string{"bike_id", "last_reported"}, func(row []profiler.Value) error {
		seen++
		if len(row) != 2 {
			t.Fatalf("expected 2 values per row, got %d", len(row))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamTable: %v", err)
	}
	if seen != rowCount {
		t.Fatalf("expected to visit %d rows, visited %d", rowCount, seen)
	}
}

func TestStreamTableOrdered_YieldsRowsInOrderByColumnOrder(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER PRIMARY KEY, last_reported INTEGER);`)
	// Insert deliberately out of key order, so a plain sequential scan
	// (StreamTable) would very likely NOT come back sorted by bike_id,
	// proving StreamTableOrdered's ORDER BY is doing real work.
	inserts := []int{5, 1, 4, 2, 3}
	for _, id := range inserts {
		if _, err := db.Exec(`INSERT INTO bikes (bike_id, last_reported) VALUES (?, ?)`, id, 1620000000+id); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	var seen []int64
	err := StreamTableOrdered(db, "bikes", []string{"bike_id", "last_reported"}, []string{"bike_id"}, func(row []profiler.Value) error {
		seen = append(seen, row[0].(int64))
		return nil
	})
	if err != nil {
		t.Fatalf("StreamTableOrdered: %v", err)
	}
	want := []int64{1, 2, 3, 4, 5}
	if len(seen) != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), len(seen))
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("expected rows in bike_id order %v, got %v", want, seen)
		}
	}
}

func TestStreamTableOrdered_HandlesColumnAndTableNamesWithEmbeddedDoubleQuote(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE "stats ""weird""" (id INTEGER PRIMARY KEY, "Total ""Disability"" Recipients" INTEGER);`)
	if _, err := db.Exec(`INSERT INTO "stats ""weird""" (id, "Total ""Disability"" Recipients") VALUES (1, 42)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got []profiler.Value
	err := StreamTableOrdered(db, `stats "weird"`, []string{`Total "Disability" Recipients`}, []string{"id"}, func(row []profiler.Value) error {
		got = append(got, row[0])
		return nil
	})
	if err != nil {
		t.Fatalf("StreamTableOrdered: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
}

// TestStreamTable_DistinguishesZeroLengthBlobFromNull is a regression test
// for issue #56: sqlite3_column_blob() returns a NULL pointer for a
// zero-length BLOB even when the column is genuinely non-NULL (confirmed
// directly against modernc.org/sqlite in isolation before writing this
// test — the driver returns a nil []byte for both a true NULL and a
// zero-length non-NULL BLOB when scanning straight into []byte, but
// preserves the distinction when scanned into `any`: a true NULL comes
// back as an untyped nil interface, while a zero-length BLOB comes back as
// a non-nil interface wrapping a nil []byte). Without a fix, both a
// genuinely NULL blob and a genuine zero-length blob come back
// indistinguishable from this package's callers.
func TestStreamTable_DistinguishesZeroLengthBlobFromNull(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE blobs (id INTEGER PRIMARY KEY, b BLOB);`)
	if _, err := db.Exec(`INSERT INTO blobs (id, b) VALUES (1, X'00010203')`); err != nil {
		t.Fatalf("insert non-empty: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO blobs (id, b) VALUES (2, X'')`); err != nil {
		t.Fatalf("insert zero-length: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO blobs (id, b) VALUES (3, NULL)`); err != nil {
		t.Fatalf("insert null: %v", err)
	}

	var rows [][]profiler.Value
	err := StreamTableOrdered(db, "blobs", []string{"id", "b"}, []string{"id"}, func(row []profiler.Value) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamTableOrdered: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// Row 1: non-empty blob.
	b1, ok := rows[0][1].([]byte)
	if !ok {
		t.Fatalf("row 1: expected []byte, got %T", rows[0][1])
	}
	if string(b1) != "\x00\x01\x02\x03" {
		t.Errorf("row 1: expected 4-byte blob, got %v", b1)
	}

	// Row 2: genuine zero-length, non-NULL blob. Must NOT be NULL, and
	// must scan as a non-nil, zero-length []byte.
	if rows[1][1] == nil {
		t.Fatalf("row 2: zero-length non-NULL blob came back as NULL (interface nil) — issue #56")
	}
	b2, ok := rows[1][1].([]byte)
	if !ok {
		t.Fatalf("row 2: expected []byte, got %T", rows[1][1])
	}
	if b2 == nil {
		t.Errorf("row 2: expected non-nil zero-length []byte, got nil []byte — issue #56")
	}
	if len(b2) != 0 {
		t.Errorf("row 2: expected zero-length blob, got %d bytes", len(b2))
	}

	// Row 3: genuine NULL. Must remain NULL.
	if rows[2][1] != nil {
		t.Errorf("row 3: expected NULL, got %#v", rows[2][1])
	}
}
func TestSampleColumn_HandlesColumnAndTableNamesWithEmbeddedDoubleQuote(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE "stats ""weird""" (id INTEGER PRIMARY KEY, "Total ""Disability"" Recipients" INTEGER);`)
	if _, err := db.Exec(`INSERT INTO "stats ""weird""" ("Total ""Disability"" Recipients") VALUES (42)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	samples, err := SampleColumn(db, `stats "weird"`, `Total "Disability" Recipients`, 5)
	if err != nil {
		t.Fatalf("SampleColumn: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(samples))
	}
}

func TestStreamTable_HandlesColumnAndTableNamesWithEmbeddedDoubleQuote(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE "stats ""weird""" (id INTEGER PRIMARY KEY, "Total ""Disability"" Recipients" INTEGER);`)
	if _, err := db.Exec(`INSERT INTO "stats ""weird""" ("Total ""Disability"" Recipients") VALUES (42)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got []profiler.Value
	err := StreamTable(db, `stats "weird"`, []string{`Total "Disability" Recipients`}, func(row []profiler.Value) error {
		got = append(got, row[0])
		return nil
	})
	if err != nil {
		t.Fatalf("StreamTable: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
}
