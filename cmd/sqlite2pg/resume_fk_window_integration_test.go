//go:build integration

package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

// TestResume_ForeignKeyStepReRunsAfterCommitCrashWindow covers issue #128:
// a crash between the FK transaction committing and FKsApplied being
// written to state leaves the constraints in place with the flag still
// false. The idempotent DDL (DROP CONSTRAINT IF EXISTS + ADD, CREATE INDEX
// IF NOT EXISTS) must let a --resume re-run the step cleanly rather than
// aborting on "already exists".
func TestResume_ForeignKeyStepReRunsAfterCommitCrashWindow(t *testing.T) {
	ctx := context.Background()
	pgURL := resumeTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "fkwin.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	for _, stmt := range []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`INSERT INTO parent (id) VALUES (1), (2)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, pid INTEGER)`,
		`INSERT INTO child (id, pid) VALUES (1, 1), (2, 2)`,
	} {
		if _, err := sourceDB.Exec(stmt); err != nil {
			t.Fatalf("fixture setup %q: %v", stmt, err)
		}
	}

	idPK := config.ColumnConfig{TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1}
	pidCol := config.ColumnConfig{TargetType: "bigint", Reviewed: true}
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: sqlitePath},
		Tables: map[string]config.TableConfig{
			"parent": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": idPK},
			},
			"child": {
				Include:     true,
				ColumnOrder: []string{"id", "pid"},
				Columns:     map[string]config.ColumnConfig{"id": idPK, "pid": pidCol},
				ForeignKeys: []config.ForeignKey{{Columns: []string{"pid"}, RefTable: "parent", RefColumns: []string{"id"}}},
			},
		},
	}

	configPath := filepath.Join(dir, "fkwin.db.migration.yaml")
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
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{dbName}.Sanitize()); err != nil {
			t.Logf("cleanup: dropping test database %s failed (drop it by hand): %v", dbName, err)
		}
	})

	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("first load: %v", err)
	}

	// Simulate the crash window: the FK transaction committed (constraints
	// are really in Postgres) but FKsApplied never got written.
	st, err := readState(statePath)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !st.FKsApplied {
		t.Fatal("expected FKsApplied=true after a clean first load")
	}
	st.FKsApplied = false
	if err := writeState(statePath, st); err != nil {
		t.Fatalf("rewriting state to simulate the crash window: %v", err)
	}

	// Before #128's idempotent DDL this aborted with
	// `constraint "fk_child_pid" ... already exists`.
	if err := executeLoad(cfg, connCfg, true, statePath); err != nil {
		t.Fatalf("resumed load should re-run the FK step cleanly, got: %v", err)
	}

	st, err = readState(statePath)
	if err != nil {
		t.Fatalf("readState after resume: %v", err)
	}
	if !st.FKsApplied {
		t.Error("expected FKsApplied=true after the resumed FK step")
	}

	verifyConn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer verifyConn.Close(ctx)
	var nFK, nIdx int
	if err := verifyConn.QueryRow(ctx,
		`SELECT count(*) FROM pg_constraint WHERE conrelid = '"child"'::regclass AND contype = 'f'`,
	).Scan(&nFK); err != nil {
		t.Fatalf("counting child FK constraints: %v", err)
	}
	if nFK != 1 {
		t.Errorf("expected exactly 1 FK constraint on child after resume, got %d", nFK)
	}
	if err := verifyConn.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'child' AND indexname = 'idx_child_pid'`,
	).Scan(&nIdx); err != nil {
		t.Fatalf("counting child FK index: %v", err)
	}
	if nIdx != 1 {
		t.Errorf("expected the FK index present exactly once after resume, got %d", nIdx)
	}
}
