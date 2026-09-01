//go:build integration

// Tier 3: reproduces the primary-key ORDER BY collation-mismatch false
// positive found during final validation on sqliterepo.db's vcache table
// (composite PK (vid INTEGER, fname TEXT), 1,424/1,525 rows reported as
// false-positive mismatches; confirmed via raw byte-order sort+diff that
// the row SETS were 100% identical, only the relative ORDER differed).
// SQLite's default text comparison is BINARY (byte order); Postgres's
// ORDER BY uses the database's configured collation (this test's target,
// like the campaign's real Postgres instance, uses en_US.UTF-8) unless
// told otherwise via COLLATE. "Makefile.in" sorts before "aclocal.m4"
// under BINARY (uppercase 'M' = 0x4D < lowercase 'a' = 0x61) but after it
// under en_US.UTF-8 — so without forcing a byte-order-equivalent
// collation on the Postgres side of the ORDER BY, VerifyTable's
// primary-key comparison path lines up the wrong rows and reports
// spurious mismatches on data that is, in fact, identical.
package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

// verifyCollationFixtureConfig is a table with a single TEXT primary key —
// the shape that exposes the SQLite-BINARY-vs-Postgres-locale-collation
// ordering divergence — plus a bigint "data" column whose value uniquely
// identifies which source row a given fname belongs to, so a false pairing
// caused by mismatched ORDER BY surfaces as a reported mismatch on data
// even though every (fname, data) pair is, in fact, correctly loaded.
func verifyCollationFixtureConfig() config.TableConfig {
	return config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"fname", "data"},
		Columns: map[string]config.ColumnConfig{
			"fname": {TargetType: "text", Reviewed: true, PrimaryKeySeq: 1},
			"data":  {TargetType: "bigint", Reviewed: true},
		},
	}
}

func verifyCollationFixtureDDL(table string) string {
	return fmt.Sprintf(`
CREATE TABLE %s (
	fname TEXT PRIMARY KEY,
	data INTEGER
);
`, table)
}

// TestVerifyTable_PrimaryKeyOrdering_TextPKSurvivesCollationDivergence
// proves the fix for the ORDER BY collation-mismatch false positive: a
// text-typed primary key whose SQLite BINARY order genuinely differs from
// this Postgres instance's configured collation (en_US.UTF-8, confirmed via
// `SHOW lc_collate` locally, the same non-C-collation shape as the campaign's
// real Postgres instance) must still compare clean, because the Postgres
// side of the ORDER BY is forced to a byte-order-equivalent collation.
func TestVerifyTable_PrimaryKeyOrdering_TextPKSurvivesCollationDivergence(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyCollationFixtureConfig()
	table := "verify_text_pk_collation"

	insertSQL := fmt.Sprintf(`INSERT INTO %s (fname, data) VALUES
		('Makefile.in', 1),
		('aclocal.m4', 2)
	;`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, verifyCollationFixtureDDL(table), insertSQL)
	defer fixture.Close()

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowCountMismatch {
		t.Fatalf("expected row counts to match, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	if !result.Ordered {
		t.Fatal("expected Ordered true when the table has a primary key")
	}
	if total := result.TotalMismatches(); total != 0 {
		t.Errorf("expected 0 mismatches — the data is identical, only the Postgres locale collation's ORDER BY diverges from SQLite's BINARY order — got %d: %+v", total, result.ColumnResults)
	}
	if !result.Passed() {
		t.Error("expected Passed() true")
	}
}

// TestVerifyTable_PrimaryKeyOrdering_TextPKStillDetectsRealCorruption
// confirms the collation fix didn't weaken real corruption detection on a
// text primary key: a genuinely wrong non-PK value must still be caught.
func TestVerifyTable_PrimaryKeyOrdering_TextPKStillDetectsRealCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyCollationFixtureConfig()
	table := "verify_text_pk_collation_corrupt"

	insertSQL := fmt.Sprintf(`INSERT INTO %s (fname, data) VALUES
		('Makefile.in', 1),
		('aclocal.m4', 2)
	;`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, verifyCollationFixtureDDL(table), insertSQL)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET data = 999 WHERE fname = 'Makefile.in'`, table)); err != nil {
		t.Fatalf("corrupting row: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "data")
}
