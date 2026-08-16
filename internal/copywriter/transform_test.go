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
