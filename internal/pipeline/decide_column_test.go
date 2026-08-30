package pipeline

import (
	"testing"

	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/sqlitereader"
)

func TestDecideColumn_FlagsForReviewWhenFullTableCheckFindsAViolationOutsideTheSample(t *testing.T) {
	// Real bug (issue #13): a small sample can look entirely UUID-shaped
	// while the full table has a genuine exception the sample never drew.
	// The sample passed in here deliberately omits the violating row
	// ("811171", present in the table itself) — exactly what a real
	// random sample missing a rare exception looks like from
	// decideColumn's point of view.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO items (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10'),
		('811171')`)

	col := sqlitereader.ColumnInfo{Name: "mb_id", DeclaredType: "TEXT"}
	sample := []any{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "uuid" {
		t.Errorf("expected the suggested type uuid to still be shown, got %q", cc.TargetType)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once a violation was found, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once the full-table check found a violation")
	}
}

func TestDecideColumn_FlagsForReviewWhenFullTableHasInvalidJSONTheSampleMissed(t *testing.T) {
	// Issue #22: text_to_jsonb used to be a bare pass-through that could
	// never fail, so the geojson_text heuristic's auto-approval survived a
	// full-table check that was actually a no-op. Here 499 sampled rows
	// (represented by the two below, standing in for a sample the
	// geojson_text heuristic finds 100% clean) are valid GeoJSON, but one
	// full-table row holds "N/A" — a real-world value the sample never
	// drew. That row must now be caught and routed to review instead of
	// silently auto-approved.
	db, _ := openTestDB(t, `CREATE TABLE places (id INTEGER PRIMARY KEY, geom TEXT);`)
	db.Exec(`INSERT INTO places (geom) VALUES
		('{"type":"Point","coordinates":[1,2]}'),
		('{"type":"Point","coordinates":[3,4]}'),
		('N/A')`)

	col := sqlitereader.ColumnInfo{Name: "geom", DeclaredType: "TEXT"}
	sample := []any{
		`{"type":"Point","coordinates":[1,2]}`,
		`{"type":"Point","coordinates":[3,4]}`,
	}

	cc, unresolved, err := decideColumn(db, "places", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "jsonb" {
		t.Errorf("expected the suggested type jsonb to still be shown, got %q", cc.TargetType)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once a violation was found, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once the full-table check found the invalid-JSON row")
	}
}

func TestDecideColumn_AutoApprovesWhenTheFullTableCheckFindsNoViolation(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, mb_id TEXT);`)
	db.Exec(`INSERT INTO items (mb_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10')`)

	col := sqlitereader.ColumnInfo{Name: "mb_id", DeclaredType: "TEXT"}
	sample := []any{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "uuid" {
		t.Errorf("expected uuid, got %q", cc.TargetType)
	}
	if cc.Confidence < 0.9 {
		t.Errorf("expected the original confidence preserved when the full-table check confirms the sample, got %f", cc.Confidence)
	}
	if unresolved != nil {
		t.Errorf("expected no UnresolvedCase, got %+v", unresolved)
	}
}

