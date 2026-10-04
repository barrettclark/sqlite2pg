//go:build integration

// Tier 3 (real Postgres): postLoadTable must position a rowid-alias identity
// sequence past the loaded rows when MAX(id) sits at the column type's
// maximum, and a --resume that re-runs it must succeed. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run TestPostLoadTable -v
package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
)

func TestPostLoadTable_IdentitySequence(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		target string
		ids    []int64
		// wantNext is the id the next insert must get; 0 means the sequence
		// is exhausted and the insert must fail at the type maximum.
		wantNext int64
	}{
		{name: "integer at type maximum", file: "int_max.db", target: "integer", ids: []int64{1, 2147483647}},
		{name: "smallint at type maximum", file: "smallint_max.db", target: "smallint", ids: []int64{1, 32767}},
		{name: "only non-positive ids", file: "nonpositive.db", target: "integer", ids: []int64{-5, 0, -1}, wantNext: 1},
		{name: "empty table", file: "empty.db", target: "integer", wantNext: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pgURL := identityTestPgURL(t)

			dir := t.TempDir()
			sqlitePath := filepath.Join(dir, tt.file)
			sourceDB, err := sql.Open("sqlite", sqlitePath)
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			defer sourceDB.Close()
			if _, err := sourceDB.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, label TEXT)`); err != nil {
				t.Fatalf("creating t: %v", err)
			}
			for _, id := range tt.ids {
				if _, err := sourceDB.Exec(`INSERT INTO t (id, label) VALUES (?, 'x')`, id); err != nil {
					t.Fatalf("seeding id %d: %v", id, err)
				}
			}

			tc := config.TableConfig{
				Include:     true,
				ColumnOrder: []string{"id", "label"},
				Columns: map[string]config.ColumnConfig{
					"id":    identityColumns("INTEGER", tt.target, 1),
					"label": identityColumns("TEXT", "text", 0),
				},
			}
			cfg := &config.MigrationConfig{
				Source: config.SourceInfo{Path: sqlitePath},
				Tables: map[string]config.TableConfig{"t": tc},
			}
			configPath := filepath.Join(dir, tt.file+".migration.yaml")
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

			if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
				t.Fatalf("load failed: %v", err)
			}

			conn, err := pgx.ConnectConfig(ctx, connCfg)
			if err != nil {
				t.Fatalf("connecting after load: %v", err)
			}
			defer conn.Close(ctx)

			// A --resume that finds t loaded but unmarked re-runs postLoadTable.
			if err := postLoadTable(ctx, conn, "t", tc, 0); err != nil {
				t.Fatalf("resume postLoadTable failed: %v", err)
			}

			var id int64
			err = conn.QueryRow(ctx, `INSERT INTO t (label) VALUES ('new') RETURNING id`).Scan(&id)
			if tt.wantNext == 0 {
				if err == nil || !strings.Contains(err.Error(), "reached maximum value") {
					t.Fatalf("expected insert to fail at type maximum, got id=%d err=%v", id, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("insert without id failed: %v", err)
			}
			if id != tt.wantNext {
				t.Errorf("expected first generated id %d, got %d", tt.wantNext, id)
			}
		})
	}
}

func TestPostLoadTable_MissingIdentityErrors(t *testing.T) {
	ctx := context.Background()
	pgURL := identityTestPgURL(t)

	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id"},
		Columns: map[string]config.ColumnConfig{
			"id": identityColumns("INTEGER", "integer", 1),
		},
	}
	dir := t.TempDir()
	connCfg, err := connectForLoad(ctx, pgURL, filepath.Join(dir, "missing_identity.db"), false, filepath.Join(dir, "state.json"))
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

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE TABLE t (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("creating t: %v", err)
	}

	err = postLoadTable(ctx, conn, "t", tc, 0)
	if err == nil {
		t.Fatal("expected error for rowid-alias column without an identity")
	}
	if !strings.Contains(err.Error(), "t.id") {
		t.Errorf("error should name table and column t.id, got: %v", err)
	}
}

func TestPostLoadTable_HighWaterAboveIdentityRange(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		highWater int64
		colMax    string
	}{
		{name: "integer", target: "integer", highWater: 2147483648, colMax: "2147483647"},
		{name: "smallint", target: "smallint", highWater: 32768, colMax: "32767"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pgURL := identityTestPgURL(t)

			tc := config.TableConfig{
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns: map[string]config.ColumnConfig{
					"id": identityColumns("INTEGER", tt.target, 1),
				},
			}
			dir := t.TempDir()
			statePath := filepath.Join(dir, "state.json")
			connCfg, err := connectForLoad(ctx, pgURL, filepath.Join(dir, "range.db"), false, statePath)
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

			conn, err := pgx.ConnectConfig(ctx, connCfg)
			if err != nil {
				t.Fatalf("connecting: %v", err)
			}
			defer conn.Close(ctx)
			stmt, err := ddl.GenerateCreateTable("t", tc)
			if err != nil {
				t.Fatalf("GenerateCreateTable: %v", err)
			}
			if _, err := conn.Exec(ctx, stmt); err != nil {
				t.Fatalf("creating t: %v", err)
			}

			err = postLoadTable(ctx, conn, "t", tc, tt.highWater)
			if err == nil {
				t.Fatal("expected error for a high-water mark above the identity column's range")
			}
			for _, want := range []string{"t.id", tt.target, strconv.FormatInt(tt.highWater, 10), tt.colMax} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should contain %q, got: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "out of bounds") {
				t.Errorf("error should be the clear range check, not the Postgres setval message: %v", err)
			}
		})
	}
}
