//go:build integration

// Tier 3 (real Postgres): identity reseed failures on the fresh and resume
// paths. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run 'TestFresh_|TestResume_' -v
package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
)

// overflowIntegerSetup leaves a source whose sqlite_sequence high-water mark
// is 2147483648, past the int4 identity maximum, with one live row.
var overflowIntegerSetup = []string{
	`INSERT INTO t (id, label) VALUES (1,'a')`,
	`INSERT INTO t (id, label) VALUES (2147483648,'a')`,
	`DELETE FROM t WHERE id = 2147483648`,
}

// withStderr runs fn with os.Stderr redirected and returns what was written.
func withStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatalf("creating stderr capture: %v", err)
	}
	orig := os.Stderr
	os.Stderr = f
	ferr := fn()
	os.Stderr = orig
	f.Close()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("reading stderr capture: %v", err)
	}
	return string(b), ferr
}

func pgConnFor(t *testing.T, connCfg *pgx.ConnConfig) *pgx.Conn {
	t.Helper()
	conn, err := pgx.ConnectConfig(context.Background(), connCfg)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}

// TestFresh_HighWaterAboveIdentityRangeLoadsNothing: a fresh load whose
// high-water mark can't fit the identity fails before any DDL or COPY, so
// the table is absent, unmarked, and every retry fails the same way.
func TestFresh_HighWaterAboveIdentityRangeLoadsNothing(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), overflowIntegerSetup)
	conn := pgConnFor(t, connCfg)

	for _, resume := range []bool{false, true, true} {
		err := executeLoad(cfg, connCfg, resume, statePath)
		if err == nil {
			t.Fatalf("resume=%v: expected range error, got nil", resume)
		}
		for _, want := range []string{"t.id", "2147483648", "2147483647"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("resume=%v: error should contain %q, got: %v", resume, want, err)
			}
		}
		var exists bool
		if err := conn.QueryRow(context.Background(), `SELECT to_regclass('"t"') IS NOT NULL`).Scan(&exists); err != nil {
			t.Fatalf("checking for t: %v", err)
		}
		if exists {
			t.Fatalf("resume=%v: t was created despite the range error", resume)
		}
		done, err := loadCompletedTables(statePath)
		if err != nil {
			t.Fatalf("reading state: %v", err)
		}
		if done["t"] {
			t.Fatalf("resume=%v: t marked completed despite the range error", resume)
		}
	}
}

// TestResume_CompletedTableWarnsOnRangeAndFinishes: a table completed earlier
// whose source high-water mark now exceeds the identity's range must warn,
// not abort, and the resume must still reach the foreign key step.
func TestResume_CompletedTableWarnsOnRangeAndFinishes(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), fiveRowsSetup)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	overflowSource(t, cfg.Source.Path)

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume should finish despite the reseed warning, got: %v", err)
	}
	assertOverflowWarning(t, stderr, "sequence set to 2147483647")
	assertFKsApplied(t, statePath)
	assertInsertExhausted(t, connCfg)
}

// TestResume_HasRowsUnmarkedOverflowMarksCompleted: a table with rows but no
// state entry (a run that died after COPY) whose high-water mark overflows
// must warn, mark the table completed, run the FK step, and advance the
// sequence to the loaded maximum.
func TestResume_HasRowsUnmarkedOverflowMarksCompleted(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), fiveRowsSetup)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("clearing state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	var seq string
	if err := conn.QueryRow(context.Background(), `SELECT pg_get_serial_sequence('"t"', 'id')`).Scan(&seq); err != nil {
		t.Fatalf("looking up sequence: %v", err)
	}
	if _, err := conn.Exec(context.Background(), `SELECT setval($1::regclass, 1, true)`, seq); err != nil {
		t.Fatalf("setting sequence behind the loaded maximum: %v", err)
	}
	overflowSource(t, cfg.Source.Path)

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume should finish despite the reseed warning, got: %v", err)
	}
	assertOverflowWarning(t, stderr, "sequence set to 2147483647")
	done, err := loadCompletedTables(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if !done["t"] {
		t.Error("table with rows was not marked completed")
	}
	assertFKsApplied(t, statePath)
	assertInsertExhausted(t, connCfg)
}

// TestResume_OverflowDoesNotResetToLoadedMax: the app has issued ids 6..10 and
// deleted them, so MAX(id) is 5 but the sequence is at 10. An overflowing
// resume must exhaust the sequence rather than reset it to MAX(id), which would
// reissue 6.
func TestResume_OverflowDoesNotResetToLoadedMax(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), fiveRowsSetup)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `INSERT INTO "t" (label) SELECT 'app' FROM generate_series(1, 5)`); err != nil {
		t.Fatalf("app inserts: %v", err)
	}
	if _, err := conn.Exec(ctx, `DELETE FROM "t" WHERE id > 5`); err != nil {
		t.Fatalf("app deletes: %v", err)
	}
	overflowSource(t, cfg.Source.Path)

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	assertOverflowWarning(t, stderr, "sequence set to 2147483647")
	assertInsertExhausted(t, connCfg)
}

