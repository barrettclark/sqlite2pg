package copywriter

// Audit cycle 3, batch 1 (issues #103, #110, #111, #121): finish the
// type-switch fall-through remediation PR #97/#98 started, and close the
// int64-intermediate / time.Time-range / float32-coverage gaps its sibling
// arms already have.

import (
	"testing"
	"time"
)

// TestTransform_ISO8601ToTimestamptz_TimeTimeInputPassesThrough is issue
// #103's regression: the iso8601_timestamp heuristic treats a time.Time
// sample as an automatic match (modernc.org/sqlite scans DATE/DATETIME/
// TIMESTAMP columns straight into time.Time), so streamed rows for such a
// column reach this transform as time.Time, not string. They must convert,
// not fall through a raw.(string)-only check.
func TestTransform_ISO8601ToTimestamptz_TimeTimeInputPassesThrough(t *testing.T) {
	in := time.Date(2019, 3, 4, 14, 22, 0, 0, time.UTC)
	got, err := Transform("iso8601_to_timestamptz", in)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if !tm.Equal(in) {
		t.Errorf("expected %v, got %v", in, tm)
	}
}

// TestTransform_ISO8601ToTimestamptz_RejectsUnexpectedType is issue #103's
// core: a rare non-string, non-time storage-class row (the iso8601
// heuristic skips these with continue rather than disqualifying) must
// error here so verifyTransformAgainstFullTable can route the column to
// review, not pass through unexamined and fail at COPY.
func TestTransform_ISO8601ToTimestamptz_RejectsUnexpectedType(t *testing.T) {
	for _, raw := range []any{int64(42), []byte("2019-03-04"), 3.5} {
		if got, err := Transform("iso8601_to_timestamptz", raw); err == nil {
			t.Errorf("expected an error for %T input, got %v", raw, got)
		}
	}
}

// TestTransform_DayFirstToTimestamptz_RejectsUnexpectedType is issue
// #103's regression for the day_first_date arm. DayFirstDate disqualifies
// on the first non-string sample, so a non-string only reaches here from
// outside the sample; it must error, not fall through.
func TestTransform_DayFirstToTimestamptz_RejectsUnexpectedType(t *testing.T) {
	for _, raw := range []any{int64(42), []byte("31/07/2006"), 3.5} {
		if got, err := Transform("dayfirst_to_timestamptz", raw); err == nil {
			t.Errorf("expected an error for %T input, got %v", raw, got)
		}
	}
}

// TestTransform_NumericTextToInteger_HandlesAlreadyNumericInput mirrors
// TestTransform_StripCommas_HandlesAlreadyNumericInput: numeric_text
// disqualifies on the first non-string sample, so a non-string only
// reaches here from outside the sample — but when it does, an
// integer-shaped value must convert and a fractional / out-of-range one
// must error, not fall through (issue #103).
func TestTransform_NumericTextToInteger_HandlesAlreadyNumericInput(t *testing.T) {
	got, err := Transform("numeric_text_to_integer", int64(42))
	if err != nil || got != int64(42) {
		t.Fatalf("int64(42): got %v (%T), err %v", got, got, err)
	}
	got, err = Transform("numeric_text_to_integer", int(42))
	if err != nil || got != int64(42) {
		t.Fatalf("int(42): got %v (%T), err %v", got, got, err)
	}
	got, err = Transform("numeric_text_to_integer", float64(42))
	if err != nil || got != int64(42) {
		t.Fatalf("float64(42): got %v (%T), err %v", got, got, err)
	}
	if _, err := Transform("numeric_text_to_integer", float64(42.5)); err == nil {
		t.Error("expected an error for a fractional float64 into an integer-targeted transform")
	}
	if _, err := Transform("numeric_text_to_integer", float64(1e20)); err == nil {
		t.Error("expected an error for a float64 outside int64's range")
	}
	if _, err := Transform("numeric_text_to_integer", []byte("42")); err == nil {
		t.Error("expected an error for a []byte input")
	}
}

