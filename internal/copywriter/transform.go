// Package copywriter streams rows from SQLite through per-column
// transforms into Postgres via the COPY protocol, one row at a time —
// never buffering a full table, which is what made the original Python
// pre-processing script unable to scale past ~1M-row tables.
package copywriter

import (
	"encoding/json"
	"fmt"
	"math"
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
		// esri_typename (issue #22 audit) is a genuine pass-through by
		// design, not a gap: EsriTypeName's decision comes entirely from
		// the column's declared type, never from sampled/streamed values
		// (see its doc comment), so there is nothing about a given raw
		// value for this transform to validate.
		return raw, nil

	case "strip_commas":
		// CommaNumber.Evaluate (the paired heuristic) skips non-string
		// samples with `continue` rather than disqualifying the column,
		// so a column can be mostly int64/float64-storage REAL/INTEGER
		// values with only a rare comma-formatted string row — a
		// raw.(string)-only check used to hand a raw float64 straight to
		// an int4 target column unconverted (`return raw, nil`), which
		// full-table verification's fitsTargetType range check has no
		// float64 case for, so the gap was never caught before COPY
		// (issue #86's audit, finding M7). An already-numeric raw value
		// needs no comma-stripping; convert it directly instead of
		// passing it through unexamined.
		switch v := raw.(type) {
		case string:
			n, err := strconv.ParseInt(strings.ReplaceAll(v, ",", ""), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("strip_commas: %q: %w", v, err)
			}
			return n, nil
		case int64:
			return v, nil
		case float64:
			if v != math.Trunc(v) {
				return nil, fmt.Errorf("strip_commas: %v is not a whole number", v)
			}
			// Converting an out-of-int64-range float64 is implementation-
			// dependent per the Go spec, not an error — it would silently
			// produce a garbage int64 for a genuinely huge value instead
			// of failing loudly (Copilot PR #98 finding). -2^63/2^63 are
			// both exactly representable in float64, so this bounds check
			// itself carries no precision loss.
			if v < -9223372036854775808.0 || v >= 9223372036854775808.0 {
				return nil, fmt.Errorf("strip_commas: %v overflows int64", v)
			}
			return int64(v), nil
		default:
			return nil, fmt.Errorf("strip_commas: unexpected type %T", raw)
		}

	case "strip_commas_float":
		// Same reasoning as strip_commas above: an already-numeric raw
		// value (int64 or float64) needs no comma-stripping and fits a
		// double precision target directly.
		switch v := raw.(type) {
		case string:
			f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
			if err != nil {
				return nil, fmt.Errorf("strip_commas_float: %q: %w", v, err)
			}
			return f, nil
		case int64:
			return float64(v), nil
		case float64:
			return v, nil
		default:
			return nil, fmt.Errorf("strip_commas_float: unexpected type %T", raw)
		}

	case "unix_epoch_seconds":
		// toInt64's float64 case truncates toward zero, silently dropping
		// up to a full second of real sub-second precision (e.g.
		// 1712345678.9 -> 1712345678) — undocumented and, at up to 999ms,
		// too large a loss to call incidental (issue #90's audit, finding
		// L4). Split the fractional part into nanoseconds instead of
		// truncating it away; an int64 input (the overwhelmingly common
		// shape — epoch seconds are tiny relative to float64's exact
		// range) converts through with no precision change at all.
		f, ok := toFloat64(raw)
		if !ok {
			return nil, fmt.Errorf("unix_epoch_seconds: unexpected type %T", raw)
		}
		sec := math.Floor(f)
		// int64(sec) is implementation-dependent per the Go spec for NaN,
		// ±Inf, or any value outside int64's range (Copilot PR #98
		// finding, same class as strip_commas' overflow above) — check
		// explicitly rather than relying on it. -2^63/2^63 are both
		// exactly representable in float64, so this bound comparison
		// itself carries no precision loss. (time.Unix's own nsec
		// parameter is documented to normalize a carry past 1e9 from the
		// rounding just below, so that part needs no extra guard.)
		if math.IsNaN(sec) || sec < -9223372036854775808.0 || sec >= 9223372036854775808.0 {
			return nil, fmt.Errorf("unix_epoch_seconds: %v is out of range", f)
		}
		nanos := int64(math.Round((f - sec) * float64(time.Second)))
		return time.Unix(int64(sec), nanos).UTC(), nil

	case "unix_epoch_millis":
		// A fractional millisecond is sub-millisecond precision — small
		// relative to unix_epoch_seconds' up-to-999ms loss above, and
		// still not the deliberate microsecond-resolution cutoff
		// unix_epoch_micros hits below, but truncated here the same way
		// for now rather than as a documented accepted limit.
		ms, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("unix_epoch_millis: unexpected type %T", raw)
		}
		return time.UnixMilli(ms).UTC(), nil

	case "unix_epoch_micros":
		// Any fractional part here is sub-microsecond — below Postgres's
		// own timestamptz storage resolution (microseconds), so
		// truncating it away loses nothing Postgres could have kept
		// anyway; genuinely intended, unlike unix_epoch_seconds above.
		us, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("unix_epoch_micros: unexpected type %T", raw)
		}
		return time.UnixMicro(us).UTC(), nil

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

	case "iso8601_to_date":
		// Shares profiler.ParseTimestamp with the iso8601_timestamp
		// heuristic that assigns this transform when every *sampled*
		// value's time-of-day is midnight (issue #14) — same reasoning as
		// iso8601_to_timestamptz: a format the heuristic accepts must
		// always be one this transform can convert.
		//
		// The heuristic's "midnight" judgment is made from the sample
		// alone, so a rare non-midnight value can exist elsewhere in the
		// full table (issue #42). Silently discarding that time-of-day
		// component — as this used to do — made the transform unable to
		// ever fail, which turned issue #13's full-table verification
		// into a silent no-op for this transform, exactly as issue #22
		// found for text_to_jsonb. So a non-midnight value is rejected
		// here rather than truncated: verifyTransformAgainstFullTable
		// (internal/pipeline/verify_transform.go) runs this exact
		// function against every row, and this error is what lets it
		// catch the case and route the column to review instead of
		// silently discarding real data at load.
		//
		// The heuristic that assigns this transform (iso8601_timestamp)
		// fires on both a string sample AND a time.Time sample — the
		// modernc.org/sqlite driver scans a DATE/DATETIME/TIMESTAMP-
		// declared column straight into time.Time rather than a string
		// (see internal/pipeline/profile.go), so streamed rows for such a
		// column arrive here as time.Time, not string. A raw.(string)-only
		// check used to let every such row bypass the non-midnight guard
		// entirely (issue #79's audit, finding H3) — the same
		// "type-switch fall-through" shape as M6/M7 below.
		switch v := raw.(type) {
		case string:
			tm, ok := profiler.ParseTimestamp(v)
			if !ok {
				return nil, fmt.Errorf("iso8601_to_date: cannot parse %q", v)
			}
			if tm.Hour() != 0 || tm.Minute() != 0 || tm.Second() != 0 || tm.Nanosecond() != 0 {
				return nil, fmt.Errorf("iso8601_to_date: %q has a non-midnight time-of-day component", v)
			}
			return time.Date(tm.Year(), tm.Month(), tm.Day(), 0, 0, 0, 0, time.UTC), nil
		case time.Time:
			if v.Hour() != 0 || v.Minute() != 0 || v.Second() != 0 || v.Nanosecond() != 0 {
				return nil, fmt.Errorf("iso8601_to_date: %s has a non-midnight time-of-day component", v.Format(time.RFC3339Nano))
			}
			return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, time.UTC), nil
		default:
			return nil, fmt.Errorf("iso8601_to_date: unexpected type %T", raw)
		}

	case "int_to_bool":
		// Also accepts a "0"/"1" string (not routed through toInt64,
		// which would silently accept other numeric-looking strings too):
		// boolean01 assigns this same transform to TEXT/CHAR-affinity
		// 0/1 flag columns (e.g. sakila.db's customer.active, CHAR(1)
		// storing '0'/'1') alongside its original INTEGER-affinity case,
		// since the underlying judgment is identical and only the
		// storage representation differs.
		if s, ok := raw.(string); ok {
			switch s {
			case "0":
				return false, nil
			case "1":
				return true, nil
			default:
				return nil, fmt.Errorf("int_to_bool: unexpected string %q", s)
			}
		}
		n, ok := toInt64(raw)
		if !ok {
			return nil, fmt.Errorf("int_to_bool: unexpected type %T", raw)
		}
		return n != 0, nil

	case "text_to_jsonb":
		// Issue #22: this used to be a bare `return raw, nil` that could
		// never fail, which made full-table verification (issue #13) a
		// silent no-op for geojson_text columns — a rare non-JSON value
		// (e.g. "N/A") outside the sample would "pass" the full-table
		// check and only surface once COPY itself rejected it with
		// "invalid input syntax for type json". Validating the JSON here
		// keeps the "verify by running the real transform" model intact
		// instead of duplicating this check in the verifier.
		//
		// GeoJSON.Evaluate (the paired heuristic) skips non-string samples
		// with `continue` rather than disqualifying the column, so a
		// column can be mostly GeoJSON text with a rare BLOB row (SQLite's
		// dynamic typing permits it — the same shape issue #83 found). A
		// raw.(string)-only check reintroduced the exact "can never fail"
		// gap this transform was fixed for issue #22 to avoid, just for
		// []byte instead of string (issue #86's audit, finding M7):
		// json.Valid accepts a []byte directly, same rules either way.
		var b []byte
		switch v := raw.(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		default:
			return nil, fmt.Errorf("text_to_jsonb: unexpected type %T", raw)
		}
		if !json.Valid(b) {
			return nil, fmt.Errorf("text_to_jsonb: %q is not valid JSON", b)
		}
		return raw, nil

	case "julian_day_to_date":
		f, ok := toFloat64(raw)
		if !ok {
			return nil, fmt.Errorf("julian_day_to_date: unexpected type %T", raw)
		}
		// Round to the nearest whole Julian Day Number rather than
		// truncating the fraction (issue #24). Astronomical Julian Day is
		// noon-based (JD N.0 is noon UT on the calendar day JDN N
		// represents), so a fractional JD in the midnight-to-noon half of
		// the range belongs to the FOLLOWING day's JDN; math.Floor(f + 0.5)
		// is the standard JD-to-JDN conversion that accounts for that,
		// where int64(f) truncation always floored to the earlier day.
		return julianDayToDate(int64(math.Floor(f + 0.5))), nil

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

	case "numeric_text_to_integer":
		s, ok := raw.(string)
		if !ok {
			return raw, nil
		}
		if s == "" {
			// Matches the numeric_text heuristic's own leniency: an empty
			// string is "no value on file," the same convention seen
			// elsewhere (e.g. uuid_format), not a disqualifying non-number.
			return nil, nil
		}
		n, err := parseWholeNumberText(s)
		if err != nil {
			return nil, fmt.Errorf("numeric_text_to_integer: %q: %w", s, err)
		}
		return n, nil

	case "numeric_text_to_double":
		s, ok := raw.(string)
		if !ok {
			return raw, nil
		}
		if s == "" {
			return nil, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("numeric_text_to_double: %q: %w", s, err)
		}
		return f, nil

	case "excel_serial_to_timestamptz":
		f, ok := toFloat64(raw)
		if !ok {
			return nil, fmt.Errorf("excel_serial_to_timestamptz: unexpected type %T", raw)
		}
		return excelSerialToTime(f), nil

	case "dayfirst_to_timestamptz":
		s, ok := raw.(string)
		if !ok {
			return raw, nil
		}
		// Shares profiler.ParseDayFirstTimestamp with the day_first_date
		// heuristic that assigns this transform, for the same reason
		// iso8601_to_timestamptz shares ParseTimestamp with its heuristic:
		// a format the heuristic accepts must always be one this transform
		// can actually convert.
		if tm, ok := profiler.ParseDayFirstTimestamp(s); ok {
			return tm, nil
		}
		return nil, fmt.Errorf("dayfirst_to_timestamptz: cannot parse %q", s)

	case "uuid_format":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("uuid_format: unexpected type %T", raw)
		}
		if s == "" {
			// The uuid_format heuristic itself treats an empty string as
			// skippable — like NULL, not a disqualifying non-UUID value
			// (see its Evaluate) — since that's how beets (and likely
			// other SQLite-backed ORMs) represents "no ID assigned" for
			// an optional text column instead of using NULL. A column
			// can pass the heuristic on a sample that happened to miss
			// every empty-string row (real example: beets' albums.
			// mb_albumid, 13 empty out of 13,629, easily missed by a
			// 500-row sample) and only hit one during the full-table
			// COPY, so the transform has to honor the same leniency or
			// this fails at load time on a column the profiler already
			// promised was a uuid.
			return nil, nil
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

	case "uuid_list_format":
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("uuid_list_format: unexpected type %T", raw)
		}
		if s == "" {
			// Same "no value" convention as uuid_format (see its case
			// above): an empty string means "no value", not a
			// disqualifying non-UUID.
			return nil, nil
		}
		// pgx's array codec finds a Go slice's element type by
		// reflection and wraps each element with that element type's
		// own codec (UUIDCodec here) — a []pgtype.UUID therefore
		// encodes directly into a uuid[] column via ArrayCodec, the
		// same way a single pgtype.UUID encodes into uuid.
		//
		// heuristics.escapedNulSeparator (mirrored here, since this
		// package can't import the heuristics package) is the literal
		// "\␀" (backslash + U+2400) form the real beets_library.db
		// evidence for this transform actually stores instead of a raw
		// NUL byte — see that constant's doc comment for the full
		// story. Normalizing it to 0x00 first means a hypothetical
		// column that genuinely NUL-joins with a raw byte still works
		// unchanged.
		s = strings.ReplaceAll(s, "\\␀", "\x00")
		parts := strings.Split(s, "\x00")
		list := make([]pgtype.UUID, len(parts))
		for i, p := range parts {
			var u pgtype.UUID
			if err := u.Scan(p); err != nil {
				return nil, fmt.Errorf("uuid_list_format: part %d (%q): %w", i, p, err)
			}
			list[i] = u
		}
		return list, nil

	case "nullif_sentinels":
		// SentinelNull.Evaluate (the paired heuristic) skips a sample
		// value it doesn't recognize (not int64/float64/string) with
		// `continue` rather than disqualifying the column, so a rare
		// non-string, non-numeric value (e.g. a BLOB — SQLite's dynamic
		// typing permits it) can still reach here with this transform
		// assigned. A raw.(string)-only check let such a value pass
		// through unexamined (Copilot PR #98 finding, same class as
		// issue #86/M7): full-table verification wouldn't flag it, and
		// COPY would only fail once pgx tried and failed to encode it.
		switch v := raw.(type) {
		case string:
			if sentinelTokens[strings.ToLower(v)] {
				return nil, nil
			}
			cleaned := strings.ReplaceAll(v, ",", "")
			if n, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
				return n, nil
			}
			// SentinelNull suggests "double precision" whenever a sampled
			// value has a decimal component (commaNumberPattern/
			// plainNumberPattern with a "."), so a value like "1,234.56"
			// is a real, expected input here — ParseInt alone rejects it,
			// and `return raw, nil` used to hand the resulting Go string
			// straight to pgx's float8 codec, which can't binary-encode
			// it (issue #85's audit, finding M6). Try float64 before
			// falling back.
			if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
				return f, nil
			}
			return nil, fmt.Errorf("nullif_sentinels: %q is not a recognized sentinel token and not numeric", v)
		case int64:
			return v, nil
		case float64:
			return v, nil
		default:
			return nil, fmt.Errorf("nullif_sentinels: unexpected type %T", raw)
		}

	case "nullif_empty":
		// No heuristic currently assigns this transform (issue #22
		// audit); it's dead code, not a validation gap in the sense the
		// audit was looking for. Left as a true pass-through, matching
		// its name: it only ever nulls out an empty string.
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

