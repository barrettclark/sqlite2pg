package copywriter

import (
	"testing"
	"time"

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

func TestTransform_StripCommas(t *testing.T) {
	got, err := Transform("strip_commas", "2,949")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if got != int64(2949) {
		t.Errorf("expected 2949, got %v (%T)", got, got)
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