// TestTransform_NumericTextToDouble_HandlesAlreadyNumericInput mirrors the
// above for the double-precision arm.
func TestTransform_NumericTextToDouble_HandlesAlreadyNumericInput(t *testing.T) {
	got, err := Transform("numeric_text_to_double", int64(42))
	if err != nil || got != float64(42) {
		t.Fatalf("int64(42): got %v (%T), err %v", got, got, err)
	}
	got, err = Transform("numeric_text_to_double", float64(42.5))
	if err != nil || got != float64(42.5) {
		t.Fatalf("float64(42.5): got %v (%T), err %v", got, got, err)
	}
	if _, err := Transform("numeric_text_to_double", []byte("42")); err == nil {
		t.Error("expected an error for a []byte input")
	}
}

// TestTransform_JulianDayToDate_RejectsIntermediateOverflow is issue
// #110's regression: the existing guard only rejects values outside
// int64's range, but julianDayToDate's own intermediates (p := jdn +
// 68569, then 4*p, 146097*q, ...) overflow int64 well before that, near
// 2^61, silently producing a garbage date rather than an error.
func TestTransform_JulianDayToDate_RejectsIntermediateOverflow(t *testing.T) {
	for _, jd := range []float64{5e18, -5e18, 3e18} {
		if got, err := Transform("julian_day_to_date", jd); err == nil {
			t.Errorf("jd=%v: expected an out-of-range error, got %v", jd, got)
		}
	}
}

// TestTransform_JulianDayToDate_StillAcceptsRealDates guards the clamp
// added for #110 against being too tight — every JDN the JulianDay
// heuristic can suggest (its sample window is [1721425.5, 2816787.5]) must
// still convert.
func TestTransform_JulianDayToDate_StillAcceptsRealDates(t *testing.T) {
	for _, jd := range []float64{1721425.5, 2299161, 2440588, 2816787.5} {
		if _, err := Transform("julian_day_to_date", jd); err != nil {
			t.Errorf("jd=%v: unexpected error %v", jd, err)
		}
	}
}

// TestTransform_UnixEpoch_RejectsWrappingValue is issue #111's regression:
// a value inside int64's range but far outside any plausible timestamp
// still wraps time.Time's own internal seconds-since-year-1 int64 to an
// arbitrary instant with no error, and migrate verify recomputes the same
// wrap on both sides and reports a match. (unix_epoch_micros is excluded:
// the entire int64 µs range maps to within ±292471 years of 1970, all of
// which PostgreSQL's timestamptz can represent, so no µs value can produce
// an implausible-year timestamp.)
func TestTransform_UnixEpoch_RejectsWrappingValue(t *testing.T) {
	for _, transform := range []string{"unix_epoch_seconds", "unix_epoch_millis"} {
		if got, err := Transform(transform, float64(9e18)); err == nil {
			t.Errorf("%s(9e18): expected an out-of-range error, got %v", transform, got)
		}
		if got, err := Transform(transform, int64(9000000000000000000)); err == nil {
			t.Errorf("%s(int64 9e18): expected an out-of-range error, got %v", transform, got)
		}
	}
}

// TestTransform_UnixEpoch_StillAcceptsRealTimestamps guards the #111 clamp
// against being too tight — ordinary present-day and historical epoch
// values in each of the three scales must still convert.
func TestTransform_UnixEpoch_StillAcceptsRealTimestamps(t *testing.T) {
	cases := []struct {
		transform string
		v         int64
	}{
		{"unix_epoch_seconds", 1712345678},
		{"unix_epoch_seconds", -2208988800}, // 1900-01-01
		{"unix_epoch_millis", 1712345678000},
		{"unix_epoch_micros", 1712345678000000},
	}
	for _, c := range cases {
		if _, err := Transform(c.transform, c.v); err != nil {
			t.Errorf("%s(%d): unexpected error %v", c.transform, c.v, err)
		}
	}
}

// TestTransform_UnixEpoch_AcceptsFloat32Input is issue #121's regression:
// toFloat64 accepts a float32 but epochToInt64 (used by the millis/micros
// arms) did not, so unix_epoch_seconds accepted a float32 while its
// siblings rejected it as "unexpected type".
func TestTransform_UnixEpoch_AcceptsFloat32Input(t *testing.T) {
	for _, transform := range []string{"unix_epoch_seconds", "unix_epoch_millis", "unix_epoch_micros"} {
		if _, err := Transform(transform, float32(1000)); err != nil {
			t.Errorf("%s(float32): unexpected error %v", transform, err)
		}
	}
}
