package copywriter

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"sqlite2pg/internal/profiler"
)

func TestTransform_Passthrough_WhenNoTransformNamed(t *testing.T) {
	got, err := Transform("", int64(42))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != int64(42) {
		t.Errorf("expected passthrough value 42, got %v", got)
	}
}

func TestTransform_TextToJsonb_ValidGeoJSONPassesThrough(t *testing.T) {
	got, err := Transform("text_to_jsonb", `{"type":"Point","coordinates":[1,2]}`)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != `{"type":"Point","coordinates":[1,2]}` {
		t.Errorf("expected the raw JSON text passed through unchanged, got %v", got)
	}
}

func TestTransform_TextToJsonb_RejectsInvalidJSON(t *testing.T) {
	// Issue #22: text_to_jsonb used to be a bare pass-through
	// (`return raw, nil`) that could never fail, which made full-table
	// verification (issue #13) a no-op for geojson_text columns — a value
	// like "N/A" outside the sample would "pass" the full-table check and
	// then blow up COPY with "invalid input syntax for type json".
	_, err := Transform("text_to_jsonb", "N/A")
	if err == nil {
		t.Fatal("expected an error for a value that isn't valid JSON")
	}
}

func TestTransform_StripCommas(t *testing.T) {
	got, err := Transform("strip_commas", "2,949")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != int64(2949) {
		t.Errorf("expected 2949, got %v (%T)", got, got)
	}
}

func TestTransform_StripCommasFloat(t *testing.T) {
	got, err := Transform("strip_commas_float", "1,234.56")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != float64(1234.56) {
		t.Errorf("expected 1234.56, got %v (%T)", got, got)
	}
}

func TestTransform_StripCommasFloat_RejectsUnparsable(t *testing.T) {
	_, err := Transform("strip_commas_float", "1,234.56.78")
	if err == nil {
		t.Fatal("expected an error for a value that isn't a parsable float")
	}
}

func TestTransform_StripCommas_ErrorsOnDecimal(t *testing.T) {
	// Issue #23: strip_commas is only ever meant to run against
	// comma-formatted whole numbers now that comma_number targets
	// strip_commas_float for anything with a fractional part — this pins
	// down that strip_commas itself still can't parse a decimal, so a
	// misrouted value fails loudly rather than silently truncating.
	_, err := Transform("strip_commas", "1,234.56")
	if err == nil {
		t.Fatal("expected an error: strip_commas can't parse a decimal point")
	}
}

func TestTransform_UnixEpochSeconds(t *testing.T) {
	got, err := Transform("unix_epoch_seconds", int64(1620000000))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Unix() != 1620000000 {
		t.Errorf("expected unix time 1620000000, got %d", tm.Unix())
	}
}

func TestTransform_UnixEpochMillis(t *testing.T) {
	got, err := Transform("unix_epoch_millis", int64(1735689600000))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2025 || tm.Month() != time.January || tm.Day() != 1 {
		t.Errorf("expected 2025-01-01, got %v", tm)
	}
}

func TestTransform_UnixEpochMicros(t *testing.T) {
	got, err := Transform("unix_epoch_micros", int64(1735689600000000))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2025 || tm.Month() != time.January || tm.Day() != 1 {
		t.Errorf("expected 2025-01-01, got %v", tm)
	}
}

func TestTransform_NumericTextToInteger(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{"24", 24},
		{"1998.0", 1998},
		{"-5", -5},
	}
	for _, c := range cases {
		got, err := Transform("numeric_text_to_integer", c.raw)
		if err != nil {
			t.Fatalf("Transform(%q): %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("Transform(%q) = %v, want %d", c.raw, got, c.want)
		}
	}
}

