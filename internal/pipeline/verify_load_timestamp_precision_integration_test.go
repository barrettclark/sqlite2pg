//go:build integration

package pipeline

import (
	"context"
	"fmt"
	"testing"

	"sqlite2pg/internal/config"
)

// TestVerifyTable_ExcelSerialTimestamp_SubMicrosecondPasses is issue #63's
// end-to-end proof: an Excel/Access serial datetime whose fractional part
// yields sub-microsecond nanoseconds, loaded into a timestamptz column via
// excel_serial_to_timestamptz, must verify clean — Postgres rounds the
// value to microseconds on the way in, and verify recomputes the exact
// nanosecond value, so the comparison has to happen at microsecond
// resolution or every such row is a false mismatch.
func TestVerifyTable_ExcelSerialTimestamp_SubMicrosecondPasses(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	table := "verify_excel_serial_ts"
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "ts"},
		Columns: map[string]config.ColumnConfig{
			"id": {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"ts": {TargetType: "timestamptz", Transform: "excel_serial_to_timestamptz", Reviewed: true},
		},
	}
	sqliteDDL := fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, ts REAL);`, table)
	// 45000.4567 -> 2023-03-15T10:57:38.880000212Z (sub-µs ns);
	// 43831.123456 -> 2020-01-01T02:57:46.598400101Z.
	insertSQL := fmt.Sprintf(`INSERT INTO %s (id, ts) VALUES (1, 45000.4567), (2, 43831.123456);`, table)

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
