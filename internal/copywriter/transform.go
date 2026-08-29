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

	"github.com/jackc/pgx/v5/pgtype"

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
		// Shares profiler.ParseTimestamp with the iso8601_timestamp
		// heuristic that assigns this transform — using a separate,
		// independently-maintained layout list here previously let the
		// heuristic accept a format (e.g. date-only "1980-12-08") this
		// transform didn't know how to convert, failing at COPY time on a
		// column the profiler had already promised was a timestamp.
		if tm, ok := profiler.ParseTimestamp(s); ok {
			return tm, nil
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

	case "yyyymmdd_to_date":
		s, ok := toYYYYMMDDString(raw)
		if !ok {
			return nil, fmt.Errorf("yyyymmdd_to_date: unexpected type %T", raw)
		}
		tm, err := time.Parse("20060102", s)
		if err != nil {
			return nil, fmt.Errorf("yyyymmdd_to_date: %q: %w", s, err)
		}
		return tm, nil

	case "uuid_format":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("uuid_format: unexpected type %T", raw)
		}
		// pgx's uuid codec only accepts values implementing UUIDValuer
		// (which a plain Go string doesn't) — a raw string reaches
		// PlanEncode as nil and COPY fails with "cannot find encode
		// plan". pgtype.UUID.Scan parses the canonical text form into
		// the [16]byte pgx actually needs.
		var u pgtype.UUID
		if err := u.Scan(s); err != nil {
			return nil, fmt.Errorf("uuid_format: %q: %w", s, err)
		}
		return u, nil

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

// toYYYYMMDDString normalizes v (an int64 from an INTEGER column or a
// string from a TEXT column — both forms seen in real source data) to its
// 8-digit string form, or reports false for any other shape. It does not
// validate the digits form a real calendar date — time.Parse in the
// yyyymmdd_to_date case does that.
func toYYYYMMDDString(v profiler.Value) (string, bool) {
	switch val := v.(type) {
	case int64:
		return strconv.FormatInt(val, 10), true
	case string:
		return val, true
	default:
		return "", false
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