func TestTransform_NumericTextToIntegerPreservesExactPrecisionBeyondFloat64(t *testing.T) {
	// Issue #15: parsing via strconv.ParseFloat(s, 64) and casting to int64
	// silently rounds to the nearest representable float64 once s exceeds
	// float64's ~15-17 significant digits. This must go through
	// strconv.ParseInt directly (or an equivalent exact path) so a 19-digit
	// value like a real bikes.db legacy_id round-trips exactly.
	cases := []struct {
		raw  string
		want int64
	}{
		{"2124037125711300644", 2124037125711300644},
		{"1795146692060860976", 1795146692060860976},
		{"9007199254740993", 9007199254740993},
	}
	for _, c := range cases {
		got, err := Transform("numeric_text_to_integer", c.raw)
		if err != nil {
			t.Fatalf("Transform(%q): %v", c.raw, err)
		}
		if got != c.want {
			t.Errorf("Transform(%q) = %v, want %d (precision lost)", c.raw, got, c.want)
		}
	}
}

func TestTransform_NumericTextToIntegerRejectsValuesTooLargeForInt64(t *testing.T) {
	// Must fail loudly (not silently saturate to math.MaxInt64 via a
	// float64 round-trip) once a value genuinely can't fit in an int64.
	if _, err := Transform("numeric_text_to_integer", "12345678901234567890"); err == nil {
		t.Fatal("expected an error for a value beyond int64 range, not silent saturation")
	}
}

func TestTransform_NumericTextToIntegerTreatsEmptyStringAsNull(t *testing.T) {
	got, err := Transform("numeric_text_to_integer", "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for an empty string, got %v", got)
	}
}

func TestTransform_NumericTextToIntegerRejectsUnparseableValues(t *testing.T) {
	if _, err := Transform("numeric_text_to_integer", "not-a-number"); err == nil {
		t.Fatal("expected an error for an unparseable value")
	}
}

func TestTransform_NumericTextToDouble(t *testing.T) {
	got, err := Transform("numeric_text_to_double", "18.5")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != float64(18.5) {
		t.Errorf("expected 18.5, got %v", got)
	}
}

func TestTransform_NumericTextToDoubleTreatsEmptyStringAsNull(t *testing.T) {
	got, err := Transform("numeric_text_to_double", "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for an empty string, got %v", got)
	}
}

func TestTransform_ISO8601ToTimestamptz(t *testing.T) {
	got, err := Transform("iso8601_to_timestamptz", "2026-08-14T18:01:38.401Z")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2026 || tm.Month() != time.August || tm.Day() != 14 {
		t.Errorf("expected 2026-08-14, got %v", tm)
	}
}

func TestTransform_ISO8601ToDate(t *testing.T) {
	cases := []string{"1953-09-02", "1996-01-02 00:00:00"}
	for _, raw := range cases {
		got, err := Transform("iso8601_to_date", raw)
		if err != nil {
			t.Fatalf("Transform(%q): %v", raw, err)
		}
		tm, ok := got.(time.Time)
		if !ok {
			t.Fatalf("expected time.Time, got %T", got)
		}
		if tm.Hour() != 0 || tm.Minute() != 0 || tm.Second() != 0 || tm.Nanosecond() != 0 {
			t.Errorf("expected midnight time-of-day, got %v", tm)
		}
	}
}

func TestTransform_ISO8601ToDateRejectsUnparseableValues(t *testing.T) {
	if _, err := Transform("iso8601_to_date", "not a date"); err == nil {
		t.Error("expected an error for an unparseable string")
	}
}

func TestTransform_ISO8601ToDateRejectsNonMidnightValues(t *testing.T) {
	// Issue #42: iso8601_to_date used to silently discard the
	// time-of-day component instead of erroring on it, making the
	// transform unable to ever fail — which turned issue #13's
	// full-table verification into a silent no-op for this transform,
	// exactly like #22 found for text_to_jsonb. The heuristic that
	// assigns this transform (iso8601_timestamp, issue #14) only ever
	// does so when every *sampled* value's time-of-day is midnight; a
	// genuine non-midnight value outside the sample must now be
	// rejected rather than truncated.
	if _, err := Transform("iso8601_to_date", "1996-01-04 14:37:00"); err == nil {
		t.Error("expected an error for a non-midnight timestamp, not a silent truncation")
	}
}

