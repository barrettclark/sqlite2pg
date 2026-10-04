//go:build integration

// Tier 3 (real Postgres): an AUTOINCREMENT table's identity must continue
// from SQLite's sqlite_sequence high-water mark rather than MAX(id), so an id
// SQLite would never reuse (because its row was deleted) isn't reused after
// the migration. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run TestAutoincrement -v
package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

// tenRowsDeleteNewest inserts ids 1-10 and deletes 9 and 10: MAX(id) is 8,
// but SQLite's high-water mark is 10.
var tenRowsDeleteNewest = []string{
	`INSERT INTO t (id, label) VALUES (1,'a'),(2,'a'),(3,'a'),(4,'a'),(5,'a'),(6,'a'),(7,'a'),(8,'a'),(9,'a'),(10,'a')`,
	`DELETE FROM t WHERE id IN (9, 10)`,
}

// autoincFixture writes an AUTOINCREMENT source table t seeded with setup,
// saves and reloads its config (so the Autoincrement flag must survive the
// YAML round trip), and returns what executeLoad needs.
func autoincFixture(t *testing.T, pgURL string, setup []string) (*config.MigrationConfig, *pgx.ConnConfig, string) {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "autoinc.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	if _, err := sourceDB.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT)`); err != nil {
		t.Fatalf("creating t: %v", err)
	}
	for _, stmt := range setup {
		if _, err := sourceDB.Exec(stmt); err != nil {
			t.Fatalf("seeding %q: %v", stmt, err)
		}
	}

	tc := config.TableConfig{
		Include:       true,
		ColumnOrder:   []string{"id", "label"},
		Autoincrement: true,
		Columns: map[string]config.ColumnConfig{
			"id":    identityColumns("INTEGER", "integer", 1),
			"label": identityColumns("TEXT", "text", 0),
		},
	}
	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Source:        config.SourceInfo{Path: sqlitePath},
		Tables:        map[string]config.TableConfig{"t": tc},
	}
	configPath := filepath.Join(dir, "autoinc.db.migration.yaml")
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfg, err = config.Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Tables["t"].Autoincrement {
		t.Fatal("Autoincrement flag lost in config round trip")
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
	return cfg, connCfg, statePath
}

// insertWithoutID inserts a row letting the identity pick the id and returns it.
func insertWithoutID(t *testing.T, connCfg *pgx.ConnConfig) int64 {
	t.Helper()
	conn, err := pgx.ConnectConfig(context.Background(), connCfg)
	if err != nil {
		t.Fatalf("connecting after load: %v", err)
	}
	defer conn.Close(context.Background())
	var id int64
	if err := conn.QueryRow(context.Background(), `INSERT INTO t (label) VALUES ('new') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert without id failed: %v", err)
	}
	return id
}

func TestAutoincrement_InsertWithoutIDAfterLoad(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		// wantNext is SQLite's next AUTOINCREMENT id: high-water mark + 1.
		wantNext int64
	}{
		{name: "newest rows deleted", setup: tenRowsDeleteNewest, wantNext: 11},
		{name: "never inserted", setup: nil, wantNext: 1},
		{
			name: "all rows deleted",
			setup: []string{
				`INSERT INTO t (id, label) VALUES (1,'a'),(2,'a'),(3,'a')`,
				`DELETE FROM t`,
			},
			wantNext: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tt.setup)
			if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
				t.Fatalf("load failed: %v", err)
			}
			if got := insertWithoutID(t, connCfg); got != tt.wantNext {
				t.Errorf("first generated id = %d, want %d", got, tt.wantNext)
			}
		})
	}
}

// TestAutoincrement_ResumeReseedsFromHighWater: a --resume that finds the
// table loaded but unmarked re-runs postLoadTable, which must reseed from the
// same high-water mark rather than MAX(id).
func TestAutoincrement_ResumeReseedsFromHighWater(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tenRowsDeleteNewest)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("clearing completed state: %v", err)
	}
	if err := executeLoad(cfg, connCfg, true, statePath); err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if got := insertWithoutID(t, connCfg); got != 11 {
		t.Errorf("first generated id after resume = %d, want 11", got)
	}
}
