//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

// TestVerifyTable_BlobRowInTextColumn_VerifiesCleanAfterRealLoad is issue
// #83's end-to-end proof. A TEXT-declared column whose 500-row sample was
// all strings resolves to a plain "text" target with no transform
// (fallbackTypeFor); SQLite's dynamic typing still permits a BLOB row to
// live in that same column, and pgx's TextCodec accepts []byte for a text
// column at COPY time without complaint — the load is correct. Before the
// fix, verify's expected ([]byte, raw from SQLite) vs actual (string,
// scanned back via pgtype.Text) shape mismatch fell through to a %v
// fallback that renders the two differently, reporting a false mismatch
// on the BLOB row.
func TestVerifyTable_BlobRowInTextColumn_VerifiesCleanAfterRealLoad(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_blob_in_text"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "note"},
		Columns: map[string]config.ColumnConfig{
			"id":   {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"note": {TargetType: "text", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, note TEXT);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, note) VALUES
		(1, 'ordinary string'),
		(2, x'68656c6c6f'),
		(3, 'another string');`, table)

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

// TestVerifyTable_BlobRowInTextColumn_StillDetectsRealCorruption confirms
// the fix doesn't blind verify to a genuinely wrong value in the same
// column.
func TestVerifyTable_BlobRowInTextColumn_StillDetectsRealCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_blob_in_text_corrupt"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "note"},
		Columns: map[string]config.ColumnConfig{
			"id":   {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"note": {TargetType: "text", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, note TEXT);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, note) VALUES (1, x'68656c6c6f');`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, sqliteDDL, insertSQL)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET note = 'wrong' WHERE id = 1`, table)); err != nil {
		t.Fatalf("corrupting the value: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.Passed() {
		t.Error("expected verification to fail after the value was changed in Postgres")
	}
}