// FitsRange reports whether n fits the Postgres integer type targetType
// names ("smallint"/int2, "integer"/int4, "bigint"/int8) — any int64
// always fits "bigint" since that's exactly int64's own range. Every other
// targetType reports true: this is only meaningful for the three integer
// types, and callers that don't yet know a value is integer-shaped (or are
// checking a non-integer target) have nothing for this to say. Shared by
// the TUI type picker (internal/tui/logic.go, issue #27 — offering
// "smallint" for a value outside int2's range let the picker promise a
// type the real COPY would then reject) and by
// verifyTransformAgainstFullTable's fitsTargetType (internal/pipeline/
// verify_transform.go, issue #15's originally int4-only range check).
func FitsRange(n int64, targetType string) bool {
	switch targetType {
	case "smallint":
		return n >= math.MinInt16 && n <= math.MaxInt16
	case "integer":
		return n >= math.MinInt32 && n <= math.MaxInt32
	case "bigint":
		return true
	default:
		return true
	}
}

// parseWholeNumberText parses s as an exact int64 without ever routing
// through float64 — issue #15: strconv.ParseFloat(s, 64) followed by an
// int64 cast silently rounds to the nearest representable float64 once s
// exceeds float64's ~15-17 significant digits (a real fixture,
// bikes.db.legacy_id's 19-digit IDs, was corrupted by dozens of units this
// way on an otherwise "successful" load), and saturates to
// math.MaxInt64/MinInt64 with no error once the value is large enough to
// overflow int64 entirely after that rounding.
//
// The numeric_text heuristic accepts whole numbers spelled with a
// trailing ".0" (e.g. "1998.0") as well as plain digit strings — see its
// sawFraction check — so a value containing a decimal point is only valid
// here when everything after the point is zeros; that suffix is trimmed
// before the exact integer parse, never fed through ParseFloat.
func parseWholeNumberText(s string) (int64, error) {
	intPart := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		for _, c := range s[i+1:] {
			if c != '0' {
				return 0, fmt.Errorf("has a non-zero fractional part")
			}
		}
		intPart = s[:i]
	}
	return strconv.ParseInt(intPart, 10, 64)
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

