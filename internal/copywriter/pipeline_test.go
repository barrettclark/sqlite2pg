package copywriter

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
)

func openTestDB(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("exec ddl: %v", err)
	}
	return db
}

func TestTableSource_StreamsAllRowsWithTransformsApplied(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER, last_reported INTEGER, is_installed INTEGER);`)
	rows := [][3]int64{{1, 1620000000, 1}, {2, 1620003600, 0}, {3, 1620007200, 1}}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO bikes VALUES (?, ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"bike_id", "last_reported", "is_installed"},
		Columns: map[string]config.ColumnConfig{
			"bike_id":       {TargetType: "integer"},
			"last_reported": {TargetType: "timestamptz", Transform: "unix_epoch_seconds"},
			"is_installed":  {TargetType: "boolean", Transform: "int_to_bool"},
		},
	}

	src := NewTableSource(db, "bikes", tc)

	var seen int
	for src.Next() {
		vals, err := src.Values()
		if err != nil {
			t.Fatalf("Values: %v", err)
		}
		if len(vals) != 3 {
			t.Fatalf("expected 3 values, got %d: %v", len(vals), vals)
		}
		if _, ok := vals[1].(interface{ Unix() int64 }); !ok {
			// last_reported should have been transformed to a time.Time
			t.Errorf("expected last_reported to be a time value, got %T", vals[1])
		}
		if _, ok := vals[2].(bool); !ok {
			t.Errorf("expected is_installed to be a bool, got %T", vals[2])
		}
		seen++
	}
	if err := src.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if seen != len(rows) {
		t.Fatalf("expected to stream %d rows, got %d", len(rows), seen)
	}
}

func TestTableSource_OnRowFiresOncePerRowReturnedByNext(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER);`)
	for i := 0; i < 5; i++ {
		if _, err := db.Exec(`INSERT INTO bikes VALUES (?)`, i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	tc := config.TableConfig{
		ColumnOrder: []string{"bike_id"},
		Columns:     map[string]config.ColumnConfig{"bike_id": {TargetType: "integer"}},
	}

	var calls int
	src := NewTableSource(db, "bikes", tc).OnRow(func() { calls++ })
	var seen int
	for src.Next() {
		seen++
	}
	if err := src.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if calls != seen {
		t.Errorf("expected OnRow to fire once per row (%d), fired %d times", seen, calls)
	}
	if calls != 5 {
		t.Errorf("expected 5 calls, got %d", calls)
	}
}

func TestTableSource_OnRowIsOptional(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE bikes (bike_id INTEGER);`)
	if _, err := db.Exec(`INSERT INTO bikes VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	tc := config.TableConfig{
		ColumnOrder: []string{"bike_id"},
		Columns:     map[string]config.ColumnConfig{"bike_id": {TargetType: "integer"}},
	}

	// No OnRow registered — must not panic.
	src := NewTableSource(db, "bikes", tc)
	for src.Next() {
	}
	if err := src.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
}

func TestTableSource_ExcludesDroppedColumns(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (a INTEGER, shape BLOB);`)
	if _, err := db.Exec(`INSERT INTO t VALUES (1, x'00')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"a", "shape"},
		Columns: map[string]config.ColumnConfig{
			"a":     {TargetType: "integer"},
			"shape": {TargetType: "__drop__"},
		},
	}

	src := NewTableSource(db, "t", tc)
	if !src.Next() {
		t.Fatalf("expected at least one row, Err: %v", src.Err())
	}
	vals, err := src.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(vals) != 1 {
		t.Fatalf("expected dropped column excluded, got %d values: %v", len(vals), vals)
	}
}

func TestTableSource_SurfacesTransformErrorsViaErr(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (n TEXT);`)
	if _, err := db.Exec(`INSERT INTO t VALUES ('not-a-number')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"n"},
		Columns: map[string]config.ColumnConfig{
			"n": {TargetType: "integer", Transform: "strip_commas"},
		},
	}

	src := NewTableSource(db, "t", tc)
	for src.Next() {
		// drain
	}
	if src.Err() == nil {
		t.Fatal("expected a transform error to surface via Err()")
	}
}

