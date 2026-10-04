//go:build integration

// Tier 3 (real Postgres): a SQLite REAL infinity (1e999 / -1e999) loaded
// with no transform must reach Postgres as the same infinity. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run TestRealInf -v
package main

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

// realInfFixture loads a two-row SQLite table whose x and y REAL columns
// hold +Inf and -Inf, with no transform on either, into double precision
// (x) and numeric (y). It returns the Postgres connection config for the
// loaded database and the load error.
func realInfFixture(t *testing.T, xType, yType string) (*pgx.ConnConfig, error) {
	t.Helper()
	ctx := context.Background()
	pgURL := resumeTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "real_inf.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	if _, err := sourceDB.Exec(`CREATE TABLE real_inf (id INTEGER PRIMARY KEY, x REAL, y REAL)`); err != nil {
		t.Fatalf("creating fixture table: %v", err)
	}
	if _, err := sourceDB.Exec(`INSERT INTO real_inf (id, x, y) VALUES (1, 1e999, 1e999), (2, -1e999, -1e999)`); err != nil {
		t.Fatalf("seeding fixture rows: %v", err)
	}

	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: sqlitePath},
		Tables: map[string]config.TableConfig{
			"real_inf": {
				Include:     true,
				ColumnOrder: []string{"id", "x", "y"},
				Columns: map[string]config.ColumnConfig{
					"id": {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
					"x":  {TargetType: xType, Reviewed: true},
					"y":  {TargetType: yType, Reviewed: true},
				},
			},
		},
	}

	configPath := filepath.Join(dir, "real_inf.db.migration.yaml")
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	statePath := configPath + ".state.json"

	connCfg, err := connectForLoad(ctx, pgURL, sqlitePath, false, statePath)
	if err != nil {
		t.Skipf("no Postgres available at %s: %v", pgURL, err)
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

	return connCfg, executeLoad(cfg, connCfg, false, statePath)
}

// TestRealInf_DoublePrecisionStoresSignedInfinity is the end-to-end proof
// that a REAL +Inf/-Inf with no transform lands as +Infinity/-Infinity in
// a double precision column.
func TestRealInf_DoublePrecisionStoresSignedInfinity(t *testing.T) {
	connCfg, err := realInfFixture(t, "double precision", "double precision")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	conn, err := pgx.ConnectConfig(context.Background(), connCfg)
	if err != nil {
		t.Fatalf("connecting to verify: %v", err)
	}
	defer conn.Close(context.Background())

	rows, err := conn.Query(context.Background(), `SELECT id, x FROM real_inf ORDER BY id`)
	if err != nil {
		t.Fatalf("querying loaded rows: %v", err)
	}
	defer rows.Close()
	want := map[int64]float64{1: math.Inf(1), 2: math.Inf(-1)}
	got := map[int64]float64{}
	for rows.Next() {
		var id int64
		var x float64
		if err := rows.Scan(&id, &x); err != nil {
			t.Fatalf("scanning row: %v", err)
		}
		got[id] = x
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating rows: %v", err)
	}
	for id, w := range want {
		if g, ok := got[id]; !ok || g != w {
			t.Errorf("row %d: x = %v (present=%v), want %v", id, g, ok, w)
		}
	}
}

// TestRealInf_NumericOutcome records what a REAL +Inf/-Inf with no
// transform does when loaded into a numeric column. Numeric has Infinity
// since Postgres 14; this test pins whichever answer the driver gives.
func TestRealInf_NumericOutcome(t *testing.T) {
	connCfg, err := realInfFixture(t, "double precision", "numeric")
	if err != nil {
		t.Fatalf("numeric load failed: %v", err)
	}

	conn, err := pgx.ConnectConfig(context.Background(), connCfg)
	if err != nil {
		t.Fatalf("connecting to verify: %v", err)
	}
	defer conn.Close(context.Background())

	var pos, neg string
	if err := conn.QueryRow(context.Background(), `SELECT y::text FROM real_inf WHERE id = 1`).Scan(&pos); err != nil {
		t.Fatalf("reading numeric +Inf: %v", err)
	}
	if err := conn.QueryRow(context.Background(), `SELECT y::text FROM real_inf WHERE id = 2`).Scan(&neg); err != nil {
		t.Fatalf("reading numeric -Inf: %v", err)
	}
	if pos != "Infinity" || neg != "-Infinity" {
		t.Errorf("numeric stored %q and %q, want \"Infinity\" and \"-Infinity\"", pos, neg)
	}
}
