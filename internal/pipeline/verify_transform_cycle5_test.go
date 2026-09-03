package pipeline

import (
	"testing"
	"time"
)

// issue #161 (audit cycle 5 L4): Go's Year() is astronomical (year 0 =
// 1 BC), so Go year -4713 is 4714 BC — of which Postgres can store only
// the last 38 days (its floor is Julian day 0 = 4714-11-24 BC). The
// guard was -4713, accepting essentially all of an unstorable year.
func TestFitsTemporalRange_MinYearBoundary(t *testing.T) {
	yr4713bc := time.Date(-4712, time.June, 1, 0, 0, 0, 0, time.UTC) // Go -4712 = 4713 BC, storable
	yr4714bc := time.Date(-4713, time.June, 1, 0, 0, 0, 0, time.UTC) // Go -4713 = 4714 BC, effectively not

	if !fitsTargetType(yr4713bc, "date") || !fitsTargetType(yr4713bc, "timestamptz") {
		t.Errorf("4713 BC (Go year -4712) should fit both date and timestamptz")
	}
	if fitsTargetType(yr4714bc, "date") || fitsTargetType(yr4714bc, "timestamptz") {
		t.Errorf("4714 BC (Go year -4713) should be rejected by both — Postgres stores only its last 38 days")
	}
}