func TestTransform_UUIDFormat(t *testing.T) {
	got, err := Transform("uuid_format", "90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	u, ok := got.(pgtype.UUID)
	if !ok {
		t.Fatalf("expected pgtype.UUID, got %T", got)
	}
	if !u.Valid {
		t.Fatal("expected a valid UUID")
	}
	if got, want := u.String(), "90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10"; got != want {
		t.Errorf("expected round-trip %q, got %q", want, got)
	}
}

func TestTransform_UUIDFormatRejectsUnparseableValues(t *testing.T) {
	if _, err := Transform("uuid_format", "not-a-uuid"); err == nil {
		t.Fatal("expected an error for an unparseable UUID")
	}
}

func TestTransform_UUIDFormatTreatsEmptyStringAsNull(t *testing.T) {
	// Regression test for a real bug: beets' albums.mb_albumid is a
	// canonical UUID in 13,616 of 13,629 rows and an empty string (not
	// NULL) in the other 13 — "no ID assigned" for an optional field,
	// the same shape the uuid_format heuristic's Evaluate already treats
	// as skippable. A 500-row sample easily misses all 13, so the
	// heuristic can confidently assign uuid_format to a column the
	// transform then has to actually handle at COPY time, not just at
	// sampling time — this used to error with "cannot parse UUID ''".
	got, err := Transform("uuid_format", "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil (SQL NULL) for an empty string, got %v", got)
	}
}

func TestTransform_UUIDListFormatSingleUUIDRoundTrips(t *testing.T) {
	got, err := Transform("uuid_list_format", "90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	list, ok := got.([]pgtype.UUID)
	if !ok {
		t.Fatalf("expected []pgtype.UUID, got %T", got)
	}
	if len(list) != 1 {
		t.Fatalf("expected a 1-element list, got %d elements", len(list))
	}
	if !list[0].Valid {
		t.Fatal("expected a valid UUID")
	}
	if got, want := list[0].String(), "90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10"; got != want {
		t.Errorf("expected round-trip %q, got %q", want, got)
	}
}

func TestTransform_UUIDListFormatThreeUUIDsRoundTrip(t *testing.T) {
	raw := "cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022\x0090b141b9-c39f-4a26-8f5d-9d3c1e2a7b10"
	got, err := Transform("uuid_list_format", raw)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	list, ok := got.([]pgtype.UUID)
	if !ok {
		t.Fatalf("expected []pgtype.UUID, got %T", got)
	}
	want := []string{
		"cc75b164-273c-4dce-9cdf-292045a0d38b",
		"3422ac1a-8dbb-4f23-a337-0bd0a0150022",
		"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10",
	}
	if len(list) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(list))
	}
	for i, w := range want {
		if got := list[i].String(); got != w {
			t.Errorf("element %d: expected %q, got %q", i, w, got)
		}
	}
}

func TestTransform_UUIDListFormatHandlesRealBeetsEscapedSeparator(t *testing.T) {
	// Real beets_library.db evidence: the actual separator on disk is the
	// literal "\␀" (backslash + U+2400) escape, not a raw NUL byte — see
	// heuristics.escapedNulSeparator's doc comment.
	raw := "7113aab7-628f-4050-ae49-dbecac110ca8\\␀a5d79c54-81c3-4a73-af6a-ad5c143d3f21"
	got, err := Transform("uuid_list_format", raw)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	list, ok := got.([]pgtype.UUID)
	if !ok {
		t.Fatalf("expected []pgtype.UUID, got %T", got)
	}
	want := []string{
		"7113aab7-628f-4050-ae49-dbecac110ca8",
		"a5d79c54-81c3-4a73-af6a-ad5c143d3f21",
	}
	if len(list) != len(want) {
		t.Fatalf("expected %d elements, got %d", len(want), len(list))
	}
	for i, w := range want {
		if got := list[i].String(); got != w {
			t.Errorf("element %d: expected %q, got %q", i, w, got)
		}
	}
}

func TestTransform_UUIDListFormatRejectsInvalidPart(t *testing.T) {
	raw := "cc75b164-273c-4dce-9cdf-292045a0d38b\x00not-a-uuid"
	if _, err := Transform("uuid_list_format", raw); err == nil {
		t.Fatal("expected an error for an unparseable UUID part")
	}
}

