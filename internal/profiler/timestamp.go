package profiler

import "time"

// timestampLayouts is the single source of truth for recognizing a
// timestamp string, shared by the iso8601_timestamp heuristic (which
// decides a column should be a timestamp) and the copywriter's
// iso8601_to_timestamptz transform (which actually converts each value at
// COPY time). Keeping one list means a format the heuristic accepts can
// never silently fail to convert later.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05", // SQLite's datetime()/CURRENT_TIMESTAMP canonical format
	"2006-01-02",          // date only
	"1/2/2006 3:04:05 PM", // US-style M/D/YYYY with 12-hour AM/PM (e.g. Access/Excel exports)
}

// ParseTimestamp attempts every recognized layout and returns the first
// successful parse.
func ParseTimestamp(s string) (time.Time, bool) {
	for _, layout := range timestampLayouts {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm, true
		}
	}
	return time.Time{}, false
}
