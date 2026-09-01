//go:build integration

// Tier 3: reproduces the second regression in e6bc33e's own fix — forcing
// COLLATE "C" on every text-typed primary-key column's Postgres ORDER BY
// assumes the SQLite source column is BINARY-collated, but SQLite allows a
// column to be declared COLLATE NOCASE (or RTRIM), which sorts differently
// from BINARY. When that's the case, SQLite's own ORDER BY (NOCASE) and
// Postgres's forced COLLATE "C" ORDER BY (byte order) can walk genuinely
// correct, identical data in different orders — reintroducing the exact
// false-positive-mismatch bug class e6bc33e was written to fix, just
// triggered from the SQLite side this time. VerifyTable must detect a
// non-BINARY-collated primary-key column and degrade to the
// order-independent aggregate comparison path instead.
package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

func verifyNocaseFixtureConfig() config.TableConfig {
	return config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"fname", "data"},
		Columns: map[string]config.ColumnConfig{
			"fname": {TargetType: "text", Reviewed: true, PrimaryKeySeq: 1},
			"data":  {TargetType: "bigint", Reviewed: true},
		},
	}
}

// verifyNocaseFixtureDDL declares fname COLLATE NOCASE — SQLite's ORDER BY
// on this column sorts case-insensitively ("apple" and "Banana" as if both
// lowercase), which disagrees with Postgres's forced COLLATE "C" byte-order
// ORDER BY ("Banana" < "apple" under byte order, since 'B' = 0x42 <
// 'a' = 0x61).
func verifyNocaseFixtureDDL(table string) string {
	return fmt.Sprintf(`
CREATE TABLE %s (
	fname TEXT PRIMARY KEY COLLATE NOCASE,
	data INTEGER
);
`, table)
}

// TestVerifyTable_NoCasePrimaryKey_DegradesToUnorderedInsteadOfFalsePositive
// proves the fix: a TEXT PRIMARY KEY COLLATE NOCASE column, whose NOCASE
// order genuinely diverges from Postgres's forced COLLATE "C" byte order,
// must not produce a false-positive mismatch — VerifyTable must detect the
// non-BINARY collation and route to the order-independent aggregate
// comparison instead of the (unsafe for this column) PK-ordered path.
func TestVerifyTable_NoCasePrimaryKey_DegradesToUnorderedInsteadOfFalsePositive(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyNocaseFixtureConfig()
	table := "verify_nocase_pk"

	insertSQL := fmt.Sprintf(`INSERT INTO %s (fname, data) VALUES
		('apple', 1),
		('Banana', 2)
	;`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, verifyNocaseFixtureDDL(table), insertSQL)
	defer fixture.Close()

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowCountMismatch {
		t.Fatalf("expected row counts to match, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	if result.Ordered {
		t.Error("expected Ordered false — a NOCASE-collated primary key column must degrade to the unordered aggregate comparison path, not use PK-ordered comparison")
	}
	if total := result.TotalMismatches(); total != 0 {
		t.Errorf("expected 0 mismatches — the data is identical, only NOCASE-vs-C-collation ordering diverges — got %d: %+v", total, result.ColumnResults)
	}
	if !result.Passed() {
		t.Error("expected Passed() true")
	}
}

// TestVerifyTable_NoCasePrimaryKey_StillDetectsRealCorruption confirms the
// degrade-to-unordered fallback doesn't weaken real corruption detection.
// This exercises verifyTableUnordered's aggregate comparison (see
// TestVerifyTable_NoPrimaryKey_AggregateComparisonStillDetectsRealCorruption
// for the same non-1-exact-count rationale), not the ordered path's exact
// single-mismatch guarantee — a single changed value can shift two
// positions in the sorted comparison.
func TestVerifyTable_NoCasePrimaryKey_StillDetectsRealCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyNocaseFixtureConfig()
	table := "verify_nocase_pk_corrupt"

	insertSQL := fmt.Sprintf(`INSERT INTO %s (fname, data) VALUES
		('apple', 1),
		('Banana', 2)
	;`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, verifyNocaseFixtureDDL(table), insertSQL)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`UPDATE %s SET data = 999 WHERE fname = 'apple'`, table)); err != nil {
		t.Fatalf("corrupting row: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, table, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.Ordered {
		t.Error("expected Ordered false — a NOCASE-collated primary key column must degrade to the unordered aggregate comparison path")
	}
	cr := result.ColumnResults["data"]
	if cr == nil || cr.MismatchCount == 0 {
		t.Fatalf("expected at least 1 mismatch for column %q, got none (columns with mismatches: %v)", "data", result.ColumnResults)
	}
	for other, otherResult := range result.ColumnResults {
		if other != "data" && otherResult.MismatchCount != 0 {
			t.Errorf("expected no mismatches in column %q, got %d", other, otherResult.MismatchCount)
		}
	}
	if result.Passed() {
		t.Error("expected Passed() false when a real value is corrupted")
	}
}

// TestVerifyTable_BinaryPrimaryKey_StillUsesOrderedPath confirms the
// common case (no explicit COLLATE, the SQLite default of BINARY) is
// unaffected by this fix — it must still use the cheaper, more precise
// PK-ordered comparison path, not unconditionally degrade to unordered.
func TestVerifyTable_BinaryPrimaryKey_StillUsesOrderedPath(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyCollationFixtureConfig()
	table := "verify_binary_pk_still_ordered"

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
	if !result.Ordered {
		t.Error("expected Ordered true — a plain BINARY-collated (default) text primary key must still use the PK-ordered comparison path")
	}
	if !result.Passed() {
		t.Errorf("expected Passed() true, got %+v", result.ColumnResults)
	}
}
