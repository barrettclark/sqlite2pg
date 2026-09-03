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

// TestResume_ForeignKeysForATableIncludedBetweenLoadAndResume covers issue
// #142 / M4: executeLoad's FK step used to be gated on a one-shot
// FKsApplied flag. A load run to completion sets the flag; if the config
// is then edited to flip a table from include: false to include: true and
// re-run with --resume, the new table was created and COPY'd but the FK
// step was skipped ("foreign keys already applied ... skipping") — so its
// foreign keys and FK indexes were never created, and the load still
// reported success. The step now runs every invocation (idempotent DDL in
// one transaction), so the newly-included table gets its constraints.
func TestResume_ForeignKeysForATableIncludedBetweenLoadAndResume(t *testing.T) {
	ctx := context.Background()
	pgURL := resumeTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "fknew.db")
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
				Include:     false, // excluded for the first load
				ColumnOrder: []string{"id", "pid"},
				Columns:     map[string]config.ColumnConfig{"id": idPK, "pid": pidCol},
				ForeignKeys: []config.ForeignKey{{Columns: []string{"pid"}, RefTable: "parent", RefColumns: []string{"id"}}},
			},
		},
	}

	configPath := filepath.Join(dir, "fknew.db.migration.yaml")
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
		t.Fatalf("first load (child excluded): %v", err)
	}
	st, err := readState(statePath)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !st.FKsApplied {
		t.Fatal("expected FKsApplied=true after the first load")
	}

	// The config edit: include child now, and resume.
	tc := cfg.Tables["child"]
	tc.Include = true
	cfg.Tables["child"] = tc
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save after edit: %v", err)
	}
	if err := executeLoad(cfg, connCfg, true, statePath); err != nil {
		t.Fatalf("resumed load (child now included): %v", err)
	}

	verifyConn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer verifyConn.Close(ctx)

	var nRows int
	if err := verifyConn.QueryRow(ctx, `SELECT count(*) FROM "child"`).Scan(&nRows); err != nil {
		t.Fatalf("counting child rows: %v", err)
	}
	if nRows != 2 {
		t.Errorf("expected child loaded with 2 rows on resume, got %d", nRows)
	}

	var nFK int
	if err := verifyConn.QueryRow(ctx,
		`SELECT count(*) FROM pg_constraint WHERE conrelid = '"child"'::regclass AND contype = 'f'`,
	).Scan(&nFK); err != nil {
		t.Fatalf("counting child FK constraints: %v", err)
	}
	if nFK != 1 {
		t.Errorf("expected 1 FK constraint on child after resume, got %d (the M4 bug: FK step skipped for a newly-included table)", nFK)
	}

	var nIdx int
	if err := verifyConn.QueryRow(ctx,
		`SELECT count(*) FROM pg_indexes WHERE tablename = 'child' AND indexname = 'idx_child_pid'`,
	).Scan(&nIdx); err != nil {
		t.Fatalf("counting child FK index: %v", err)
	}
	if nIdx != 1 {
		t.Errorf("expected the FK index on child after resume, got %d", nIdx)
	}
}
