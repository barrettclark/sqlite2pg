//go:build integration

// Tier 3: VerifyTable against a real Postgres instance — see
// integration_test.go's doc comment for how to run this tier (same PGURL
// convention, same build tag). These tests seed a source SQLite table,
// load it into Postgres for real via the same copywriter pipeline
// `migrate load` uses, then deliberately corrupt individual Postgres
// values (or drop a row) to prove VerifyTable's type-aware comparison
// actually catches what it claims to catch — not just that a clean load
// reports clean.
package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
)

// verifyFixtureConfig is the shared TableConfig for every VerifyTable test
// below: one column of each of the trickier type shapes VerifyTable must
// compare correctly (timestamptz, uuid[], bytea), plus a plain text and a
// plain boolean column so the fixture isn't exclusively edge cases.
func verifyFixtureConfig() config.TableConfig {
	return config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "name", "active", "created_at", "tags", "data"},
		Columns: map[string]config.ColumnConfig{
			"id":         {TargetType: "bigint", Reviewed: true, PrimaryKeySeq: 1},
			"name":       {TargetType: "text", Reviewed: true},
			"active":     {TargetType: "boolean", Transform: "int_to_bool", Reviewed: true},
			"created_at": {TargetType: "timestamptz", Transform: "unix_epoch_seconds", Reviewed: true},
			"tags":       {TargetType: "uuid[]", Transform: "uuid_list_format", Reviewed: true},
			"data":       {TargetType: "bytea", Reviewed: true},
		},
	}
}

// verifyFixtureDDL returns the source SQLite CREATE TABLE for table — the
// SQLite table name must match the Postgres table name VerifyTable is
// given, the same convention the real load pipeline follows (table names
// aren't disambiguated the way column identifiers can be).
func verifyFixtureDDL(table string) string {
	return fmt.Sprintf(`
CREATE TABLE %s (
	id INTEGER PRIMARY KEY,
	name TEXT,
	active INTEGER,
	created_at INTEGER,
	tags TEXT,
	data BLOB
);
`, table)
}

// connectVerifyTestPostgres connects to the Tier 3 test database, skipping
// the test (not failing it) when no Postgres is reachable — same
// convention as loadFixtureEndToEnd's connection in integration_test.go.
func connectVerifyTestPostgres(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgURL(t))
	if err != nil {
		t.Skipf("no Postgres available at %s: %v", pgURL(t), err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	return conn
}

// loadVerifyFixture creates and populates the SQLite source table, then
// creates and loads the matching Postgres table via the real DDL/COPY
// pipeline (not hand-written SQL) — so a clean load here is exactly what
// `migrate load` itself would have produced, and VerifyTable's job is to
// confirm that.
func loadVerifyFixture(t *testing.T, pgConn *pgx.Conn, table string, tc config.TableConfig, insertSQL string) *sql.DB {
	t.Helper()
	ctx := context.Background()

	sqliteDB, _ := openTestDB(t, verifyFixtureDDL(table))
	if _, err := sqliteDB.Exec(insertSQL); err != nil {
		t.Fatalf("inserting fixture rows: %v", err)
	}

	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, pgx.Identifier{table}.Sanitize())); err != nil {
		t.Fatalf("dropping pre-existing table: %v", err)
	}
	stmt, err := ddl.GenerateCreateTable(table, tc)
	if err != nil {
		t.Fatalf("generating DDL: %v", err)
	}
	if _, err := pgConn.Exec(ctx, stmt); err != nil {
		t.Fatalf("creating table %s:\n%s\nerror: %v", table, stmt, err)
	}

	src := copywriter.NewTableSource(sqliteDB, table, tc)
	if _, err := copywriter.LoadTable(ctx, pgConn, table, tc, src); err != nil {
		t.Fatalf("loading table %s: %v", table, err)
	}

	return sqliteDB
}

// fixtureInsert returns the two-row INSERT for table (see verifyFixtureDDL
// — the SQLite and Postgres table names must match).
func fixtureInsert(table string) string {
	return fmt.Sprintf(`INSERT INTO %s (id, name, active, created_at, tags, data) VALUES
	(1, 'alpha', 1, 1700000000, '90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10' || X'00' || 'e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10', X'deadbeef'),
	(2, 'beta', 0, 1700003600, '11111111-1111-1111-1111-111111111111', X'cafef00d')
;`, table)
}

