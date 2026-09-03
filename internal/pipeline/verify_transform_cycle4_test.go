package pipeline

import (
	"testing"
	"time"
)

// issue #140 (audit cycle 4 M2): fitsTargetType range-checks the integer
// targets but had nothing to say about a time.Time, so a temporal
// transform (julian_day_to_date, excel_serial_to_timestamptz, an epoch
// arm on a stray huge value) that produces a year outside PostgreSQL's
// storable range verified clean and auto-approved — then `load` aborted
// mid-COPY on a column verify had just certified. Range-check the
// time.Time so the column is routed to review instead.
func TestFitsTargetType_TimeTimeAgainstPostgresRange(t *testing.T) {
	inRange := time.Date(3000, time.January, 1, 0, 0, 0, 0, time.UTC)
	farFuture := time.Date(2_733_194, time.November, 27, 12, 0, 0, 0, time.UTC)
	farPast := time.Date(-5000, time.January, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		val        any
		targetType string
		want       bool
	}{
		{"ordinary date fits date", inRange, "date", true},
		{"ordinary date fits timestamptz", inRange, "timestamptz", true},
		{"year 2.7M exceeds timestamptz max", farFuture, "timestamptz", false},
		{"year 2.7M fits date (below 5.87M)", farFuture, "date", true},
		{"year 8M exceeds date max", time.Date(8_000_000, time.January, 1, 0, 0, 0, 0, time.UTC), "date", false},
		{"year -5000 below both mins", farPast, "date", false},
		{"year -5000 below timestamptz min", farPast, "timestamptz", false},
		{"non-temporal target has no opinion", farFuture, "text", true},
	}
	for _, c := range cases {
		if got := fitsTargetType(c.val, c.targetType); got != c.want {
			t.Errorf("%s: fitsTargetType(%v, %q) = %v, want %v", c.name, c.val, c.targetType, got, c.want)
		}
	}
}
