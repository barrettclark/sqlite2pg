//go:build integration

// Reproduces issue #77 (whole-codebase audit, finding H1): isTextTargetType
// only ever matched a bare "varchar" target type, but the only varchar-shaped
// target type this codebase actually produces is "varchar(N)" (varcharFinding,
// profile.go). A VARCHAR(N) primary key therefore never got the COLLATE "C"
// forced onto its ORDER BY expression, so Postgres ordered by the database's
// locale collation while SQLite ordered BINARY — the same false-positive
// shape verify_load_collation_test.go already covers for TargetType "text",
// here for TargetType "varchar(N)" instead.
package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

func verifyVarcharPKFixtureConfig() config.TableConfig {
	return config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"fname", "data"},
		Columns: map[string]config.ColumnConfig{
			"fname": {TargetType: "varchar(50)", Reviewed: true, PrimaryKeySeq: 1},
			"data":  {TargetType: "bigint", Reviewed: true},
		},
	}
}

func verifyVarcharPKFixtureDDL(table string) string {
	return fmt.Sprintf(`
CREATE TABLE %s (
	fname VARCHAR(50) PRIMARY KEY,
	data INTEGER
);
`, table)
}

// TestVerifyTable_PrimaryKeyOrdering_VarcharPKSurvivesCollationDivergence
// proves the fix for issue #77: a varchar(N)-typed primary key whose SQLite
// BINARY order genuinely differs from this Postgres instance's configured
// collation must still compare clean, because the Postgres side of the
// ORDER BY is forced to a byte-order-equivalent collation.
func TestVerifyTable_PrimaryKeyOrdering_VarcharPKSurvivesCollationDivergence(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyVarcharPKFixtureConfig()
	table := "verify_varchar_pk_collation"

	insertSQL := fmt.Sprintf(`INSERT INTO %s (fname, data) VALUES
		('Makefile.in', 1),
		('aclocal.m4', 2)
	;`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, verifyVarcharPKFixtureDDL(table), insertSQL)
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

// TestVerifyTable_PrimaryKeyOrdering_VarcharPKStillDetectsRealCorruption
// confirms the fix didn't weaken real corruption detection on a varchar(N)
// primary key.
func TestVerifyTable_PrimaryKeyOrdering_VarcharPKStillDetectsRealCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyVarcharPKFixtureConfig()
	table := "verify_varchar_pk_collation_corrupt"

	insertSQL := fmt.Sprintf(`INSERT INTO %s (fname, data) VALUES
		('Makefile.in', 1),
		('aclocal.m4', 2)
	;`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, verifyVarcharPKFixtureDDL(table), insertSQL)
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