// pgFixtureRow builds one literal Postgres row matching fixtureInsert's
// shape, for corruptOneRow below.
func pgFixtureRow(id int64, name string, active bool, epochSeconds int64, tags []string, dataHex string) string {
	quotedTags := make([]string, len(tags))
	for i, u := range tags {
		quotedTags[i] = "'" + u + "'"
	}
	ts := time.Unix(epochSeconds, 0).UTC().Format(time.RFC3339)
	return fmt.Sprintf(`(%d, '%s', %t, '%s'::timestamptz, ARRAY[%s]::uuid[], '\x%s'::bytea)`,
		id, name, active, ts, strings.Join(quotedTags, ","), dataHex)
}

// correctRow1 and correctRow2 are pgFixtureRow's rendering of
// fixtureInsert's own two rows, exactly as copywriter.Transform would
// convert them — the baseline corruptOneRow modifies one field of.
func correctRow1() string {
	return pgFixtureRow(1, "alpha", true, 1700000000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "deadbeef")
}
func correctRow2() string {
	return pgFixtureRow(2, "beta", false, 1700003600, []string{"11111111-1111-1111-1111-111111111111"}, "cafef00d")
}

// corruptOneRow replaces every row in table with row1 and correctRow2(),
// in that order, via TRUNCATE + a single multi-row INSERT rather than an
// UPDATE. A plain sequential INSERT deterministically appends rows to the
// heap in the given order, which is what VerifyTable's row-by-position
// comparison (see VerifyTable's doc comment) relies on; an UPDATE instead
// writes a new tuple version that Postgres is free to place anywhere in
// the table's physical storage (observed in practice, even for a 2-row
// table, to change sequential-scan order), which would make a corruption
// test like this one flaky by the very mechanism it's trying to test.
func corruptOneRow(t *testing.T, pgConn *pgx.Conn, table, row1 string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`TRUNCATE %s`, pgx.Identifier{table}.Sanitize())); err != nil {
		t.Fatalf("truncating %s: %v", table, err)
	}
	stmt := fmt.Sprintf(`INSERT INTO %s (id, name, active, created_at, tags, data) VALUES %s, %s`,
		pgx.Identifier{table}.Sanitize(), row1, correctRow2())
	if _, err := pgConn.Exec(ctx, stmt); err != nil {
		t.Fatalf("reinserting corrupted rows into %s: %v", table, err)
	}
}

