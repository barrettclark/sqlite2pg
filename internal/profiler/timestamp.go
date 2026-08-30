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
	"Jan 2, 2006",         // month-name date, e.g. "Jan 2, 2006"
	"2 January 2006",      // day-first month-name date, e.g. "2 January 2006"
}

// ParseTimestamp attempts every recognized layout and returns the first
// successful parse.
//
// Every target these heuristics assign is timestamptz, never bare
// timestamp — an intentional, now-explicit decision (issue #2), not an
// accident of Go's zero-value default. Some of these layouts carry no
// timezone at all (SQLite's plain datetime() strings, date-only values,
// the US-style and day-first/month-name formats), so there's a real
// choice buried in how they're interpreted: time.Parse treats a
// zone-less layout as UTC, so that's what every naive timestamp in a
// migrated database is assumed to mean. This mirrors pgloader's own
// default of always targeting timestamptz for SQLite/MySQL temporal
// columns, but goes one step further: pgloader passes naive strings
// through as-is and lets Postgres interpret them using the *receiving
// session's* timezone setting at insert time (an implicit, environment-
// dependent behavior several pgloader issues report tripping people up
// on) — this tool pins the interpretation to UTC explicitly in code
// before the value ever reaches Postgres, so the result is the same
// regardless of the destination session's configuration. If a source
// database's naive timestamps are known to represent some other zone,
// that's still a real possibility this can't detect from the data alone
// — SQLite carries no zone information to check against — and would need
// correcting after load, the same way pgloader users already do.
func ParseTimestamp(s string) (time.Time, bool) {
	for _, layout := range timestampLayouts {
		if tm, err := time.Parse(layout, s); err == nil {
			return tm, true
		}
	}
	return time.Time{}, false
}