func TestTransform_UUIDListFormatTreatsEmptyStringAsNull(t *testing.T) {
	got, err := Transform("uuid_list_format", "")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil (SQL NULL) for an empty string, got %v", got)
	}
}

func TestTransform_ExcelSerialToTimestamptz(t *testing.T) {
	// 44197 is the Excel serial number for 2021-01-01.
	got, err := Transform("excel_serial_to_timestamptz", float64(44197))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2021 || tm.Month() != time.January || tm.Day() != 1 {
		t.Errorf("expected 2021-01-01, got %v", tm)
	}
}

func TestTransform_ExcelSerialToTimestamptzKeepsFractionalTimeOfDay(t *testing.T) {
	// 44197.5 is noon on 2021-01-01.
	got, err := Transform("excel_serial_to_timestamptz", float64(44197.5))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Hour() != 12 {
		t.Errorf("expected noon, got %v", tm)
	}
}

func TestTransform_DayFirstToTimestamptz(t *testing.T) {
	got, err := Transform("dayfirst_to_timestamptz", "31/07/2006")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2006 || tm.Month() != time.July || tm.Day() != 31 {
		t.Errorf("expected 2006-07-31, got %v", tm)
	}
}

func TestTransform_DayFirstToTimestamptzRejectsUnparseableValues(t *testing.T) {
	if _, err := Transform("dayfirst_to_timestamptz", "not-a-date"); err == nil {
		t.Fatal("expected an error for an unparseable day-first date")
	}
}

func TestTransform_ISO8601ToTimestamptz_USStyleAMPM(t *testing.T) {
	// neh-grants.db's CouncilDate/BeginGrant/EndGrant columns — this must
	// convert successfully through the same shared layout list the
	// iso8601_timestamp heuristic uses to classify the column as
	// timestamptz in the first place, or the heuristic's promise and the
	// transform's actual behavior would drift apart again.
	got, err := Transform("iso8601_to_timestamptz", "7/31/2006 12:00:00 AM")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2006 || tm.Month() != time.July || tm.Day() != 31 {
		t.Errorf("expected 2006-07-31, got %v", tm)
	}
}

func TestTransform_IntToBool(t *testing.T) {
	one, err := Transform("int_to_bool", int64(1))
	if err != nil || one != true {
		t.Errorf("expected true, got %v, err %v", one, err)
	}
	zero, err := Transform("int_to_bool", int64(0))
	if err != nil || zero != false {
		t.Errorf("expected false, got %v, err %v", zero, err)
	}
}

// TestTransform_IntToBoolAcceptsZeroOneStrings covers issue #1's Part A:
// boolean01 now assigns int_to_bool to TEXT/CHAR-affinity 0/1 columns too
// (e.g. sakila.db's customer.active, CHAR(1) storing '0'/'1'), so the
// transform must actually handle the string form, not just int64/int.
func TestTransform_IntToBoolAcceptsZeroOneStrings(t *testing.T) {
	one, err := Transform("int_to_bool", "1")
	if err != nil || one != true {
		t.Errorf("expected true, got %v, err %v", one, err)
	}
	zero, err := Transform("int_to_bool", "0")
	if err != nil || zero != false {
		t.Errorf("expected false, got %v, err %v", zero, err)
	}
	if _, err := Transform("int_to_bool", "2"); err == nil {
		t.Error("expected an error for a string that isn't exactly \"0\" or \"1\"")
	}
	if _, err := Transform("int_to_bool", "01"); err == nil {
		t.Error("expected an error for an ambiguous digit string like \"01\"")
	}
}

func TestTransform_JulianDayToDate(t *testing.T) {
	// JDN 2440588 is the well-known reference point for 1970-01-01
	// (Julian Date 2440587.5 = 1970-01-01 00:00 UT).
	got, err := Transform("julian_day_to_date", float64(2440588.0))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 1970 || tm.Month() != time.January || tm.Day() != 1 {
		t.Errorf("expected 1970-01-01, got %v", tm)
	}
}

