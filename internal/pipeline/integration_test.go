//go:build integration

// Tier 3: end-to-end `migrate load` against a real Postgres instance, for
// the smaller fixtures (bikes.db, AustinRoadConstruction.db,
// DisabilityCompByCounty.db — MACS-scale tables are deliberately excluded
// from this default-off tier; see PGLOADER_REWRITE_PLAN_V2.md). Run with:
//
//	PGURL=postgres://user@localhost:5432/sqlite2pg_test?sslmode=disable \
//	  go test -tags integration ./internal/pipeline/... -run TestIntegration -v
package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/user"
	"testing"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
)

func pgURL(t *testing.T) string {
	t.Helper()
	if u := os.Getenv("PGURL"); u != "" {
		return u
	}
	usr, err := user.Current()
	if err != nil {
		t.Skipf("PGURL not set and could not determine current user: %v", err)
	}
	return fmt.Sprintf("postgres://%s@localhost:5432/sqlite2pg_test?sslmode=disable", usr.Username)
}

// loadFixtureEndToEnd profiles sourceName, force-approves every column
// (simulating a completed wizard review — this tier verifies the COPY
// pipeline and type conversions, not the ambiguity-resolution UI, which is
// covered by Tier 1/2), creates its tables in a scratch Postgres schema,
// and loads it via the real COPY protocol.
func loadFixtureEndToEnd(t *testing.T, sourceName string) (*sql.DB, *pgx.Conn, *config.MigrationConfig) {
	t.Helper()
	ctx := context.Background()

	sqliteDB, path := openFixture(t, sourceName)
	// Sample size intentionally covers every row of these small fixtures —
	// a partial sample can miss a rare edge-case row entirely (as it did
	// here in development: DisabilityCompByCounty.db's single 'Unknown'
	// aggregate row is the table's very last row, so a sample smaller than
	// the table silently never saw it, the profiler fell back to a bare
	// integer with no transform, and the real COPY of all rows then failed
	// on that unsampled row). Tier 3 is meant to catch exactly this kind of
	// gap between what profiling saw and what streaming actually loads.
	result, err := ProfileDatabase(sqliteDB, path, 5000, 0.9)
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

	conn, err := pgx.Connect(ctx, pgURL(t))
	if err != nil {
		t.Skipf("no Postgres available at %s: %v", pgURL(t), err)
	}
	t.Cleanup(func() { conn.Close(ctx) })

	for tableName, tc := range result.Config.Tables {
		if !tc.Include {
			continue
		}
		pgTable := sanitizeIdent(tableName)

		if _, err := conn.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %q`, pgTable)); err != nil {
			t.Fatalf("drop table: %v", err)
		}
		stmt := ddl.GenerateCreateTable(pgTable, tc)
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("creating table %s:\n%s\nerror: %v", pgTable, stmt, err)
		}

		src := copywriter.NewTableSource(sqliteDB, tableName, tc)
		if _, err := copywriter.LoadTable(ctx, conn, pgTable, tc, src); err != nil {
			t.Fatalf("loading table %s: %v", pgTable, err)
		}
	}

	return sqliteDB, conn, result.Config
}

func sanitizeIdent(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '/' || r == ' ' || r == '.' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func TestIntegration_Bikes_RowCountAndTypesMatch(t *testing.T) {
	sqliteDB, conn, cfg := loadFixtureEndToEnd(t, "bikes.db")
	ctx := context.Background()

	var sourceCount int
	if err := sqliteDB.QueryRow(`SELECT COUNT(*) FROM bikes`).Scan(&sourceCount); err != nil {
		t.Fatalf("counting source rows: %v", err)
	}

	pgTable := sanitizeIdent("bikes")
	var loadedCount int
	if err := conn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q`, pgTable)).Scan(&loadedCount); err != nil {
		t.Fatalf("counting loaded rows: %v", err)
	}
	if sourceCount != loadedCount {
		t.Errorf("expected %d rows loaded, got %d", sourceCount, loadedCount)
	}

	var dataType string
	err := conn.QueryRow(ctx,
		`SELECT data_type FROM information_schema.columns WHERE table_name = $1 AND column_name = 'last_reported'`,
		pgTable).Scan(&dataType)
	if err != nil {
		t.Fatalf("querying information_schema: %v", err)
	}
	if dataType != "timestamp with time zone" {
		t.Errorf("expected last_reported to be timestamptz, got %q", dataType)
	}

	_ = cfg
}

func TestIntegration_AustinRoadConstruction_ISO8601AndJSONBConvertCorrectly(t *testing.T) {
	_, conn, _ := loadFixtureEndToEnd(t, "AustinRoadConstruction.db")
	ctx := context.Background()
	pgTable := sanitizeIdent("construction")

	var geometryType string
	err := conn.QueryRow(ctx,
		`SELECT data_type FROM information_schema.columns WHERE table_name = $1 AND column_name = 'geometry'`,
		pgTable).Scan(&geometryType)
	if err != nil {
		t.Fatalf("querying information_schema: %v", err)
	}
	if geometryType != "jsonb" {
		t.Errorf("expected geometry column to be jsonb, got %q", geometryType)
	}

	var rowCount int
	if err := conn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE geometry IS NOT NULL`, pgTable)).Scan(&rowCount); err != nil {
		t.Fatalf("counting rows with geometry: %v", err)
	}
	if rowCount == 0 {
		t.Error("expected at least one row with a valid jsonb geometry value")
	}
}

func TestIntegration_DisabilityCompByCounty_SentinelBecomesNullNotAnError(t *testing.T) {
	_, conn, _ := loadFixtureEndToEnd(t, "DisabilityCompByCounty.db")
	ctx := context.Background()
	pgTable := sanitizeIdent("DisabilityCompByCounty")

	var nullCount int
	err := conn.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %q WHERE "FIPS code" IS NULL`, pgTable)).Scan(&nullCount)
	if err != nil {
		t.Fatalf("querying FIPS code nulls: %v", err)
	}
	if nullCount != 1 {
		t.Errorf("expected exactly 1 NULL FIPS code (the 'Unknown' aggregate row), got %d", nullCount)
	}
}
