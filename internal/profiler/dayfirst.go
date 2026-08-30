package profiler

import "time"

// dayFirstLayouts are the day-first (D/M/YYYY) counterparts of the
// ambiguous US-style M/D/YYYY layouts in timestamp.go — shared between the
// day_first_date heuristic (which decides a column is day-first, requiring
// proof via a value no US-style reading could produce) and the
// dayfirst_to_timestamptz transform (which converts every value the same
// way once the heuristic has already committed to this reading).
var dayFirstLayouts = []string{
	"2/1/2006 3:04:05 PM",
	"2/1/2006 15:04:05",
	"2/1/2006",
}

// ParseDayFirstTimestamp attempts every day-first layout and returns the
// first successful parse. Callers needing to distinguish a genuinely
// day-first value from one that merely also happens to be valid read
// month-first should check the returned time's Day() against 12 — see
// DayFirstDate.Evaluate.
func ParseDayFirstTimestamp(s string) (time.Time, bool) {
	for _, layout := range dayFirstLayouts {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm, true
		}
	}
	return time.Time{}, false
}
