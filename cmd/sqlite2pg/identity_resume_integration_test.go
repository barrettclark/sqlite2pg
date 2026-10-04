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

	"sqlite2pg/internal/ddl"
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

// TestResume_CompletedTableWithoutIdentityWarnsOnDrift: the config declares a
// rowid-alias identity but the live table has none. Nothing in this tool
// creates an identity-less rowid table, so this is drift from outside it.
// It must be reported, not skipped silently, and must not fail the resume.
func TestResume_CompletedTableWithoutIdentityWarnsOnDrift(t *testing.T) {
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
	for _, want := range []string{"warning:", "t.id", "rowid-alias identity"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("drift warning should contain %q, got: %q", want, stderr)
		}
	}
}

// TestResume_NonRangeReseedFailureDoesNotMarkTable: on the has-rows path, a
// reseed failure that isn't an overflow must abort before the table is marked
// completed, so the next resume retries it. Here setval fails because the
// sequence's MAXVALUE is below the loaded maximum.
func TestResume_NonRangeReseedFailureDoesNotMarkTable(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), tenRowsDeleteNewest)
	if err := executeLoad(cfg, connCfg, false, statePath); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("clearing completed state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	var seq string
	if err := conn.QueryRow(context.Background(), `SELECT pg_get_serial_sequence('"t"', 'id')`).Scan(&seq); err != nil {
		t.Fatalf("looking up sequence: %v", err)
	}
	if _, err := conn.Exec(context.Background(), "ALTER SEQUENCE "+seq+" MAXVALUE 5 RESTART WITH 1"); err != nil {
		t.Fatalf("lowering MAXVALUE: %v", err)
	}

	err := executeLoad(cfg, connCfg, true, statePath)
	if err == nil {
		t.Fatal("expected the non-range reseed failure to abort the resume")
	}
	if strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected a non-range error, got: %v", err)
	}
	done, err := loadCompletedTables(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if done["t"] {
		t.Error("t was marked completed despite a failed reseed")
	}
}

// TestFresh_OverflowOnResumeKeepsExistingEmptyTable: an overflow on a resume
// that finds an empty partial t must fail before the DROP, leaving t and the
// state file exactly as they were.
func TestFresh_OverflowOnResumeKeepsExistingEmptyTable(t *testing.T) {
	cfg, connCfg, statePath := autoincFixture(t, identityTestPgURL(t), overflowIntegerSetup)
	if err := writeState(statePath, loadState{}); err != nil {
		t.Fatalf("writing state: %v", err)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	conn := pgConnFor(t, connCfg)
	stmt, err := ddl.GenerateCreateTable("t", cfg.Tables["t"])
	if err != nil {
		t.Fatalf("GenerateCreateTable: %v", err)
	}
	if _, err := conn.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("creating empty t: %v", err)
	}

	err = executeLoad(cfg, connCfg, true, statePath)
	if err == nil || !strings.Contains(err.Error(), "2147483648") {
		t.Fatalf("expected range error, got: %v", err)
	}
	var exists bool
	if err := conn.QueryRow(context.Background(), `SELECT to_regclass('"t"') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("checking for t: %v", err)
	}
	if !exists {
		t.Error("t was dropped by a resume that then failed the range check")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("state file changed:\nbefore: %s\nafter:  %s", before, after)
	}
}
