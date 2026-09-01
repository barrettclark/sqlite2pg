//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

// TestVerifyTable_JsonbColumn_VerifiesCleanDespitePostgresCanonicalization
// is issue #61's end-to-end proof. A TEXT column of compact GeoJSON is
// mapped to jsonb via text_to_jsonb (validate-only). Postgres rewrites it
// on storage — a space after every ':' and ',', object keys reordered.
// Before the fix, verify compared Postgres's canonical string against the
// row's original compact string and reported every row as a mismatch on a
// correct load. It must now verify clean.
func TestVerifyTable_JsonbColumn_VerifiesCleanDespitePostgresCanonicalization(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_jsonb_geojson"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "geom"},
		Columns: map[string]config.ColumnConfig{
			"id":   {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"geom": {TargetType: "jsonb", Transform: "text_to_jsonb", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, geom TEXT);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, geom) VALUES
		(1, '{"type":"Point","coordinates":[1,2]}'),
		(2, '{"coordinates":[3.5,4.5],"type":"Point"}'),
		(3, '{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]],"properties":{"n":1e3}}');`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, sqliteDDL, insertSQL)
	defer fixture.Close()

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if !result.Passed() {
		t.Fatalf("expected verification to pass, got %d mismatch(es): %+v", result.TotalMismatches(), result.ColumnResults)
	}
}

// TestVerifyTable_JsonbColumn_StillDetectsRealDifference confirms the
// canonicalization doesn't blind verify to a genuinely different document.
func TestVerifyTable_JsonbColumn_StillDetectsRealDifference(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_jsonb_geojson_corrupt"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "geom"},
		Columns: map[string]config.ColumnConfig{
			"id":   {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"geom": {TargetType: "jsonb", Transform: "text_to_jsonb", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, geom TEXT);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, geom) VALUES (1, '{"type":"Point","coordinates":[1,2]}');`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, sqliteDDL, insertSQL)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET geom = '{"type":"Point","coordinates":[9,9]}'::jsonb WHERE id = 1`, table)); err != nil {
		t.Fatalf("corrupting the jsonb value: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.Passed() {
		t.Error("expected verification to fail after the jsonb value was changed in Postgres")
	}
}