// excelEpoch is day zero of the Excel/Access serial-date system: 1899-12-30
// (not 1900-01-01) to account for Lotus 1-2-3's fictitious 1900-02-29,
// which Excel deliberately preserved for backward compatibility.
var excelEpoch = time.Date(1899, time.December, 30, 0, 0, 0, 0, time.UTC)

// excelSerialToTime converts an Excel/Access serial date number to a
// time.Time, preserving any fractional part as a time-of-day offset (e.g.
// 44197.5 is noon on the day 44197 alone represents).
//
// The whole-day component goes through AddDate, not
// time.Duration(days)*24*time.Hour: ExcelSerialDate's heuristic
// (profiler/heuristics/excel_serial_date.go) only requires 50% of sampled
// values to land in the plausible Excel-serial window — it deliberately
// tolerates a minority outside it — and the transform then runs on every
// row. A genuinely out-of-range serial (e.g. an epoch-seconds value like
// 1.7e9 sitting in an otherwise Excel-serial column) turns into a days
// count whose Duration form overflows int64 nanoseconds (~292-year range)
// and silently wraps to an arbitrary, plausible-*looking* wrong date
// (issue #82's audit, finding M3) — the exact kind of error nothing
// downstream catches, since full-table verification recomputes the same
// wrapped value on both sides. AddDate does plain calendar arithmetic with
// no Duration-sized intermediate, so an out-of-range serial instead
// produces a wildly implausible far-future/past date that Postgres's own
// timestamp range check (~4713 BC to 294276 AD) will reject at COPY time —
// a loud, catchable failure instead of silent corruption.
func excelSerialToTime(serial float64) time.Time {
	days := math.Trunc(serial)
	fracSeconds := (serial - days) * 24 * 60 * 60
	return excelEpoch.
		AddDate(0, 0, clampDaysToInt(days)).
		Add(time.Duration(fracSeconds * float64(time.Second)))
}

