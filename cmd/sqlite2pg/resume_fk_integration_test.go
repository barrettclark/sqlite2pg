//go:build integration

// Tier 3 (real Postgres): issue #109 / M6's regression. executeLoad's
// foreign-key step used to run every ALTER TABLE ... ADD CONSTRAINT and
// CREATE INDEX one at a time and only then set FKsApplied. If it failed
// partway (an inferred-FK violation, a lock timeout, a dropped
// connection), the constraints added so far were committed, FKsApplied
// stayed false, and every subsequent `sqlite2pg load --resume` re-entered
// the step and aborted on the *first* statement with
// "constraint ... already exists" — never reaching, or reporting, the
// genuine failure. The step is now one transaction, so a partial failure
// rolls back cleanly. Run with:
//
//	PGURL=postgres://user@localhost:5432/postgres?sslmode=disable \
//	  go test -tags integration ./cmd/sqlite2pg/... -run TestResume_ForeignKey -v
package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

func TestResume_ForeignKeyStepPartialFailureCanBeResumed(t *testing.T) {
	ctx := context.Background()
	pgURL := resumeTestPgURL(t)

	dir := t.TempDir()
	sqlitePath := filepath.Join(dir, "fk.db")
	sourceDB, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sourceDB.Close()
	for _, stmt := range []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`INSERT INTO parent (id) VALUES (1), (2)`,
		`CREATE TABLE child_a (id INTEGER PRIMARY KEY, pid INTEGER)`,
		`INSERT INTO child_a (id, pid) VALUES (1, 1), (2, 2)`,
		// child_b has a dangling reference (pid 99, no such parent) — the
		// ALTER TABLE ... ADD CONSTRAINT for it fails, after child_a's has
		// already been issued in the same step.
		`CREATE TABLE child_b (id INTEGER PRIMARY KEY, pid INTEGER)`,
		`INSERT INTO child_b (id, pid) VALUES (1, 99)`,
	} {
		if _, err := sourceDB.Exec(stmt); err != nil {
			t.Fatalf("fixture setup %q: %v", stmt, err)
		}
	}

	idPK := config.ColumnConfig{TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1}
	pidCol := config.ColumnConfig{TargetType: "bigint", Reviewed: true}
	fkToParent := []config.ForeignKey{{Columns: []string{"pid"}, RefTable: "parent", RefColumns: []string{"id"}}}

	cfg := &config.MigrationConfig{
		Source: config.SourceInfo{Path: sqlitePath},
		Tables: map[string]config.TableConfig{
			"parent": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": idPK},
			},
			"child_a": {
				Include:     true,
				ColumnOrder: []string{"id", "pid"},
				Columns:     map[string]config.ColumnConfig{"id": idPK, "pid": pidCol},
				ForeignKeys: fkToParent,
			},
			"child_b": {
				Include:     true,
				ColumnOrder: []string{"id", "pid"},
				Columns:     map[string]config.ColumnConfig{"id": idPK, "pid": pidCol},
				ForeignKeys: fkToParent,
			},
		},
	}

	configPath := filepath.Join(dir, "fk.db.migration.yaml")
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

	// First attempt: every table loads, then the FK step fails on
	// child_b's dangling reference.
	firstErr := executeLoad(cfg, connCfg, false, statePath)
	if firstErr == nil {
		t.Fatal("expected the first load's FK step to fail on child_b's dangling reference")
	}
	if strings.Contains(firstErr.Error(), "already exists") {
		t.Fatalf("first attempt failed with an 'already exists' error, want the genuine FK violation: %v", firstErr)
	}

	st, err := readState(statePath)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if st.FKsApplied {
		t.Fatal("expected FKsApplied=false after the FK step failed")
	}
	if !(contains(st.Completed, "parent") && contains(st.Completed, "child_a") && contains(st.Completed, "child_b")) {
		t.Fatalf("expected all three tables marked completed before the FK step, got %v", st.Completed)
	}

	// The key assertion: child_a's constraint was issued before child_b's
	// failed, but the step is one transaction, so it must have rolled
	// back — no FK constraint on child_a.
	checkConn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	var nConstraints int
	if err := checkConn.QueryRow(ctx,
		`SELECT count(*) FROM pg_constraint WHERE conrelid = '"child_a"'::regclass AND contype = 'f'`,
	).Scan(&nConstraints); err != nil {
		checkConn.Close(ctx)
		t.Fatalf("counting child_a FK constraints: %v", err)
	}
	checkConn.Close(ctx)
	if nConstraints != 0 {
		t.Fatalf("expected child_a to have no FK constraint after the failed step rolled back, got %d", nConstraints)
	}

	// Clear the condition that made child_b's ALTER fail. A resumed run
	// re-adds constraints but does NOT re-COPY the (already-Completed)
	// tables, so the fix has to land in Postgres, not the SQLite source
	// — this stands in for the transient failure modes the finding names
	// (a lock timeout, a dropped connection) clearing between attempts.
	fixConn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := fixConn.Exec(ctx, `UPDATE "child_b" SET pid = 1 WHERE pid = 99`); err != nil {
		fixConn.Close(ctx)
		t.Fatalf("clearing the dangling reference in Postgres: %v", err)
	}
	fixConn.Close(ctx)

	// Resume: before the fix this aborted on child_a's constraint with
	// "constraint ... already exists"; now the step retries cleanly.
	if err := executeLoad(cfg, connCfg, true, statePath); err != nil {
		t.Fatalf("resumed load failed (the M6 bug: FK step not atomic, child_a's constraint left behind): %v", err)
	}

	st, err = readState(statePath)
	if err != nil {
		t.Fatalf("readState after resume: %v", err)
	}
	if !st.FKsApplied {
		t.Fatal("expected FKsApplied=true after a successful resumed FK step")
	}

	verifyConn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer verifyConn.Close(ctx)
	for _, tbl := range []string{"child_a", "child_b"} {
		var n int
		if err := verifyConn.QueryRow(ctx,
			`SELECT count(*) FROM pg_constraint WHERE conrelid = ($1)::regclass AND contype = 'f'`,
			pgx.Identifier{tbl}.Sanitize(),
		).Scan(&n); err != nil {
			t.Fatalf("counting %s FK constraints after resume: %v", tbl, err)
		}
		if n != 1 {
			t.Errorf("expected 1 FK constraint on %s after resume, got %d", tbl, n)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
