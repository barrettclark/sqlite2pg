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

	"github.com/jackc/pgx/v5"
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
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), []string{`INSERT INTO t (id, label) VALUES (1,'a')`})
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	src, err := sql.Open("sqlite", cfg.Source.Path)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer src.Close()
	for _, stmt := range overflowIntegerSetup[1:] {
		if _, err := src.Exec(stmt); err != nil {
			t.Fatalf("source %q: %v", stmt, err)
		}
	}

	stderr, err := withStderr(t, func() error { return executeLoad(cfg, connCfg, true, statePath) })
	if err != nil {
		t.Fatalf("resume should finish despite the reseed warning, got: %v", err)
	}
	for _, want := range []string{"warning:", "t.id", "2147483648", "2147483647", "retype the column to bigint and re-run"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("warning should contain %q, got: %q", want, stderr)
		}
	}
	st, err := readState(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if !st.FKsApplied {
		t.Error("resume did not reach the foreign key step (fks_applied is false)")
	}
}

// TestResume_CompletedTableWithoutIdentitySkipsSilently: a completed table
// the catalog says has no identity (e.g. created by an older binary) is
// skipped with no warning and no error.
func TestResume_CompletedTableWithoutIdentitySkipsSilently(t *testing.T) {
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
	if stderr != "" {
		t.Errorf("expected no warning for a table without identity, got: %q", stderr)
	}
}
