package heuristics

import (
	"testing"
	"time"

	"sqlite2pg/internal/profiler"
)

func TestISO8601_DetectsISO8601Timestamps(t *testing.T) {
	h := ISO8601{}
	meta := profiler.ColumnMeta{Table: "austinroadconstruction", Name: "created_at", DeclaredType: "TEXT"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for a TEXT column")
	}

	samples := []profiler.Value{"2026-08-14T18:01:38.401Z", "2026-08-01T00:00:00Z", nil}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for ISO 8601 timestamp strings")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
}

func TestISO8601_AppliesToDatetimeDeclaredColumns(t *testing.T) {
	h := ISO8601{}
	// chinook.db's employees.BirthDate/HireDate: declared DATETIME, not
	// TEXT/CHAR — AppliesTo must not gate this heuristic out entirely.
	if !h.AppliesTo(profiler.ColumnMeta{Name: "BirthDate", DeclaredType: "DATETIME"}) {
		t.Error("expected AppliesTo to return true for a DATETIME column")
	}
}

func TestISO8601_DetectsSQLiteCanonicalDatetimeFormat(t *testing.T) {
	h := ISO8601{}
	// SQLite's own datetime()/CURRENT_TIMESTAMP produce "YYYY-MM-DD HH:MM:SS"
	// — space-separated, no 'T', no timezone offset. Very common in the
	// wild (e.g. chinook.db) and distinct from RFC3339.
	samples := []profiler.Value{"1962-02-18 00:00:00", "2002-08-14 00:00:00"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "DATETIME"}, samples)
	if !ok {
		t.Fatal("expected a finding for SQLite canonical datetime strings")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
}

func TestISO8601_RecognizesValuesTheDriverAlreadyParsedAsTimeTime(t *testing.T) {
	// modernc.org/sqlite scans DATE/DATETIME/TIMESTAMP-declared columns
	// straight into time.Time (verified against chinook.db's
	// employees.BirthDate), not a string — the heuristic must recognize
	// that value directly rather than requiring a string to parse.
	h := ISO8601{}
	samples := []profiler.Value{
		time.Date(1962, time.February, 18, 0, 0, 0, 0, time.UTC),
		time.Date(1958, time.December, 8, 0, 0, 0, 0, time.UTC),
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "DATETIME"}, samples)
	if !ok {
		t.Fatal("expected a finding for already-parsed time.Time samples")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
}

func TestISO8601_NoOpinionOnNonTimestampText(t *testing.T) {
	h := ISO8601{}
	samples := []profiler.Value{"Austin", "Texas"}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for plain text that isn't a timestamp")
	}
}