func TestVerifyTable_ExactMatch_NoMismatches(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	fixture := loadVerifyFixture(t, pgConn, "verify_exact_match", verifyFixtureConfig(), fixtureInsert("verify_exact_match"))
	defer fixture.Close()

	result, err := VerifyTable(context.Background(), fixture, pgConn, "verify_exact_match", verifyFixtureConfig())
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowCountMismatch {
		t.Fatalf("expected row counts to match, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	if result.RowsCompared != 2 {
		t.Errorf("expected 2 rows compared, got %d", result.RowsCompared)
	}
	if total := result.TotalMismatches(); total != 0 {
		t.Errorf("expected 0 mismatches on a clean load, got %d: %+v", total, result.ColumnResults)
	}
	if !result.Passed() {
		t.Errorf("expected Passed() true on a clean load")
	}
}

func TestVerifyTable_DetectsRowCountMismatch(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	fixture := loadVerifyFixture(t, pgConn, "verify_row_count_mismatch", tc, fixtureInsert("verify_row_count_mismatch"))
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, `DELETE FROM verify_row_count_mismatch WHERE id = 2`); err != nil {
		t.Fatalf("deleting row to induce mismatch: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, "verify_row_count_mismatch", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if !result.RowCountMismatch {
		t.Fatal("expected a row-count mismatch to be detected")
	}
	if result.SourceRowCount != 2 || result.TargetRowCount != 1 {
		t.Errorf("expected source=2 target=1, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	if result.RowsCompared != 0 {
		t.Errorf("expected the expensive full comparison to be skipped after a row-count mismatch, got RowsCompared=%d", result.RowsCompared)
	}
	if result.Passed() {
		t.Error("expected Passed() false")
	}
}

func TestVerifyTable_DetectsWrongTextValue(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	fixture := loadVerifyFixture(t, pgConn, "verify_wrong_text", tc, fixtureInsert("verify_wrong_text"))
	defer fixture.Close()

	row1 := pgFixtureRow(1, "corrupted", true, 1700000000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "deadbeef")
	corruptOneRow(t, pgConn, "verify_wrong_text", row1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, "verify_wrong_text", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "name")
}

func TestVerifyTable_DetectsWrongBooleanValue(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	fixture := loadVerifyFixture(t, pgConn, "verify_wrong_bool", tc, fixtureInsert("verify_wrong_bool"))
	defer fixture.Close()

	row1 := pgFixtureRow(1, "alpha", false, 1700000000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "deadbeef")
	corruptOneRow(t, pgConn, "verify_wrong_bool", row1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, "verify_wrong_bool", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "active")
}

func TestVerifyTable_DetectsWrongTimestamptzValue(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	fixture := loadVerifyFixture(t, pgConn, "verify_wrong_timestamptz", tc, fixtureInsert("verify_wrong_timestamptz"))
	defer fixture.Close()

	row1 := pgFixtureRow(1, "alpha", true, 1893456000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "deadbeef")
	corruptOneRow(t, pgConn, "verify_wrong_timestamptz", row1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, "verify_wrong_timestamptz", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "created_at")

	mismatch := result.ColumnResults["created_at"].Examples[0]
	expected, ok := mismatch.Expected.(time.Time)
	if !ok {
		t.Fatalf("expected Expected to be a time.Time, got %T", mismatch.Expected)
	}
	actual, ok := mismatch.Actual.(time.Time)
	if !ok {
		t.Fatalf("expected Actual to be a time.Time, got %T", mismatch.Actual)
	}
	if expected.Equal(actual) {
		t.Error("expected the recorded mismatch's expected/actual times to actually differ")
	}
}

func TestVerifyTable_DetectsWrongUUIDListValue(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	fixture := loadVerifyFixture(t, pgConn, "verify_wrong_uuid_list", tc, fixtureInsert("verify_wrong_uuid_list"))
	defer fixture.Close()

	row1 := pgFixtureRow(1, "alpha", true, 1700000000, []string{"00000000-0000-0000-0000-000000000000"}, "deadbeef")
	corruptOneRow(t, pgConn, "verify_wrong_uuid_list", row1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, "verify_wrong_uuid_list", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "tags")
}

func TestVerifyTable_DetectsWrongByteaValue(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	fixture := loadVerifyFixture(t, pgConn, "verify_wrong_bytea", tc, fixtureInsert("verify_wrong_bytea"))
	defer fixture.Close()

	row1 := pgFixtureRow(1, "alpha", true, 1700000000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "00000000")
	corruptOneRow(t, pgConn, "verify_wrong_bytea", row1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, "verify_wrong_bytea", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "data")
}

func TestVerifyTable_CapsExamplesButCountsEveryMismatch(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()

	// 30 rows, every one wrong in the same column — more than
	// maxMismatchExamples, so this proves the cap on stored examples
	// doesn't also cap the reported total count.
	var values string
	for i := 1; i <= 30; i++ {
		values += fmt.Sprintf("(%d, 'row%d', 1, %d, '11111111-1111-1111-1111-111111111111', X'ab'),", i, i, 1700000000+i)
	}
	insert := "INSERT INTO verify_many_mismatches (id, name, active, created_at, tags, data) VALUES " + values[:len(values)-1] + ";"

	fixture := loadVerifyFixture(t, pgConn, "verify_many_mismatches", tc, insert)
	defer fixture.Close()

	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, `UPDATE verify_many_mismatches SET name = 'wrong'`); err != nil {
		t.Fatalf("corrupting rows: %v", err)
	}

	result, err := VerifyTable(ctx, fixture, pgConn, "verify_many_mismatches", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	cr := result.ColumnResults["name"]
	if cr == nil {
		t.Fatal("expected mismatches recorded for name")
	}
	if cr.MismatchCount != 30 {
		t.Errorf("expected the total mismatch count to be uncapped (30), got %d", cr.MismatchCount)
	}
	if len(cr.Examples) != maxMismatchExamples {
		t.Errorf("expected stored examples capped at %d, got %d", maxMismatchExamples, len(cr.Examples))
	}
}

func TestVerifyTable_SkipsTableWithNoIncludedColumns(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"geom"},
		Columns: map[string]config.ColumnConfig{
			"geom": {TargetType: "__drop__"},
		},
	}
	sqliteDB, _ := openTestDB(t, `CREATE TABLE nogeom (geom BLOB);`)
	defer sqliteDB.Close()

	result, err := VerifyTable(context.Background(), sqliteDB, pgConn, "nogeom", tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowsCompared != 0 || len(result.ColumnResults) != 0 {
		t.Errorf("expected a no-op result for a table with no included columns, got %+v", result)
	}
}

// verifyFixtureConfigNoPK is verifyFixtureConfig with the primary key
// stripped from "id" — for tests proving the no-PK aggregate comparison
// path (VerifyTable can't do a position-based comparison at all without a
// key to order by).
func verifyFixtureConfigNoPK() config.TableConfig {
	tc := verifyFixtureConfig()
	id := tc.Columns["id"]
	id.PrimaryKeySeq = 0
	tc.Columns["id"] = id
	return tc
}

// reinsertRowsReversed replaces every row in table with the two given
// pgFixtureRow literals in the OPPOSITE of their natural id order — a
// TRUNCATE + single multi-row INSERT, same technique corruptOneRow uses,
// but here deliberately simulating physical/scan-order drift (the "row 2's
// data lands before row 1's on a plain sequential scan" failure mode
// documented on VerifyTable) rather than a genuine value corruption: the
// DATA is unchanged, only the physical/insertion order differs from
// SQLite's natural rowid order.
func reinsertRowsReversed(t *testing.T, pgConn *pgx.Conn, table, rowForID2, rowForID1 string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pgConn.Exec(ctx, fmt.Sprintf(`TRUNCATE %s`, pgx.Identifier{table}.Sanitize())); err != nil {
		t.Fatalf("truncating %s: %v", table, err)
	}
	stmt := fmt.Sprintf(`INSERT INTO %s (id, name, active, created_at, tags, data) VALUES %s, %s`,
		pgx.Identifier{table}.Sanitize(), rowForID2, rowForID1)
	if _, err := pgConn.Exec(ctx, stmt); err != nil {
		t.Fatalf("reinserting reordered rows into %s: %v", table, err)
	}
}

// TestVerifyTable_PrimaryKeyOrdering_SurvivesPhysicalReorderingOfIdenticalData
// proves the fix for the scan-order-drift false positive: with a primary
// key present, VerifyTable orders both sides by it, so a Postgres table
// whose physical/insertion row order differs from SQLite's natural rowid
// order (id 2 inserted before id 1, exactly the drift signature the tool's
// own doc comment describes) still compares clean, because the comparison
// no longer depends on scan order matching at all.
func TestVerifyTable_PrimaryKeyOrdering_SurvivesPhysicalReorderingOfIdenticalData(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	table := "verify_pk_reordered"
	fixture := loadVerifyFixture(t, pgConn, table, tc, fixtureInsert(table))
	defer fixture.Close()

	reinsertRowsReversed(t, pgConn, table, correctRow2(), correctRow1())

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowCountMismatch {
		t.Fatalf("expected row counts to match, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	if total := result.TotalMismatches(); total != 0 {
		t.Errorf("expected 0 mismatches once ordered by primary key despite physical reordering, got %d: %+v", total, result.ColumnResults)
	}
	if !result.Passed() {
		t.Error("expected Passed() true — the data is identical, only physical order differs")
	}
	if !result.Ordered {
		t.Error("expected Ordered true when the table has a primary key")
	}
}

// TestVerifyTable_PrimaryKeyOrdering_StillDetectsRealCorruption confirms
// the primary-key ordering fix didn't weaken real corruption detection: a
// genuinely wrong value must still be caught, deterministically, even when
// the corrupted row is reinserted out of physical order.
func TestVerifyTable_PrimaryKeyOrdering_StillDetectsRealCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfig()
	table := "verify_pk_reordered_corrupt"
	fixture := loadVerifyFixture(t, pgConn, table, tc, fixtureInsert(table))
	defer fixture.Close()

	corruptedRow1 := pgFixtureRow(1, "corrupted", true, 1700000000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "deadbeef")
	reinsertRowsReversed(t, pgConn, table, correctRow2(), corruptedRow1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	assertSingleMismatch(t, result, "name")
	if !result.Ordered {
		t.Error("expected Ordered true when the table has a primary key")
	}
}

// TestVerifyTable_NoPrimaryKey_AggregateComparisonSurvivesScanOrderDrift
// proves the no-PK fallback: without a key to order by, VerifyTable must
// compare column value multisets instead of trusting scan position, so a
// Postgres table whose physical row order differs from SQLite's (but whose
// DATA is identical) still compares clean.
func TestVerifyTable_NoPrimaryKey_AggregateComparisonSurvivesScanOrderDrift(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfigNoPK()
	table := "verify_nopk_reordered"
	fixture := loadVerifyFixture(t, pgConn, table, tc, fixtureInsert(table))
	defer fixture.Close()

	reinsertRowsReversed(t, pgConn, table, correctRow2(), correctRow1())

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowCountMismatch {
		t.Fatalf("expected row counts to match, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	if total := result.TotalMismatches(); total != 0 {
		t.Errorf("expected 0 mismatches from the aggregate comparison despite physical reordering, got %d: %+v", total, result.ColumnResults)
	}
	if !result.Passed() {
		t.Error("expected Passed() true — the data is identical, only physical order differs")
	}
	if result.Ordered {
		t.Error("expected Ordered false for a table with no primary key")
	}
}

// TestVerifyTable_NoPrimaryKey_AggregateComparisonStillDetectsRealCorruption
// confirms the no-PK aggregate fallback still catches a genuinely wrong
// value even though it can't say which source row it came from.
func TestVerifyTable_NoPrimaryKey_AggregateComparisonStillDetectsRealCorruption(t *testing.T) {
	pgConn := connectVerifyTestPostgres(t)
	tc := verifyFixtureConfigNoPK()
	table := "verify_nopk_corrupt"
	fixture := loadVerifyFixture(t, pgConn, table, tc, fixtureInsert(table))
	defer fixture.Close()

	corruptedRow1 := pgFixtureRow(1, "corrupted", true, 1700000000, []string{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}, "deadbeef")
	corruptOneRow(t, pgConn, table, corruptedRow1)

	result, err := VerifyTable(context.Background(), fixture, pgConn, table, tc)
	if err != nil {
		t.Fatalf("VerifyTable: %v", err)
	}
	if result.RowCountMismatch {
		t.Fatalf("expected row counts to match, got source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	cr := result.ColumnResults["name"]
	if cr == nil {
		t.Fatalf("expected a mismatch recorded for column %q, got none (columns with mismatches: %v)", "name", result.ColumnResults)
	}
	// A single changed value can shift two positions in the sorted
	// comparison (the corrupted value's old spot AND its new spot both
	// stop lining up), so the aggregate path doesn't promise exactly 1
	// mismatch the way the ordered path does — only that it caught
	// something.
	if cr.MismatchCount == 0 {
		t.Errorf("expected at least 1 mismatch for name, got %d", cr.MismatchCount)
	}
	for other, otherResult := range result.ColumnResults {
		if other != "name" && otherResult.MismatchCount != 0 {
			t.Errorf("expected no mismatches in column %q, got %d", other, otherResult.MismatchCount)
		}
	}
	if result.Passed() {
		t.Error("expected Passed() false when a real value is corrupted")
	}
	if result.Ordered {
		t.Error("expected Ordered false for a table with no primary key")
	}
}

func assertSingleMismatch(t *testing.T, result TableVerifyResult, column string) {
	t.Helper()
	if result.RowCountMismatch {
		t.Fatalf("unexpected row-count mismatch: source=%d target=%d", result.SourceRowCount, result.TargetRowCount)
	}
	cr := result.ColumnResults[column]
	if cr == nil {
		t.Fatalf("expected a mismatch recorded for column %q, got none (columns with mismatches: %v)", column, result.ColumnResults)
	}
	if cr.MismatchCount != 1 {
		t.Errorf("expected exactly 1 mismatch for %q, got %d", column, cr.MismatchCount)
	}
	for other, otherResult := range result.ColumnResults {
		if other != column && otherResult.MismatchCount != 0 {
			t.Errorf("expected no mismatches in column %q, got %d", other, otherResult.MismatchCount)
		}
	}
	if result.Passed() {
		t.Error("expected Passed() false when a mismatch was recorded")
	}
}