// maxPlausibleExcelDays bounds clampDaysToInt: obscenely larger than any
// real calendar date (±2.7 billion years from the Excel epoch) yet safely
// within time.Time.AddDate's own working range — confirmed empirically
// that AddDate itself silently wraps/overflows for a days argument much
// larger than this (1e15 days flips the resulting year's sign) — and
// within float64's exact-integer range (2^53), so this bound comparison
// itself carries no precision loss.
const maxPlausibleExcelDays = 1e12

// clampDaysToInt converts days (already an integer-valued float64, per
// excelSerialToTime's math.Trunc) to an int for AddDate, clamping to
// ±maxPlausibleExcelDays instead of relying on Go's implementation-
// dependent behavior for a float64 conversion outside int's range (or,
// past AddDate's own safe range, wrapping to a plausible-looking wrong
// date — the same silent-corruption class this function's AddDate switch
// was already fixed to avoid). NaN clamps to 0 (the epoch itself) since it
// has no defined sign to clamp toward.
func clampDaysToInt(days float64) int {
	switch {
	case math.IsNaN(days):
		return 0
	case days <= -maxPlausibleExcelDays:
		return -maxPlausibleExcelDays
	case days >= maxPlausibleExcelDays:
		return maxPlausibleExcelDays
	default:
		return int(days)
	}
}