// TestResume_SequenceAlreadyAtMaxStaysExhausted: a sequence already at the
// column maximum must stay there on an overflowing resume, reporting no change.
func TestResume_SequenceAlreadyAtMaxStaysExhausted(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), fiveRowsSetup)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	if _, err := conn.Exec(context.Background(), `SELECT setval(pg_get_serial_sequence('"t"', 'id'), 2147483647, true)`); err != nil {
		t.Fatalf("exhausting sequence: %v", err)
	}
	overflowSource(t, cfg.Source.Path)

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	assertOverflowWarning(t, stderr, "sequence left unchanged")
	assertInsertExhausted(t, connCfg)
}

// TestResume_EmptyTableOverflowExhaustsSequence: with no loaded rows and an
// overflowing high-water mark, the sequence is still exhausted, so no id can
// be issued from it.
func TestResume_EmptyTableOverflowExhaustsSequence(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), nil)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	overflowSource(t, cfg.Source.Path)

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	assertOverflowWarning(t, stderr, "sequence set to 2147483647")
	assertInsertExhausted(t, connCfg)
}

// TestReseedCompletedTable_AliasExcludedFromColumnOrderIsNotDrift: a PK
// column left out of ColumnOrder isn't created, so the config doesn't claim
// an alias for it, and a table without an identity is not drift.
func TestReseedCompletedTable_AliasExcludedFromColumnOrderIsNotDrift(t *testing.T) {
	connCfg, err := connectForLoad(context.Background(), identityTestPgURL(t), filepath.Join(t.TempDir(), "drift.db"), false, filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Skipf("no Postgres available: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	if _, err := conn.Exec(context.Background(), `CREATE TABLE "t" (id integer PRIMARY KEY, v text)`); err != nil {
		t.Fatalf("creating t: %v", err)
	}
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"v"},
		Columns: map[string]config.ColumnConfig{
			"id": identityColumns("INTEGER", "integer", 1),
			"v":  identityColumns("TEXT", "text", 0),
		},
	}

	stderr, err := withStderr(t, func() error {
		return reseedCompletedTable(context.Background(), conn, nil, "t", "t", tc)
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if stderr != "" {
		t.Errorf("expected no warning, got: %q", stderr)
	}
}

// fiveRowsSetup loads ids 1..5 so the next generated id after a correct
// reseed is 6.
var fiveRowsSetup = []string{`INSERT INTO t (id, label) VALUES (1,'a'),(2,'a'),(3,'a'),(4,'a'),(5,'a')`}

// overflowSource leaves the source's sqlite_sequence high-water mark at
// 2147483648 without changing its live rows.
func overflowSource(t *testing.T, path string) {
	t.Helper()
	src, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer src.Close()
	for _, stmt := range overflowIntegerSetup[1:] {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatalf("source %q: %v", stmt, err)
		}
	}
}

func assertOverflowWarning(t *testing.T, stderr, note string) {
	t.Helper()
	for _, want := range []string{"warning:", "t.id", "2147483648", "2147483647", note, "retype the column to bigint and re-run"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning should contain %q, got: %q", want, stderr)
		}
	}
}

// assertInsertExhausted checks that an id-less insert fails because the
// identity sequence is exhausted, rather than returning an id.
func assertInsertExhausted(t *testing.T, connCfg *pgx.ConnConfig) {
	t.Helper()
	conn := pgConnFor(t, connCfg)
	var id int64
	err := conn.QueryRow(context.Background(), `INSERT INTO "t" (label) VALUES ('new') RETURNING id`).Scan(&id)
	if err == nil {
		t.Fatalf("insert returned id %d, want the exhausted sequence to fail it", id)
	}
	if !strings.Contains(err.Error(), "reached maximum value of sequence") {
		t.Errorf("insert should fail with the exhausted-sequence error, got: %v", err)
	}
}

func assertFKsApplied(t *testing.T, statePath string) {
	t.Helper()
	st, err := readState(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if !st.FKsApplied {
		t.Error("resume did not reach the foreign key step (fks_applied is false)")
	}
}

// TestResume_CompletedTableWithoutIdentityRepairsIdentity: a table loaded by a
// build that never created identities (config says rowid-alias, catalog has no
// identity) must get one on resume, so the next id-less insert succeeds.
func TestResume_CompletedTableWithoutIdentityRepairsIdentity(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tenRowsDeleteNewest)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	if _, err := conn.Exec(context.Background(), `ALTER TABLE "t" ALTER COLUMN id DROP IDENTITY`); err != nil {
		t.Fatalf("dropping identity: %v", err)
	}

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if strings.Contains(stderr, "warning") {
		t.Errorf("expected no warning when the identity is repaired, got: %q", stderr)
	}
	if got := insertWithoutID(t, connCfg); got != 11 {
		t.Errorf("first generated id after repair = %d, want 11", got)
	}
}