func TestDecideColumn_FlagsForReviewWhenFullTableHasAnInt4OverflowTheSampleMissed(t *testing.T) {
	// Issue #15: numeric_text's int4-vs-int8 sizing (sawOutOfInt4Range) is
	// decided from the sample alone. A sample that only shows int4-range
	// values auto-approves the column as "integer" even when the full
	// table contains a value that would overflow it and fail at COPY time.
	// The sample here deliberately omits the overflowing row, mirroring
	// the uuid case above.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, legacy_id TEXT);`)
	db.Exec(`INSERT INTO items (legacy_id) VALUES ('100'), ('200'), ('9999999999')`)

	col := sqlitereader.ColumnInfo{Name: "legacy_id", DeclaredType: "TEXT"}
	sample := []any{"100", "200"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "integer" {
		t.Errorf("expected the suggested type integer to still be shown, got %q", cc.TargetType)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once the int4 overflow was found, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once the full-table check found an int4 overflow")
	}
}

func TestDecideColumn_SkipsTheFullTableCheckWhenAlreadyFlaggedForReview(t *testing.T) {
	// A column already below threshold (or with disagreeing heuristics)
	// gets no benefit from a full-table check — it's already headed to
	// review, so there's no point paying for the extra scan.
	db, _ := openTestDB(t, `CREATE TABLE bikes (id INTEGER PRIMARY KEY, comp INTEGER);`)
	db.Exec(`INSERT INTO bikes (comp) VALUES (0), (1), (0)`)

	col := sqlitereader.ColumnInfo{Name: "comp", DeclaredType: "INTEGER"}
	sample := []any{int64(0), int64(1), int64(0)}

	cc, unresolved, err := decideColumn(db, "bikes", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected the ambiguous boolean01 confidence to stay below threshold, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase")
	}
}

func TestDecideColumn_FlagsForReviewWhenDefaultPassthroughSampleMixesIncompatibleStorageClasses(t *testing.T) {
	// Issue #16: SQLite's dynamic typing legally allows an
	// INTEGER-declared column to hold TEXT-storage values in any row
	// (e.g. atomic_database.db's XRAY_ENERGIES.Inner/Outer, declared INT
	// but containing subshell codes like "K"/"L1"/"M3" alongside real
	// integers). No heuristic claims this column, so it used to fall
	// straight through to default_passthrough at a blind 0.99 confidence
	// based only on the declared type / majority sample type — crashing
	// at COPY time on the first text value. A sample containing both
	// numeric and non-numeric-text values must not auto-approve.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, qty INTEGER);`)
	db.Exec(`INSERT INTO items (qty) VALUES (1), (2), ('lots-of-it')`)

	col := sqlitereader.ColumnInfo{Name: "qty", DeclaredType: "INTEGER"}
	sample := []any{int64(1), int64(2), "lots-of-it"}

	cc, unresolved, err := decideColumn(db, "items", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once the sample showed a non-numeric value in an integer-targeted column, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once default_passthrough's own sample contradicted its declared-type guess")
	}
}

func TestDecideColumn_PersistsDisagreementTieAsNeedsReview(t *testing.T) {
	// Issue #20 bug 2: when resolver.Decide flags needsReview because two
	// findings genuinely tie (as opposed to one being simply below
	// threshold), decideColumn used to store best.Confidence unchanged —
	// e.g. 0.95, above any sane --threshold — so `migrate load`'s
	// confidence-only gate and the TUI's confidence-only NeedsReview both
	// silently missed it. NeedsReview must persist the verdict
	// independently of Confidence.
	//
	// last_validation_date naturally produces a single yyyymmdd_date
	// finding at 0.95 with no real competitor (numeric_text sits 0.05
	// below it, a deliberate clean win — see yyyymmdd_date.go). To
	// reproduce a genuine tie without relying on two real heuristics
	// happening to collide, an extraFinding at the identical 0.95
	// confidence is injected directly.
	db, _ := openTestDB(t, `CREATE TABLE date_demo (id INTEGER PRIMARY KEY, last_validation_date TEXT);`)
	db.Exec(`INSERT INTO date_demo (last_validation_date) VALUES ('20210927'), ('20211015')`)

	col := sqlitereader.ColumnInfo{Name: "last_validation_date", DeclaredType: "TEXT"}
	sample := []any{"20210927", "20211015"}
	tiedFinding := profiler.Finding{
		Heuristic:     "synthetic_tie",
		SuggestedType: "integer",
		Confidence:    0.95,
		Rationale:     "synthetic finding forcing an exact-confidence tie for test purposes",
	}

	cc, unresolved, err := decideColumn(db, "date_demo", col, sample, 0.9, tiedFinding)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Confidence < 0.9 {
		t.Errorf("expected Confidence to stay at the winning finding's original value (>= 0.9), got %f — Confidence alone must not be the only signal", cc.Confidence)
	}
	if !cc.NeedsReview {
		t.Error("expected NeedsReview=true to persist the disagreement-tie verdict even though Confidence stayed above threshold")
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once two findings tied")
	}
}

func TestDecideColumn_FlagsForReviewWhenNumericDeclaredColumnHasASentinelString(t *testing.T) {
	// Issue #25: a NUMERIC(10,2)/DECIMAL-declared column with one
	// catch-all sentinel row (e.g. 'Unknown') isn't recognized by
	// sentinel_null.AppliesTo (it only checks for INT/REAL/FLOA/DOUB in
	// the declared type), so no heuristic claims the column and it falls
	// through to default_passthrough. fallbackTypeFor sees sawFloat and
	// picks "double precision" before ever noticing the sentinel string
	// was also sampled. This must not auto-approve at 0.99 confidence —
	// either sentinel_null needs to recognize NUMERIC/DECIMAL, or (as
	// verified here) issue #16's fallbackSampleMismatch check already
	// catches the string value default_passthrough's own target can't
	// hold, exactly as it does for the INTEGER-declared case.
	db, _ := openTestDB(t, `CREATE TABLE payments (id INTEGER PRIMARY KEY, amount NUMERIC(10,2));`)
	db.Exec(`INSERT INTO payments (amount) VALUES (100.50), (200.75), ('Unknown')`)

	col := sqlitereader.ColumnInfo{Name: "amount", DeclaredType: "NUMERIC(10,2)"}
	sample := []any{100.50, 200.75, "Unknown"}

	cc, unresolved, err := decideColumn(db, "payments", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once the sample showed a sentinel string in a NUMERIC-declared column, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once default_passthrough's own sample contradicted its NUMERIC declared-type guess")
	}
}

func TestDecideColumn_FlagsForReviewWhenAPrimaryKeyColumnHasAnEmptyStringUUID(t *testing.T) {
	// Issue #31: uuid_format maps "" to NULL by design (to tolerate an
	// optional-UUID column like beets' albums.mb_albumid). That's fine for
	// an ordinary column, but station_id here is the table's primary key —
	// auto-approving uuid_format would let a NULL reach COPY's PRIMARY KEY
	// (station_id) clause and abort the whole load with a not-null
	// violation. The sample below is entirely clean UUIDs (mirroring a
	// sample that missed the one empty-string row in the full table), so
	// only the full-table NULL check catches this.
	db, _ := openTestDB(t, `CREATE TABLE stations (station_id TEXT PRIMARY KEY);`)
	db.Exec(`INSERT INTO stations (station_id) VALUES
		('90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10'),
		('e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10'),
		('')`)

	col := sqlitereader.ColumnInfo{Name: "station_id", DeclaredType: "TEXT", PrimaryKeySeq: 1}
	sample := []any{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "e4eff6f3-3f1a-4d6e-9c1e-7c3d2a5b9e10"}

	cc, unresolved, err := decideColumn(db, "stations", col, sample, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.TargetType != "uuid" {
		t.Errorf("expected the suggested type uuid to still be shown, got %q", cc.TargetType)
	}
	if cc.Confidence >= 0.9 {
		t.Errorf("expected confidence dropped below threshold once a primary-key NULL was found, got %f", cc.Confidence)
	}
	if unresolved == nil {
		t.Fatal("expected an UnresolvedCase once the full-table check found a primary-key column resolving to NULL")
	}
}

func TestDecideColumn_ReviewedAlwaysStartsFalseRegardlessOfConfidence(t *testing.T) {
	// Reviewed means "a human confirmed this," a separate concept from
	// confidence/needs-review — profiling never sets it, whether a column
	// auto-approves or not.
	db, _ := openTestDB(t, `CREATE TABLE bikes (id INTEGER PRIMARY KEY, num INTEGER);`)
	db.Exec(`INSERT INTO bikes (num) VALUES (3)`)

	col := sqlitereader.ColumnInfo{Name: "num", DeclaredType: "INTEGER"}
	cc, _, err := decideColumn(db, "bikes", col, []any{int64(3)}, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.Reviewed {
		t.Error("expected Reviewed=false even for a clean default_passthrough decision")
	}
}

func TestDecideColumn_CarriesNotNullThrough(t *testing.T) {
	// Issue #34: NotNull is read from the source schema but was never
	// carried through to config.ColumnConfig, so a source `NOT NULL`
	// column silently lost that constraint in the generated DDL.
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, email TEXT NOT NULL, nickname TEXT);`)
	db.Exec(`INSERT INTO items (email, nickname) VALUES ('a@example.com', 'a')`)

	notNullCol := sqlitereader.ColumnInfo{Name: "email", DeclaredType: "TEXT", NotNull: true}
	cc, _, err := decideColumn(db, "items", notNullCol, []any{"a@example.com"}, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if !cc.NotNull {
		t.Error("expected NotNull=true carried through from the source column's declared NOT NULL")
	}

	nullableCol := sqlitereader.ColumnInfo{Name: "nickname", DeclaredType: "TEXT", NotNull: false}
	cc, _, err = decideColumn(db, "items", nullableCol, []any{"a"}, 0.9)
	if err != nil {
		t.Fatalf("decideColumn: %v", err)
	}
	if cc.NotNull {
		t.Error("expected NotNull=false for a column with no source NOT NULL constraint")
	}
}
