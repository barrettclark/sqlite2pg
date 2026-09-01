//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

// TestVerifyTable_JsonbColumn_BlobRowVerifiesCleanDespitePostgresCanonicalization
// is Copilot's PR #98 finding's end-to-end proof: a GeoJSON column can
// have a rare BLOB row (SQLite's dynamic typing permits it — issue #83's
// shape), and text_to_jsonb must return that row's transformed value as a
// string, not the raw []byte, or expectedForCompare's string-only
// canonicalization check (issue #61) never runs for it — reintroducing
// #61's exact false-mismatch bug the moment Postgres reformats whitespace
// or object-key order on storage.
func TestVerifyTable_JsonbColumn_BlobRowVerifiesCleanDespitePostgresCanonicalization(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_jsonb_geojson_blob"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "geom"},
		Columns: map[string]config.ColumnConfig{
			"id":   {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"geom": {TargetType: "jsonb", Transform: "text_to_jsonb", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, geom);`, table)
	// Row 1 is a genuine BLOB (SQLite CAST(... AS BLOB)), deliberately
	// with object keys out of Postgres's canonical sort order and no
	// space after ':'/',' so a false mismatch would show up immediately
	// if canonicalization didn't run.
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, geom) VALUES
		(1, CAST('{"type":"Point","coordinates":[1,2]}' AS BLOB)),
		(2, '{"coordinates":[3.5,4.5],"type":"Point"}');`, table)

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
