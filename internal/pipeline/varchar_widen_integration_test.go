//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
)

// TestVarcharSuggestion_WidenedLengthActuallyLoads is issue #84's
// end-to-end proof: a VARCHAR(N)-declared column whose real data exceeds N
// (SQLite never enforces the declared length) must load successfully once
// the reviewer accepts the suggested varchar(target) as ProfileDatabase
// now shows it — not abort COPY with "value too long for type character
// varying(N)" the way accepting the un-widened declared N would have.
func TestVarcharSuggestion_WidenedLengthActuallyLoads(t *testing.T) {
	sqliteDB, path := openTestDB(t, `
		CREATE TABLE customers (
			id INTEGER PRIMARY KEY,
			first_name VARCHAR(5),
			city VARCHAR(100)
		);
	`)
	sqliteDB.Exec(`INSERT INTO customers (first_name, city) VALUES (?, ?)`, "Alex", "Springfield")
	sqliteDB.Exec(`INSERT INTO customers (first_name, city) VALUES (?, ?)`, "Bartholomew", "Shelbyville")

	result, err := ProfileDatabase(sqliteDB, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}
	tc := result.Config.Tables["customers"]
	firstName := tc.Columns["first_name"]
	if firstName.TargetType != "varchar(11)" {
		t.Fatalf("expected first_name widened to varchar(11), got %q", firstName.TargetType)
	}
	for name, col := range tc.Columns {
		col.Reviewed = true
		tc.Columns[name] = col
	}
	result.Config.Tables["customers"] = tc

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgURL(t))
	if err != nil {
		t.Skipf("no Postgres available at %s: %v", pgURL(t), err)
	}
	t.Cleanup(func() { conn.Close(ctx) })

	pgTable := "verify_varchar_widen_customers"
	if _, err := conn.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %q`, pgTable)); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	stmt, err := ddl.GenerateCreateTable(pgTable, tc)
	if err != nil {
		t.Fatalf("generating DDL: %v", err)
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		t.Fatalf("creating table:\n%s\nerror: %v", stmt, err)
	}
	t.Cleanup(func() { conn.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %q`, pgTable)) })

	src := copywriter.NewTableSource(sqliteDB, "customers", tc)
	if _, err := copywriter.LoadTable(ctx, conn, pgTable, tc, src); err != nil {
		t.Fatalf("loading table (the widened-length fix should have prevented this): %v", err)
	}

	var loadedCount int
	if err := conn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q`, pgTable)).Scan(&loadedCount); err != nil {
		t.Fatalf("counting loaded rows: %v", err)
	}
	if loadedCount != 2 {
		t.Errorf("expected 2 rows loaded, got %d", loadedCount)
	}
}
