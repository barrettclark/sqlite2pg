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
