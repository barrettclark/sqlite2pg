package profiler

import (
	"testing"
	"time"
)

func TestParseDayFirstTimestamp_ParsesDMYDates(t *testing.T) {
	cases := []struct {
		s     string
		day   int
		month time.Month
		year  int
	}{
		{"31/07/2006", 31, time.July, 2006},
		{"2/1/2006", 2, time.January, 2006},
		{"31/07/2006 14:30:00", 31, time.July, 2006},
		{"31/07/2006 2:30:00 PM", 31, time.July, 2006},
	}
	for _, c := range cases {
		tm, ok := ParseDayFirstTimestamp(c.s)
		if !ok {
			t.Fatalf("ParseDayFirstTimestamp(%q) failed to parse", c.s)
		}
		if tm.Day() != c.day || tm.Month() != c.month || tm.Year() != c.year {
			t.Errorf("ParseDayFirstTimestamp(%q) = %v, want day %d month %s year %d", c.s, tm, c.day, c.month, c.year)
		}
	}
}

func TestParseDayFirstTimestamp_RejectsNonDateText(t *testing.T) {
	if _, ok := ParseDayFirstTimestamp("not a date"); ok {
		t.Error("expected ParseDayFirstTimestamp to reject non-date text")
	}
}
