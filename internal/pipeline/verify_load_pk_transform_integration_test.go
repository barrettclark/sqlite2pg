//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

// TestVerifyTable_TextPrimaryKeyMappedToBigint_VerifiesClean is issue
// #60's end-to-end proof. A TEXT primary key of plain digit strings is
// auto-mapped to bigint via numeric_text_to_integer. SQLite orders the
// key as text ('1','10','11','2',...); Postgres orders the converted
// bigint (1,2,...,10,11). Before the fix, verifyTableOrdered walked the
// two sides in those different orders and reported almost every row as a
// mismatch on a byte-for-byte-correct load. VerifyTable must now detect
// the PK transform and use the order-independent path, which passes.
func TestVerifyTable_TextPrimaryKeyMappedToBigint_VerifiesClean(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_text_pk_to_bigint"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "label"},
		Columns: map[string]config.ColumnConfig{
			"id":    {TargetType: "bigint", Transform: "numeric_text_to_integer", PrimaryKeySeq: 1, Reviewed: true},
			"label": {TargetType: "text", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, label TEXT);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, label) VALUES
		('1','a'),('2','b'),('3','c'),('4','d'),('5','e'),
		('6','f'),('7','g'),('8','h'),('9','i'),('10','j'),('11','k');`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, sqliteDDL, insertSQL)
	defer fixture.Close()

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.Ordered {
		t.Error("expected Ordered false — a PK column with a transform must degrade to the order-independent path")
	}
	if !result.Passed() {
		t.Fatalf("expected verification to pass, got %d mismatch(es): %+v", result.TotalMismatches(), result.ColumnResults)
	}
}

// TestVerifyTable_TextPrimaryKeyMappedToBigint_StillDetectsCorruption
// confirms the degrade-to-unordered path keeps catching real corruption
// for this shape.
func TestVerifyTable_TextPrimaryKeyMappedToBigint_StillDetectsCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_text_pk_to_bigint_corrupt"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "label"},
		Columns: map[string]config.ColumnConfig{
			"id":    {TargetType: "bigint", Transform: "numeric_text_to_integer", PrimaryKeySeq: 1, Reviewed: true},
			"label": {TargetType: "text", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id TEXT PRIMARY KEY, label TEXT);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, label) VALUES ('1','a'),('2','b'),('3','c');`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, sqliteDDL, insertSQL)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET label = 'ZZZ' WHERE id = 2`, table)); err != nil {
		t.Fatalf("corrupting a row: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.Passed() {
		t.Error("expected verification to fail after a label was corrupted in Postgres")
	}
}