func TestTableSource_TransformErrorNamesTheTableColumnAndSuggestsAFix(t *testing.T) {
	// Issue #13: a transform failure at load time (residual case a
	// profile-time full-table check can't cover, e.g. --force past a
	// flagged column) previously surfaced only the raw transform error
	// with no indication of which column, or what to do about it.
	db := openTestDB(t, `CREATE TABLE albums (mb_id TEXT);`)
	if _, err := db.Exec(`INSERT INTO albums VALUES ('not-a-uuid')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"mb_id"},
		Columns: map[string]config.ColumnConfig{
			"mb_id": {TargetType: "uuid", Transform: "uuid_format"},
		},
	}

	src := NewTableSource(db, "albums", tc)
	for src.Next() {
	}
	err := src.Err()
	if err == nil {
		t.Fatal("expected a transform error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "albums.mb_id") {
		t.Errorf("expected the error to name albums.mb_id, got %q", msg)
	}
	if !strings.Contains(msg, "--sample-size") {
		t.Errorf("expected the error to suggest re-profiling with a larger --sample-size, got %q", msg)
	}
}

// Issue #28: once Next() has returned false and recorded a real error via
// Err(), a subsequent call to Next() (e.g. a caller re-checking after
// already seeing false) must not discard that recorded error.
func TestTableSource_NextDoesNotClobberARecordedErrorOnSubsequentCalls(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (n TEXT);`)
	if _, err := db.Exec(`INSERT INTO t VALUES ('not-a-number')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"n"},
		Columns: map[string]config.ColumnConfig{
			"n": {TargetType: "integer", Transform: "strip_commas"},
		},
	}

	src := NewTableSource(db, "t", tc)
	for src.Next() {
	}
	first := src.Err()
	if first == nil {
		t.Fatal("expected a transform error to surface via Err()")
	}

	if src.Next() {
		t.Fatal("expected Next() to keep returning false once the pipeline is exhausted")
	}
	second := src.Err()
	if second == nil {
		t.Fatal("Next() called again after exhaustion clobbered the recorded error with nil")
	}
	if second.Error() != first.Error() {
		t.Fatalf("Err() changed across a subsequent Next() call: first %q, second %q", first, second)
	}
}

// Issue #28: if the consumer stops calling Next() early (as pgx does when a
// COPY fails server-side mid-table) the producer goroutine must not be left
// permanently blocked sending on the full rowsCh — it should unblock and
// exit once the source is closed, allowing StreamTable's deferred
// rows.Close() to run and release the SQLite cursor.
func TestTableSource_CloseUnblocksTheProducerGoroutine(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (n INTEGER);`)
	// Far more rows than the 100-slot rowsCh buffer, so the producer is
	// guaranteed to still be blocked on a send when we stop consuming.
	const total = 500
	for i := 0; i < total; i++ {
		if _, err := db.Exec(`INSERT INTO t VALUES (?)`, i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	tc := config.TableConfig{
		ColumnOrder: []string{"n"},
		Columns:     map[string]config.ColumnConfig{"n": {TargetType: "integer"}},
	}

	src := NewTableSource(db, "t", tc)
	// Pull far fewer rows than were inserted, then stop — simulating pgx
	// abandoning Next() after a mid-COPY failure.
	const consumed = 5
	for i := 0; i < consumed; i++ {
		if !src.Next() {
			t.Fatalf("expected at least %d rows, Err: %v", consumed, src.Err())
		}
	}

	src.Close()

	// Drain whatever the producer already had buffered/in-flight, without
	// calling Next() again (the real-world caller never touches the
	// source again after abandoning it mid-COPY). A producer that
	// actually honors Close() exits after at most a bufferful more rows;
	// one that ignores it just streams the rest of the table to
	// completion, since nothing else in sqlitereader stops it.
	drained := 0
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case _, ok := <-src.rowsCh:
			if !ok {
				break loop
			}
			drained++
		case <-timeout:
			t.Fatalf("producer goroutine never exited: rowsCh did not close within timeout (drained %d rows)", drained)
		}
	}

	if remaining := total - consumed; drained >= remaining {
		t.Fatalf("producer streamed all %d remaining rows after Close() instead of exiting early (goroutine/cursor leak not fixed)", remaining)
	}
}

// Issue #54: Close() is a bare close(ts.done) with no guard, so a second
// call panics on an already-closed channel. Nothing in the current code
// calls Close() twice, but the doc comment doesn't say it's single-use, so
// a future defensive caller (e.g. an error path calling Close() in addition
// to a defer) would panic. Close must tolerate being called more than once,
// mirroring internal/review/state.go's doneOnce pattern.
func TestTableSource_CloseIsIdempotent(t *testing.T) {
	db := openTestDB(t, `CREATE TABLE t (n INTEGER);`)
	tc := config.TableConfig{
		ColumnOrder: []string{"n"},
		Columns:     map[string]config.ColumnConfig{"n": {TargetType: "integer"}},
	}

	src := NewTableSource(db, "t", tc)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("calling Close() twice panicked: %v", r)
		}
	}()

	src.Close()
	src.Close()
}
