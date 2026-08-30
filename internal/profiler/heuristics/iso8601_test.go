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
	// wild (e.g. chinook.db) and distinct from RFC3339. A real (non-
	// midnight) time-of-day component keeps this a genuine timestamptz
	// case rather than the date-only case covered by issue #14.
	samples := []profiler.Value{"1962-02-18 09:15:00", "2002-08-14 00:00:00"}
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
	// that value directly rather than requiring a string to parse. Both
	// samples are midnight-only (a birth date has no real time-of-day
	// component), so per issue #14 this must resolve to date, not
	// timestamptz.
	h := ISO8601{}
	samples := []profiler.Value{
		time.Date(1962, time.February, 18, 0, 0, 0, 0, time.UTC),
		time.Date(1958, time.December, 8, 0, 0, 0, 0, time.UTC),
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "DATETIME"}, samples)
	if !ok {
		t.Fatal("expected a finding for already-parsed time.Time samples")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
}

func TestISO8601_TimeTimeSamplesWithRealTimeOfDayTargetTimestamptz(t *testing.T) {
	// Contrast case for the above: a time.Time sample that carries a real
	// (non-midnight) time-of-day component must still resolve to
	// timestamptz.
	h := ISO8601{}
	samples := []profiler.Value{
		time.Date(1962, time.February, 18, 13, 45, 0, 0, time.UTC),
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

func TestISO8601_DateOnlyValuesTargetDateNotTimestamptz(t *testing.T) {
	// Issue #14: employee.db's birth_date/hire_date columns hold plain
	// "YYYY-MM-DD" date-only strings with no time-of-day component at all.
	// Targeting timestamptz and assuming UTC midnight silently rolls the
	// calendar date back a day in any non-UTC session. Every sample here
	// has no time component, so this must resolve to date instead.
	h := ISO8601{}
	samples := []profiler.Value{"1953-09-02", "1964-06-02", "1959-12-03"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "DATE"}, samples)
	if !ok {
		t.Fatal("expected a finding for date-only ISO 8601 values")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "iso8601_to_date" {
		t.Errorf("expected transform iso8601_to_date, got %q", finding.TransformExpr)
	}
}

func TestISO8601_MidnightOnlyFullTimestampsTargetDate(t *testing.T) {
	// Issue #14: neh-grants.db's BeginGrant/CouncilDate/EndGrant columns
	// hold full timestamp strings ("4/1/2006 12:00:00 AM") that are
	// functionally date-only — every sampled value's time-of-day is
	// exactly midnight. This must resolve to date, the same as a genuine
	// date-only string, rather than timestamptz.
	h := ISO8601{}
	samples := []profiler.Value{
		"1996-01-02 00:00:00",
		"1996-01-03 00:00:00",
		"1996-01-04 00:00:00",
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "TEXT"}, samples)
	if !ok {
		t.Fatal("expected a finding for midnight-only timestamp strings")
	}
	if finding.SuggestedType != "date" {
		t.Errorf("expected suggested type date, got %q", finding.SuggestedType)
	}
	if finding.TransformExpr != "iso8601_to_date" {
		t.Errorf("expected transform iso8601_to_date, got %q", finding.TransformExpr)
	}
}

func TestISO8601_MixedMidnightAndNonMidnightTargetsTimestamptz(t *testing.T) {
	// A column with even one genuine time-of-day component must still be
	// treated as a real timestamp, not downgraded to date.
	h := ISO8601{}
	samples := []profiler.Value{"1996-01-02 00:00:00", "1996-01-03 14:30:00"}
	finding, ok := h.Evaluate(profiler.ColumnMeta{DeclaredType: "TEXT"}, samples)
	if !ok {
		t.Fatal("expected a finding for timestamp strings")
	}
	if finding.SuggestedType != "timestamptz" {
		t.Errorf("expected suggested type timestamptz, got %q", finding.SuggestedType)
	}
}
