//go:build integration

// Tier 3 (real Postgres): a rowid-alias PK loaded from SQLite must accept
// inserts without an explicit id, with the identity sequence starting past
// the max loaded id. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run TestIdentity -v
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

func identityTestPgURL(t *testing.T) string {
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

func identityColumns(declared, target string, pk int) config.ColumnConfig {
	return config.ColumnConfig{DeclaredType: declared, TargetType: target, PrimaryKeySeq: pk, Reviewed: true}
}

// TestIdentity_InsertWithoutIDAfterLoad: "widgets" has a gap (max id 5, 3
// rows), so a sequence left at its default start would hand out 1 and
// collide. "empty_widgets" has no rows and must start at 1.
func TestIdentity_InsertWithoutIDAfterLoad(t *testing.T) {
	ctx := context.Background()
	pgURL := identityTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "identity.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	if _, err := sourceDB.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, label TEXT)`); err != nil {
		t.Fatalf("creating widgets: %v", err)
	}
	if _, err := sourceDB.Exec(`INSERT INTO widgets (id, label) VALUES (1, 'a'), (2, 'b'), (5, 'c')`); err != nil {
		t.Fatalf("seeding widgets: %v", err)
	}
	if _, err := sourceDB.Exec(`CREATE TABLE empty_widgets (id INTEGER PRIMARY KEY AUTOINCREMENT, label TEXT)`); err != nil {
		t.Fatalf("creating empty_widgets: %v", err)
	}

	widgets := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "label"},
		Columns: map[string]config.ColumnConfig{
			"id":    identityColumns("INTEGER", "integer", 1),
			"label": identityColumns("TEXT", "text", 0),
		},
	}
	emptyWidgets := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "label"},
		Columns: map[string]config.ColumnConfig{
			"id":    identityColumns("INTEGER", "bigint", 1),
			"label": identityColumns("TEXT", "text", 0),
		},
	}
	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: sqlitePath},
		Tables: map[string]config.TableConfig{"widgets": widgets, "empty_widgets": emptyWidgets},
	}

	configPath := filepath.Join(dir, "identity.db.migration.yaml")
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

	var id int64
	if err := conn.QueryRow(ctx, `INSERT INTO widgets (label) VALUES ('new') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert without id into widgets failed: %v", err)
	}
	if id != 6 {
		t.Errorf("widgets: expected first generated id 6 (max loaded 5 + 1), got %d", id)
	}

	if err := conn.QueryRow(ctx, `INSERT INTO empty_widgets (label) VALUES ('new') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("insert without id into empty_widgets failed: %v", err)
	}
	if id != 1 {
		t.Errorf("empty_widgets: expected first generated id 1, got %d", id)
	}

	for _, table := range []string{"widgets", "empty_widgets"} {
		var analyzed bool
		if err := conn.QueryRow(ctx,
			`SELECT last_analyze IS NOT NULL FROM pg_stat_user_tables WHERE relname = $1`, table,
		).Scan(&analyzed); err != nil {
			t.Fatalf("reading last_analyze for %s: %v", table, err)
		}
		if !analyzed {
			t.Errorf("%s: expected last_analyze to be set after load", table)
		}
	}
}