// Issue #24: julian_day_to_date truncated the fractional Julian Day
// (int64(f)) instead of rounding to the nearest whole Julian Day Number
// (math.Floor(f + 0.5), the standard JD-to-JDN conversion). Astronomical
// Julian Day is noon-based: JD N.0 is noon UT on the calendar day Fliegel
// & Van Flandern's algorithm assigns to JDN N, so JD N.0..N.5 (noon to
// midnight) still belongs to that same calendar day, while JD N.5..N+1.0
// (midnight to the following noon) belongs to calendar day N+1. Truncation
// always floors to N regardless of the fraction, so any value in the
// midnight-to-noon half of the range came out one day too EARLY.
func TestTransform_JulianDayToDate_RoundsFractionalDay(t *testing.T) {
	// 2453975.25 is 18:00 UT on the calendar day JDN 2453975 represents
	// (2006-08-27): still in the noon-to-midnight half, so both plain
	// truncation and correct rounding agree on 2006-08-27. This confirms
	// the rounding fix does not disturb the already-correct half of the
	// range.
	got, err := Transform("julian_day_to_date", float64(2453975.25))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2006 || tm.Month() != time.August || tm.Day() != 27 {
		t.Errorf("expected 2006-08-27, got %v", tm)
	}

	// 2453975.75 is 06:00 UT the following calendar day (2006-08-28): the
	// midnight-to-noon half of the range. Truncating the fraction floors
	// to JDN 2453975 (2006-08-27) — one day too early. Rounding to the
	// nearest JDN (math.Floor(f + 0.5) = 2453976) gives the correct
	// 2006-08-28.
	got, err = Transform("julian_day_to_date", float64(2453975.75))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok = got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 2006 || tm.Month() != time.August || tm.Day() != 28 {
		t.Errorf("expected 2006-08-28, got %v", tm)
	}
}

// TestTransform_JulianDayToDate_ExactMidnightRoundsForward pins down the
// exact-.5 (midnight) tie using a historically documented constant: JD
// 2299160.5 is 00:00:00 UT on 1582-10-15, the Gregorian calendar reform
// date, whose Julian Day Number is the equally well-documented 2299161
// (not 2299160). This proves an exact .5 belongs to the day that BEGINS at
// that instant, not the day that precedes it — so the geodatabase
// fixtures' realdate columns, which store only exact-midnight .5 values,
// were never "correct by luck" as issue #24 assumed; plain truncation
// mapped every one of them to the day before the correct one. This is a
// broader fix than "half of all fractional values": it corrects the exact
// midnight case too.
func TestTransform_JulianDayToDate_ExactMidnightRoundsForward(t *testing.T) {
	got, err := Transform("julian_day_to_date", float64(2299160.5))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 1582 || tm.Month() != time.October || tm.Day() != 15 {
		t.Errorf("expected 1582-10-15 (JDN 2299161), got %v", tm)
	}
}

func TestTransform_YYYYMMDDToDate(t *testing.T) {
	cases := []profiler.Value{int64(20210927), "20210927"}
	for _, raw := range cases {
		got, err := Transform("yyyymmdd_to_date", raw)
		if err != nil {
			t.Fatalf("Transform(%v): %v", raw, err)
		}
		tm, ok := got.(time.Time)
		if !ok {
			t.Fatalf("expected time.Time, got %T", got)
		}
		if tm.Year() != 2021 || tm.Month() != time.September || tm.Day() != 27 {
			t.Errorf("expected 2021-09-27, got %v", tm)
		}
	}
}

func TestTransform_YYYYMMDDToDateRejectsUnparseableValues(t *testing.T) {
	if _, err := Transform("yyyymmdd_to_date", "YYYYMMDD"); err == nil {
		t.Error("expected an error for a non-date placeholder string")
	}
	if _, err := Transform("yyyymmdd_to_date", "20211301"); err == nil {
		t.Error("expected an error for an invalid calendar date (month 13)")
	}
}

