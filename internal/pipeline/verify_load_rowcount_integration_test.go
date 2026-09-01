//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

// TestVerifyTableUnordered_ReportsErrorWhenPostgresRowsShrinkMidVerify
// covers issue #67's real trigger: a row removed from the Postgres table
// after VerifyTable's upfront COUNT(*) but before the comparison SELECT
// (a concurrent writer). verifyTableUnordered must return a
// "concurrent write during verify" error — not panic in
// compareColumnUnordered, and not silently compare a truncated multiset.
//
// It calls verifyTableUnordered directly, with SourceRowCount pre-set to
// the SQLite count, so the shrink lands in the exact window VerifyTable's
// own COUNT(*) guard can't see.
func TestVerifyTableUnordered_ReportsErrorWhenPostgresRowsShrinkMidVerify(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_rowcount_shrink"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"label", "n"},
		Columns: map[string]config.ColumnConfig{
			"label": {TargetType: "text", Reviewed: true},
			"n":     {TargetType: "bigint", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (label TEXT, n INTEGER);`, table)
	insertSQL := fmt.Sprintf(`INSERT INTO %s (label, n) VALUES ('a', 1), ('b', 2), ('c', 3);`, table)

	fixture := loadVerifyFixtureGeneric(t, pgConn, table, tc, sqliteDDL, insertSQL)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE label = 'b'`, table)); err != nil {
		t.Fatalf("deleting a row: %v", err)
	}

	result := TableVerifyResult{
		Table:          table,
		ColumnResults:  map[string]*ColumnVerifyResult{},
		SourceRowCount: 3,
	}
	_, err := verifyTableUnordered(ctx, fixture, pgConn, table, table, tc, []string{"label", "n"}, result)
	if err == nil {
		t.Fatal("verifyTableUnordered returned nil error after a Postgres row was deleted mid-verify; want a row-count-changed error")
	}
	if !strings.Contains(err.Error(), "concurrent write during verify") {
		t.Errorf("error %q does not name the concurrent-write cause", err)
	}
}
