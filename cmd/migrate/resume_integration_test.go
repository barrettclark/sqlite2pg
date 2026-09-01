//go:build integration

// Tier 3 (real Postgres): issue #78's end-to-end proof. `migrate load
// --resume`'s primary scenario — a load that fails partway through a
// table, then gets resumed — used to be non-functional: markTableCompleted
// only runs after a table's COPY finishes, but CREATE TABLE (unconditional,
// no IF NOT EXISTS) already committed before that, so a resumed run
// re-issued CREATE TABLE for the exact table it was supposed to resume and
// Postgres rejected it with "relation already exists", aborting on the
// first table --resume was meant to continue from. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/migrate/... -run TestResume -v
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

func resumeTestPgURL(t *testing.T) string {
	t.Helper()
	if u := os.Getenv("PGURL"); u != "" {
		return u
	}
	usr, err := user.Current()
	if err != nil {
		t.Skipf("PGURL not set and could not determine current user: %v", err)
	}
	return fmt.Sprintf("postgres://%s@localhost:5432/?sslmode=disable", usr.Username)
}

// resumeTestConfig builds a single-table config for the "flaky" table
// created below: an integer-typed column via numeric_text_to_integer,
// which fails loudly (not silently) on a non-numeric row — exactly what's
// needed to make CREATE TABLE succeed and COPY fail on the very next
// statement, the precise state that used to break --resume.
func resumeTestConfig() config.TableConfig {
	return config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "n"},
		Columns: map[string]config.ColumnConfig{
			"id": {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"n":  {TargetType: "integer", Transform: "numeric_text_to_integer", Reviewed: true},
		},
	}
}

// TestResume_RecoversFromAMidTableCOPYFailure is issue #78's regression
// test. First load attempt: the source has a row numeric_text_to_integer
// can't parse, so CREATE TABLE for "flaky" succeeds but COPY fails right
// after — the table exists, empty, and unmarked-completed in the state
// file, exactly the state a real crash/network-drop mid-load would leave.
// The bad row is then fixed directly in the SQLite source (simulating a
// human correcting bad source data between attempts) and the load is
// resumed. Before the fix, this second call would fail immediately with
// "relation "flaky" already exists" instead of ever reaching COPY again.
func TestResume_RecoversFromAMidTableCOPYFailure(t *testing.T) {
	ctx := context.Background()
	pgURL := resumeTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "flaky.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	if _, err := sourceDB.Exec(`CREATE TABLE flaky (id INTEGER PRIMARY KEY, n TEXT)`); err != nil {
		t.Fatalf("creating fixture table: %v", err)
	}
	if _, err := sourceDB.Exec(`INSERT INTO flaky (id, n) VALUES (1, '10'), (2, 'not-a-number')`); err != nil {
		t.Fatalf("seeding fixture rows: %v", err)
	}

	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: sqlitePath},
		Tables: map[string]config.TableConfig{"flaky": resumeTestConfig()},
	}

	configPath := filepath.Join(dir, "flaky.db.migration.yaml")
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	statePath := configPath + ".state.json"

	connCfg, err := connectForLoad(ctx, pgURL, sqlitePath, false, statePath)
	if err != nil {
		t.Skipf("no Postgres available at %s: %v", pgURL, err)
	}
	dbName := connCfg.Database
	t.Cleanup(func() {
		maintCfg, err := pgx.ParseConfig(pgURL)
		if err != nil {
			return
		}
		maintCfg.Database = "postgres"
		conn, err := pgx.ConnectConfig(ctx, maintCfg)
		if err != nil {
			return
		}
		defer conn.Close(ctx)
		conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize())
	})

	firstErr := executeLoad(cfg, connCfg, false, statePath)
	if firstErr == nil {
		t.Fatal("expected the first load attempt to fail on the bad row")
	}

	completed, err := loadCompletedTables(statePath)
	if err != nil {
		t.Fatalf("loadCompletedTables: %v", err)
	}
	if completed["flaky"] {
		t.Fatal("expected flaky to NOT be marked completed after a failed COPY")
	}

	// Fix the bad row directly in the source, simulating a human
	// correcting the data between load attempts.
	if _, err := sourceDB.Exec(`UPDATE flaky SET n = '20' WHERE id = 2`); err != nil {
		t.Fatalf("fixing the bad row: %v", err)
	}

	resumeErr := executeLoad(cfg, connCfg, true, statePath)
	if resumeErr != nil {
		t.Fatalf("resumed load failed (this is the bug: CREATE TABLE re-run on a table that already existed from the first attempt): %v", resumeErr)
	}

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connecting to verify: %v", err)
	}
	defer conn.Close(ctx)
	var count int
	if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM "flaky"`).Scan(&count); err != nil {
		t.Fatalf("counting loaded rows: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows loaded after resume, got %d", count)
	}
}