func TestTransform_NullifSentinels(t *testing.T) {
	got, err := Transform("nullif_sentinels", "Unknown")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for a sentinel token, got %v", got)
	}

	got, err = Transform("nullif_sentinels", "1001")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != int64(1001) {
		t.Errorf("expected 1001, got %v", got)
	}
}

func TestTransform_NullValuesPassThroughUnchangedRegardlessOfTransform(t *testing.T) {
	var nilValue profiler.Value
	got, err := Transform("unix_epoch_seconds", nilValue)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil to pass through, got %v", got)
	}
}

// TestTransform_ISO8601ToDate_TimeTimeInput_MidnightPasses is issue #79's
// (audit finding H3) regression: modernc.org/sqlite scans a
// DATE/DATETIME/TIMESTAMP-declared column's value straight into time.Time,
// not string, so a streamed row for such a column reaches this transform
// as time.Time. The non-midnight guard must apply to that shape too, not
// just string input.
func TestTransform_ISO8601ToDate_TimeTimeInput_MidnightPasses(t *testing.T) {
	in := time.Date(1953, time.September, 2, 0, 0, 0, 0, time.UTC)
	got, err := Transform("iso8601_to_date", in)
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	if tm.Year() != 1953 || tm.Month() != time.September || tm.Day() != 2 {
		t.Errorf("expected 1953-09-02, got %v", tm)
	}
}

func TestTransform_ISO8601ToDate_TimeTimeInput_RejectsNonMidnightValues(t *testing.T) {
	in := time.Date(1996, time.January, 4, 14, 37, 0, 0, time.UTC)
	if _, err := Transform("iso8601_to_date", in); err == nil {
		t.Error("expected an error for a non-midnight time.Time, not a silent truncation")
	}
}

// TestTransform_ExcelSerialToTimestamptz_OutOfRangeSerialDoesNotOverflow is
// issue #82's (audit finding M3) regression: ExcelSerialDate's heuristic
// tolerates up to half its sample being outside the plausible Excel-serial
// window, and the transform then runs on every row regardless. An
// epoch-seconds-scale value (~1.7e9) sitting in an otherwise Excel-serial
// column used to overflow time.Duration's int64-nanosecond range and
// silently wrap to an arbitrary, plausible-looking WRONG date
// (2122-07-26, confirmed against the pre-fix arithmetic) — this checks the
// result lands nowhere near that wrapped value, i.e. the overflow is
// gone, not just relocated.
func TestTransform_ExcelSerialToTimestamptz_OutOfRangeSerialDoesNotOverflow(t *testing.T) {
	got, err := Transform("excel_serial_to_timestamptz", float64(1.7e9))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	tm, ok := got.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", got)
	}
	// The overflow this regression guards against wrapped 1.7e9 days into
	// the 2100s (a plausible-looking near-term year); the correct,
	// non-overflowed result for a serial this large is millions of years
	// in the future. Anything in a normal calendar-plausible range means
	// the wraparound bug is back.
	if tm.Year() < 100000 {
		t.Errorf("expected a wildly out-of-range year (no Duration overflow/wraparound), got %v", tm)
	}
}

// TestTransform_NullifSentinels_CommaFormattedDecimal is issue #85's
// (audit finding M6) regression: SentinelNull, the heuristic that assigns
// this transform, suggests "double precision" whenever a sampled value has
// a decimal component (comma-formatted or plain) — a real row like
// "1,234.56" is expected input here, but used to fail ParseInt and fall
// through to a raw string pgx can't binary-encode into float8.
func TestTransform_NullifSentinels_CommaFormattedDecimal(t *testing.T) {
	got, err := Transform("nullif_sentinels", "1,234.56")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	f, ok := got.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T (%v)", got, got)
	}
	if f != 1234.56 {
		t.Errorf("expected 1234.56, got %v", f)
	}
}

// TestTransform_NullifSentinels_RejectsGenuinelyUnparseableValues confirms
// the fix doesn't just widen the pass-through: a value that's neither a
// recognized sentinel token nor numeric in any form must still be an
// error, not a silent string pass-through into a numeric column.
func TestTransform_NullifSentinels_RejectsGenuinelyUnparseableValues(t *testing.T) {
	if _, err := Transform("nullif_sentinels", "not-a-number"); err == nil {
		t.Error("expected an error for a value that's neither a sentinel token nor numeric")
	}
}

