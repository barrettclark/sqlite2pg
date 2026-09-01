//go:build integration

// Tier 3 (real Postgres): confirms the post-load verification path
// (`run --verify` / `load --verify`) actually satisfies its core design
// requirement — that it verifies directly against the in-memory
// config.MigrationConfig and *pgx.ConnConfig a just-completed load already
// holds, never by re-reading configPath/statePath from disk. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/migrate/... -run TestPostLoadVerify -v
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/pipeline"
)

func postLoadVerifyTestPgURL(t *testing.T) string {
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

// TestPostLoadVerify_WorksAfterConfigAndStateFilesAreDeleted is the
// concrete regression test for the "no disk round-trip" design requirement:
// it loads a real fixture into a scratch Postgres database, deletes the
// generated config and state files exactly as `run`'s cleanupConfigAfterLoad
// would after a successful load without --keep-config, and only then calls
// runPostLoadVerify using nothing but the in-memory config.MigrationConfig
// and *pgx.ConnConfig the earlier load already produced. If runPostLoadVerify
// silently re-read either deleted file, this would fail loudly rather than
// silently passing having verified nothing.
func TestPostLoadVerify_WorksAfterConfigAndStateFilesAreDeleted(t *testing.T) {
	ctx := context.Background()
	pgURL := postLoadVerifyTestPgURL(t)

	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fixtures", "bikes.db"))
	if err != nil {
		t.Fatalf("resolving fixture path: %v", err)
	}
	if _, err := os.Stat(fixturePath); err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	sourceDB, err := sql.Open("sqlite", "file:"+fixturePath+"?mode=ro")
	if err != nil {
		t.Fatalf("opening fixture: %v", err)
	}
	defer sourceDB.Close()
	if err := sourceDB.Ping(); err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	result, err := pipeline.ProfileDatabase(sourceDB, fixturePath, 5000, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}
	for tableName, tc := range result.Config.Tables {
		for colName, col := range tc.Columns {
			col.Reviewed = true
			tc.Columns[colName] = col
		}
		result.Config.Tables[tableName] = tc
	}
	cfg := result.Config

	dir := t.TempDir()
	configPath := filepath.Join(dir, "bikes.db.migration.yaml")
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	statePath := configPath + ".state.json"

	connCfg, err := connectForLoad(ctx, pgURL, fixturePath, false, statePath)
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
		t.Fatalf("executeLoad: %v", err)
	}

	// Simulate cleanupConfigAfterLoad's post-success deletion (issue #38/#52)
	// happening BEFORE verification, to prove runPostLoadVerify tolerates it.
	if err := os.Remove(configPath); err != nil {
		t.Fatalf("removing config to simulate cleanup: %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("removing state file to simulate cleanup: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected config file to be gone before verification, stat err: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected state file to be gone before verification, stat err: %v", err)
	}

	var out strings.Builder
	verifyErr := runPostLoadVerify(ctx, cfg, connCfg, verifyAlways, strings.NewReader(""), &out)
	if verifyErr != nil {
		t.Fatalf("runPostLoadVerify failed even though the load and data were fine: %v\noutput:\n%s", verifyErr, out.String())
	}
	if !strings.Contains(out.String(), "verifying bikes...") {
		t.Errorf("expected the report to show bikes actually being verified, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "post-load verification passed") {
		t.Errorf("expected a passing verification summary, got:\n%s", out.String())
	}
}
