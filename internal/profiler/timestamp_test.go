package profiler

import (
	"testing"
	"time"
)

func TestParseTimestamp_RecognizesEveryFormatTheISO8601HeuristicAccepts(t *testing.T) {
	// This is the single source of truth both the iso8601_timestamp
	// heuristic (deciding a column should be a timestamp) and the
	// copywriter's iso8601_to_timestamptz transform (actually converting
	// each value at COPY time) must use — a previous bug had these two
	// steps maintaining separate, drifted-apart layout lists: the
	// heuristic accepted date-only strings like "1980-12-08"
	// (northwind_small.sqlite's Employee.BirthDate), but the transform's
	// own hardcoded RFC3339-only list didn't, and the load failed on a
	// column the profiler had already promised was a timestamp.
	cases := []string{
		"2026-08-14T18:01:38.401Z", // RFC3339Nano
		"2026-08-14T18:01:38Z",     // RFC3339
		"2026-08-14T18:01:38",      // no zone
		"1962-02-18 00:00:00",      // SQLite canonical datetime
		"1980-12-08",               // date only
		"7/31/2006 12:00:00 AM",    // US-style M/D/YYYY with 12-hour AM/PM
		"11/1/2000 12:00:00 AM",    // same format, single-digit month/day
	}
	for _, s := range cases {
		if _, ok := ParseTimestamp(s); !ok {
			t.Errorf("ParseTimestamp(%q) failed to parse", s)
		}
	}
}

func TestParseTimestamp_RejectsNonTimestampText(t *testing.T) {
	if _, ok := ParseTimestamp("not a date"); ok {
		t.Error("expected ParseTimestamp to reject non-timestamp text")
	}
}

func TestParseTimestamp_ReturnsTheParsedTime(t *testing.T) {
	tm, ok := ParseTimestamp("1980-12-08")
	if !ok {
		t.Fatal("expected a successful parse")
	}
	if tm.Year() != 1980 || tm.Month() != time.December || tm.Day() != 8 {
		t.Errorf("expected 1980-12-08, got %v", tm)
	}
}

func TestParseTimestamp_ParsesMonthNameDates(t *testing.T) {
	cases := []struct {
		s     string
		year  int
		month time.Month
		day   int
	}{
		{"Jan 2, 2006", 2006, time.January, 2},
		{"2 January 2006", 2006, time.January, 2},
	}
	for _, c := range cases {
		tm, ok := ParseTimestamp(c.s)
		if !ok {
			t.Fatalf("ParseTimestamp(%q) failed to parse", c.s)
		}
		if tm.Year() != c.year || tm.Month() != c.month || tm.Day() != c.day {
			t.Errorf("ParseTimestamp(%q) = %v, want %d-%s-%d", c.s, tm, c.year, c.month, c.day)
		}
	}
}

func TestParseTimestamp_ParsesUSStyleAMPMCorrectly(t *testing.T) {
	// neh-grants.db stores CouncilDate/BeginGrant/EndGrant this way —
	// every sampled value at exactly midnight, but the format itself
	// supports any time of day.
	tm, ok := ParseTimestamp("7/31/2006 12:00:00 AM")
	if !ok {
		t.Fatal("expected a successful parse")
	}
	if tm.Year() != 2006 || tm.Month() != time.July || tm.Day() != 31 || tm.Hour() != 0 {
		t.Errorf("expected 2006-07-31 00:00, got %v", tm)
	}
}