// TestTransform_TextToJsonb_ValidatesBlobInput is issue #86's (audit
// finding M7) regression: GeoJSON.Evaluate skips non-string samples with
// continue rather than disqualifying the column, so a mostly-GeoJSON-text
// column can have a rare BLOB row (SQLite's dynamic typing permits it).
// The transform must validate a []byte input as JSON, not pass it through
// unchecked — the exact "can never fail" gap issue #22 fixed for the
// string case.
func TestTransform_TextToJsonb_ValidatesBlobInput(t *testing.T) {
	got, err := Transform("text_to_jsonb", []byte(`{"type":"Point","coordinates":[1,2]}`))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if _, ok := got.([]byte); !ok {
		t.Fatalf("expected []byte passed through, got %T", got)
	}

	if _, err := Transform("text_to_jsonb", []byte("not json")); err == nil {
		t.Error("expected an error for a non-JSON []byte value")
	}
}

// TestTransform_StripCommas_HandlesAlreadyNumericInput is issue #86's
// (audit finding M7) regression: CommaNumber.Evaluate skips non-string
// samples with continue, so a column can be mostly int64/float64-storage
// values with only a rare comma-formatted string row. An already-numeric
// raw value must convert correctly instead of passing through unexamined
// into an int4 column.
func TestTransform_StripCommas_HandlesAlreadyNumericInput(t *testing.T) {
	got, err := Transform("strip_commas", int64(42))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != int64(42) {
		t.Errorf("expected 42, got %v", got)
	}

	got, err = Transform("strip_commas", float64(42))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != int64(42) {
		t.Errorf("expected whole-number float64 to convert to int64(42), got %v (%T)", got, got)
	}

	if _, err := Transform("strip_commas", float64(42.5)); err == nil {
		t.Error("expected an error for a fractional float64 into an integer-targeted transform")
	}
}

// TestTransform_StripCommasFloat_HandlesAlreadyNumericInput mirrors
// TestTransform_StripCommas_HandlesAlreadyNumericInput for the
// double-precision-targeted variant.
func TestTransform_StripCommasFloat_HandlesAlreadyNumericInput(t *testing.T) {
	got, err := Transform("strip_commas_float", int64(42))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != float64(42) {
		t.Errorf("expected 42.0, got %v", got)
	}

	got, err = Transform("strip_commas_float", float64(42.5))
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != float64(42.5) {
		t.Errorf("expected 42.5, got %v", got)
	}
}

// TestTransform_JulianDayToDate_NegativeJDNUsesFloorDivision is issue #89's
// (audit finding L3) regression: Fliegel & Van Flandern's algorithm
// requires floor division; Go's / truncates toward zero, which disagrees
// for jdn < -68569. Expected values cross-checked against an independent
// day-count-to-civil-date algorithm (Howard Hinnant's civil_from_days),
// not against the buggy code itself.
func TestTransform_JulianDayToDate_NegativeJDNUsesFloorDivision(t *testing.T) {
	cases := []struct {
		jdn                    int64
		year, month, day int
	}{
		{-70000, -4904, 3, 30},
		{-68570, -4900, 2, 28},
		{-100000, -4986, 2, 9},
	}
	for _, c := range cases {
		// julian_day_to_date's transform input is JD (noon-based, so a
		// .5 fraction lands on the JDN's own calendar day); pass the
		// JDN directly as a whole float64.
		got, err := Transform("julian_day_to_date", float64(c.jdn))
		if err != nil {
			t.Fatalf("Transform(jdn=%d): %v", c.jdn, err)
		}
		tm, ok := got.(time.Time)
		if !ok {
			t.Fatalf("expected time.Time, got %T", got)
		}
		if tm.Year() != c.year || int(tm.Month()) != c.month || tm.Day() != c.day {
			t.Errorf("jdn=%d: expected %04d-%02d-%02d, got %v", c.jdn, c.year, c.month, c.day, tm)
		}
	}
}
