package tui

import "testing"

// TestDateTransformPreview_DoesNotConvertInfinityToInt64 is issue #112's
// (L3) regression. The L6 fix swapped ParseInt for ParseFloat, which also
// accepts "Inf" and magnitudes past 2^63; math.Trunc(±Inf) == ±Inf, so
// the f == math.Trunc(f) gate passed for infinity and int64(f) ran on it
// — implementation-dependent per the Go spec. The magnitude guard now
// keeps int64(f) to the epoch window.
func TestDateTransformPreview_DoesNotConvertInfinityToInt64(t *testing.T) {
	for _, value := range []string{"Inf", "-Inf", "+Inf", "1e400", "-1e400", "1e19"} {
		// Must not panic, and must not "convert" a non-finite / absurd
		// magnitude into a plausible timestamp.
		_, transform, ok := dateTransformPreview(value, "timestamptz")
		if ok {
			t.Errorf("dateTransformPreview(%q, timestamptz) = (_, %q, true), want ok=false", value, transform)
		}
	}
}

// TestDateTransformPreview_StillRecognizesRealEpochValues guards the #112
// magnitude gate against being too tight — the epoch values the L6 test
// covers must still preview.
func TestDateTransformPreview_StillRecognizesRealEpochValues(t *testing.T) {
	cases := map[string]string{
		"1712345678":       "unix_epoch_seconds",
		"1712345678000":    "unix_epoch_millis",
		"1712345678000000": "unix_epoch_micros",
		"1.712345678e+09":  "unix_epoch_seconds", // scientific-notation form (issue #92)
	}
	for value, wantTransform := range cases {
		tm, transform, ok := dateTransformPreview(value, "timestamptz")
		if !ok {
			t.Errorf("dateTransformPreview(%q, timestamptz): ok=false, want a preview", value)
			continue
		}
		if transform != wantTransform {
			t.Errorf("dateTransformPreview(%q, timestamptz) transform = %q, want %q", value, transform, wantTransform)
		}
		if tm.Year() < 2000 || tm.Year() > 2100 {
			t.Errorf("dateTransformPreview(%q, timestamptz) = %v, want a year near 2024", value, tm)
		}
	}
}