// TestResume_CompletedTableWithoutStatsGetsAnalyzed: a resume that finds a
// completed table with no ANALYZE on record must analyze it.
func TestResume_CompletedTableWithoutStatsGetsAnalyzed(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), fiveRowsSetup)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `SELECT pg_stat_reset_single_table_counters('"t"'::regclass)`); err != nil {
		t.Fatalf("resetting stats: %v", err)
	}
	var analyzed bool
	if err := conn.QueryRow(ctx, `SELECT last_analyze IS NOT NULL FROM pg_stat_user_tables WHERE relid = '"t"'::regclass`).Scan(&analyzed); err != nil {
		t.Fatalf("reading stats: %v", err)
	}
	if analyzed {
		t.Fatal("test setup: expected stats to be cleared before resume")
	}

	if err := executeLoad(cfg, connCfg, true, statePath); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// Stats from the resume's session are flushed when it closes; poll briefly.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var got bool
		if err := conn.QueryRow(ctx, `SELECT last_analyze IS NOT NULL FROM pg_stat_user_tables WHERE relid = '"t"'::regclass`).Scan(&got); err != nil {
			t.Fatalf("reading stats: %v", err)
		}
		if got {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("resume did not analyze a completed table with no stats")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestResume_HasRowsRepairsMissingIdentity: a table with rows and no state
// entry, loaded without an identity, must get one on resume. Only then is the
// reseed meaningful and the table marked completed.
func TestResume_HasRowsRepairsMissingIdentity(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tenRowsDeleteNewest)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("clearing state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	if _, err := conn.Exec(context.Background(), `ALTER TABLE "t" ALTER COLUMN id DROP IDENTITY`); err != nil {
		t.Fatalf("dropping identity: %v", err)
	}

	if err := executeLoad(cfg, connCfg, true, statePath); err != nil {
		t.Fatalf("resume: %v", err)
	}
	done, err := loadCompletedTables(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if !done["t"] {
		t.Error("table was not marked completed after repair")
	}
	if got := insertWithoutID(t, connCfg); got != 11 {
		t.Errorf("first generated id after repair = %d, want 11", got)
	}
}

// TestResume_RepairFailureReturnsErrorAndDoesNotMarkTable: ADD GENERATED is
// refused when the column already has a default. The resume must return that
// error and leave the table unmarked, so the next run retries it.
func TestResume_RepairFailureReturnsErrorAndDoesNotMarkTable(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tenRowsDeleteNewest)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("clearing state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	if _, err := conn.Exec(context.Background(), `ALTER TABLE "t" ALTER COLUMN id DROP IDENTITY, ALTER COLUMN id SET DEFAULT 5`); err != nil {
		t.Fatalf("setting up default: %v", err)
	}

	err := executeLoad(cfg, connCfg, true, statePath)
	if err == nil || !strings.Contains(err.Error(), "adding identity to t.id") {
		t.Fatalf("expected the identity repair error to be returned, got: %v", err)
	}
	done, err := loadCompletedTables(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if done["t"] {
		t.Error("table was marked completed despite the failed identity repair")
	}
}

// TestResume_NonRangeReseedFailureDoesNotMarkTable: on the has-rows path, a
// reseed failure that isn't an overflow must abort before the table is marked
// completed, so the next resume retries it. Here setval fails because the
// sequence's MAXVALUE is below the loaded maximum.
func TestResume_NonRangeReseedFailureDoesNotMarkTable(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tenRowsDeleteNewest)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("clearing completed state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	var seq string
	if err := conn.QueryRow(context.Background(), `SELECT pg_get_serial_sequence('"t"', 'id')`).Scan(&seq); err != nil {
		t.Fatalf("looking up sequence: %v", err)
	}
	if _, err := conn.Exec(context.Background(), "ALTER SEQUENCE "+seq+" MAXVALUE 5 RESTART WITH 1"); err != nil {
		t.Fatalf("lowering MAXVALUE: %v", err)
	}

	err := executeLoad(cfg, connCfg, true, statePath)
	if err == nil {
		t.Fatal("expected the non-range reseed failure to abort the resume")
	}
	if strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected a non-range error, got: %v", err)
	}
	done, err := loadCompletedTables(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if done["t"] {
		t.Error("t was marked completed despite a failed reseed")
	}
}

// TestFresh_OverflowOnResumeKeepsExistingEmptyTable: an overflow on a resume
// that finds an empty partial t must fail before the DROP, leaving t and the
// state file exactly as they were.
func TestFresh_OverflowOnResumeKeepsExistingEmptyTable(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), overflowIntegerSetup)
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("writing state: %v", err)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	stmt, err := ddl.GenerateCreateTable("t", cfg.Tables["t"])
	if err != nil {
		t.Fatalf("GenerateCreateTable: %v", err)
	}
	if _, err := conn.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("creating empty t: %v", err)
	}

	err = executeLoad(cfg, connCfg, true, statePath)
	if err == nil || !strings.Contains(err.Error(), "2147483648") {
		t.Fatalf("expected range error, got: %v", err)
	}
	var exists bool
	if err := conn.QueryRow(context.Background(), `SELECT to_regclass('"t"') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("checking for t: %v", err)
	}
	if !exists {
		t.Error("t was dropped by a resume that then failed the range check")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("state file changed:\nbefore: %s\nafter:  %s", before, after)
	}
}