// julianDayToDate converts an astronomical Julian Day Number to a Gregorian
// calendar date using the Fliegel & Van Flandern (1968) algorithm — the
// same integer-JDN-to-date conversion Postgres's own date_in() Julian date
// parsing is equivalent to. jdn is treated as noon-based per convention.
func julianDayToDate(jdn int64) time.Time {
	// Fliegel & Van Flandern's algorithm requires floor division
	// throughout; Go's / truncates toward zero, which only agrees with
	// floor division when both operands are non-negative or the division
	// is exact. p = jdn+68569 goes negative for any jdn < -68569 — a
	// negative-but-plausible date (year < -4900ish), not merely a
	// theoretical edge case — and Go's truncating / then produces a wildly
	// wrong result (confirmed against an independent day-count-to-
	// civil-date algorithm: jdn=-70000 truncated to year -4903, month -7,
	// day -30; floor division correctly gives -4904-03-30). floorDiv
	// throughout keeps every intermediate exact for the full int64 range
	// (issue #89's audit, finding L3).
	p := jdn + 68569
	q := floorDiv(4*p, 146097)
	r := p - floorDiv(146097*q+3, 4)
	s := floorDiv(4000*(r+1), 1461001)
	r = r - floorDiv(1461*s, 4) + 31
	tt := floorDiv(80*r, 2447)
	day := r - floorDiv(2447*tt, 80)
	u := floorDiv(tt, 11)
	month := tt + 2 - 12*u
	year := 100*(q-49) + s + u
	return time.Date(int(year), time.Month(month), int(day), 0, 0, 0, 0, time.UTC)
}

// floorDiv returns floor(a/b) for b > 0 — unlike Go's built-in /, which
// truncates toward zero and so disagrees with floor division whenever a is
// negative and the division isn't exact.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
