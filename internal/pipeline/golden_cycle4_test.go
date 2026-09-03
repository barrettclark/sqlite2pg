package pipeline

import "testing"

// TestGolden_SampleVarcharPK is audit cycle 4's Phase B fixture for the
// verify-all-fixtures campaign: two column shapes that used to make
// `verify` mass-false-fail a byte-for-byte-correct load.
//
//   - file_index: a VARCHAR(80)-declared text primary key plus two
//     VARCHAR columns of differing declared length. verify emits
//     ORDER BY "path" COLLATE "C" only if isTextTargetType recognizes
//     "varchar(80)" as text-shaped (audit-final #77 / H1).
//   - legacy_codes: a TEXT primary key of digit strings mapped to a
//     numeric type via numeric_text_to_integer — a transformed PK, which
//     verify must compare with its order-independent path because SQLite
//     orders the key as text and Postgres orders the converted integer
//     (issue #60).
func TestGolden_SampleVarcharPK(t *testing.T) {
	db, path := openFixture(t, "sample-varchar-pk.sqlite")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	fi := result.Config.Tables["file_index"]
	if fi.Columns["path"].TargetType != "varchar(80)" {
		t.Errorf("file_index.path: expected varchar(80), got %q", fi.Columns["path"].TargetType)
	}
	if fi.Columns["path"].PrimaryKeySeq != 1 {
		t.Errorf("file_index.path: expected PrimaryKeySeq 1, got %d", fi.Columns["path"].PrimaryKeySeq)
	}
	if fi.Columns["title"].TargetType != "varchar(45)" || fi.Columns["summary"].TargetType != "varchar(400)" {
		t.Errorf("file_index: expected varchar(45)/varchar(400) for title/summary, got %q/%q",
			fi.Columns["title"].TargetType, fi.Columns["summary"].TargetType)
	}

	lc := result.Config.Tables["legacy_codes"]
	ref := lc.Columns["ref"]
	if ref.TargetType != "integer" || ref.Transform != "numeric_text_to_integer" {
		t.Errorf("legacy_codes.ref: expected integer via numeric_text_to_integer, got %q via %q", ref.TargetType, ref.Transform)
	}
	if ref.PrimaryKeySeq != 1 {
		t.Errorf("legacy_codes.ref: expected PrimaryKeySeq 1, got %d", ref.PrimaryKeySeq)
	}
}

// TestGolden_SampleGeoJSON is audit cycle 4's Phase B fixture for issue
// #61: a TEXT column of GeoJSON with non-canonical whitespace and key
// order, mapped to jsonb via text_to_jsonb. verify canonicalizes both
// sides before comparing, so the load must still verify clean.
func TestGolden_SampleGeoJSON(t *testing.T) {
	db, path := openFixture(t, "sample-geojson.sqlite")
	result, err := ProfileDatabase(db, path, 500, 0.9)
	if err != nil {
		t.Fatalf("ProfileDatabase: %v", err)
	}

	boundary := result.Config.Tables["parcels"].Columns["boundary"]
	if boundary.TargetType != "jsonb" || boundary.Transform != "text_to_jsonb" {
		t.Errorf("parcels.boundary: expected jsonb via text_to_jsonb, got %q via %q", boundary.TargetType, boundary.Transform)
	}
	if boundary.Source != "heuristic:geojson_text" {
		t.Errorf("parcels.boundary: expected source heuristic:geojson_text, got %q", boundary.Source)
	}
}
