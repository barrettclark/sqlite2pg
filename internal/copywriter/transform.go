// Package copywriter streams rows from SQLite through per-column
// transforms into Postgres via the COPY protocol, one row at a time —
// never buffering a full table, which is what made the original Python
// pre-processing script unable to scale past ~1M-row tables.
package copywriter

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"sqlite2pg/internal/profiler"
)

// sentinelTokens mirrors profiler/heuristics' sentinel_null token set; kept
// separate to avoid an import of an internal heuristics package here.
var sentinelTokens = map[string]bool{
	"unknown": true,
	"n/a":     true,
	"na":      true,
	"none":    true,
	"null":    true,
	"missing": true,
	"-":       true,
}

// Transform converts a raw sampled/streamed SQLite value into the value
// pgx should write for the target Postgres column, per the transform name
// a profiler.Finding attached to that column's decision. NULLs pass through
// unchanged regardless of transform.
func Transform(transform string, raw profiler.Value) (any, error) {
	if raw == nil {
		return nil, nil
	}

	switch transform {
	case "", "esri_typename":
		return raw, nil

	case "strip_commas":
		s, ok := raw.(string)
		if !ok {
			return raw, nil
		}
		n, err := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("strip_commas: %q: %w", s, err)
		}
		return n, nil

	case "unix_epoch_seconds":
		sec, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("unix_epoch_seconds: unexpected type %T", raw)
		}
		return time.Unix(sec, 0).UTC(), nil

	case "iso8601_to_timestamptz":
		s, ok := raw.(string)
		if !ok {
			return raw, nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if tm, err := time.Parse(layout, s); err == nil {
				return tm, nil
			}
		}
		return nil, fmt.Errorf("iso8601_to_timestamptz: cannot parse %q", s)

	case "int_to_bool":
		n, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("int_to_bool: unexpected type %T", raw)
		}
		return n != 0, nil

	case "text_to_jsonb":
		return raw, nil

	case "julian_day_to_date":
		f, ok := toFloat64(raw)
		if !ok {
			return nil, fmt.Errorf("julian_day_to_date: unexpected type %T", raw)
		}
		return julianDayToDate(int64(f)), nil

	case "nullif_sentinels":
		if s, ok := raw.(string); ok {
			if sentinelTokens[strings.ToLower(s)] {
				return nil, nil
			}
			cleaned := strings.ReplaceAll(s, ",", "")
			if n, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
				return n, nil
			}
			return raw, nil
		}
		return raw, nil

	case "nullif_empty":
		if s, ok := raw.(string); ok && s == "" {
			return nil, nil
		}
		return raw, nil

	case "drop_column":
		return nil, fmt.Errorf("drop_column: this column should have been excluded before reaching the COPY pipeline")

	default:
		return nil, fmt.Errorf("unknown transform %q", transform)
	}
}

func toInt64(v profiler.Value) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

func toFloat64(v profiler.Value) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// julianDayToDate converts an astronomical Julian Day Number to a Gregorian
// calendar date using the Fliegel & Van Flandern (1968) algorithm — the
// same integer-JDN-to-date conversion Postgres's own date_in() Julian date
// parsing is equivalent to. jdn is treated as noon-based per convention.
func julianDayToDate(jdn int64) time.Time {
	p := jdn + 68569
	q := 4 * p / 146097
	r := p - (146097*q+3)/4
	s := 4000 * (r + 1) / 1461001
	r = r - 1461*s/4 + 31
	tt := 80 * r / 2447
	day := r - 2447*tt/80
	u := tt / 11
	month := tt + 2 - 12*u
	year := 100*(q-49) + s + u
	return time.Date(int(year), time.Month(month), int(day), 0, 0, 0, 0, time.UTC)
}
