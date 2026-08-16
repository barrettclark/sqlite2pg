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
