//go:build integration

// Tier 3 (real Postgres): a TEXT number re-confirmed as double precision is
// persisted with numeric_text_to_double, and COPY must load it as a float.
// Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run TestTextDouble -v
package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

func TestTextDouble_NumericTextLoadsAsFloat(t *testing.T) {
	ctx := context.Background()
	pgURL := resumeTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "text_double.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	if _, err := sourceDB.Exec(`CREATE TABLE text_double (id INTEGER PRIMARY KEY, x TEXT)`); err != nil {
		t.Fatalf("creating fixture table: %v", err)
	}
	if _, err := sourceDB.Exec(`INSERT INTO text_double (id, x) VALUES (1, '1.5'), (2, '-2.25')`); err != nil {
		t.Fatalf("seeding fixture rows: %v", err)
	}

	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: sqlitePath},
		Tables: map[string]config.TableConfig{"text_double": {
			Include:     true,
			ColumnOrder: []string{"id", "x"},
			Columns: map[string]config.ColumnConfig{
				"id": {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
				"x":  {TargetType: "double precision", Transform: "numeric_text_to_double", Reviewed: true},
			},
		}},
	}
	configPath := filepath.Join(dir, "text_double.db.migration.yaml")
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	statePath := configPath + ".state.json"

	probe, err := pgx.ParseConfig(pgURL)
	if err != nil {
		t.Fatalf("parsing PGURL: %v", err)
	}
	probe.Database = "postgres"
	probeConn, err := pgx.ConnectConfig(ctx, probe)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			t.Skipf("no Postgres reachable at %s: %v", pgURL, err)
		}
		t.Fatalf("connecting to Postgres at %s: %v", pgURL, err)
	}
	probeConn.Close(ctx)

	connCfg, err := connectForLoad(ctx, pgURL, sqlitePath, false, statePath)
	if err != nil {
		t.Fatalf("connectForLoad: %v", err)
	}
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
		conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{connCfg.Database}.Sanitize())
	})

	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connecting to verify: %v", err)
	}
	defer conn.Close(ctx)
	want := map[int64]float64{1: 1.5, 2: -2.25}
	rows, err := conn.Query(ctx, `SELECT id, x FROM text_double`)
	if err != nil {
		t.Fatalf("querying loaded rows: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id int64
		var x float64
		if err := rows.Scan(&id, &x); err != nil {
			t.Fatalf("scanning row: %v", err)
		}
		if x != want[id] {
			t.Errorf("row %d: x = %v, want %v", id, x, want[id])
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating rows: %v", err)
	}
	if seen != len(want) {
		t.Errorf("loaded %d rows, want %d", seen, len(want))
	}
}
