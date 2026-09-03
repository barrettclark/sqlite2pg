package tui

import "testing"

// issue #156 (audit cycle 5 M1): PR #151's exponent-normalization in the
// integer arm of previewValueForType assumed "that form is only ever
// produced from a float64". It isn't — `value` is fmt's %v of the raw
// SQLite value, and for a *string* value %v is the string verbatim. A
// TEXT column literally holding "1.5e3" would get bigint +
// numeric_text_to_integer committed by the picker, then COPY aborts on
// the raw string ("has a non-zero fractional part").
//
// The discriminator: a genuine float64 rendered by %v round-trips through
// ParseFloat + %v; an arbitrary scientific-notation string does not
// (%v prints the shortest form, e.g. "1500", not "1.5e3").
func TestPreviewValueForType_IntegerRejectsNonFloatScientificNotationString(t *testing.T) {
	// String-typed values that are NOT what %v of a float64 produces —
	// the picker must not offer an integer type for these.
	for _, v := range []string{"1.5e3", "1.23e+05", "2.5e1", "1.0e2"} {
		for _, typ := range []string{"integer", "bigint", "smallint"} {
			if _, _, valid := previewValueForType(v, typ); valid {
				t.Errorf("previewValueForType(%q, %q) = valid; the raw string fails numeric_text_to_integer at COPY", v, typ)
			}
		}
	}
}

// The genuine-float64 renderings from #139 must still be accepted (%v of
// a float64 whole number past 1e6 is scientific notation, and it
// round-trips).
func TestPreviewValueForType_IntegerStillAcceptsFloat64ScientificRenderings(t *testing.T) {
	cases := []struct {
		value, want string
	}{
		{"1e+06", "1000000"},
		{"1.712345678e+09", "1712345678"},
	}
	for _, c := range cases {
		display, transform, valid := previewValueForType(c.value, "bigint")
		if !valid || display != c.want || transform != "numeric_text_to_integer" {
			t.Errorf("previewValueForType(%q, bigint) = (%q, %q, %v), want (%q, numeric_text_to_integer, true)",
				c.value, display, transform, valid, c.want)
		}
	}
}

// Plain digit strings (incl. a 19-digit exact ID) are still untouched.
func TestPreviewValueForType_IntegerPlainDigitStringsUnaffectedByCycle5Guard(t *testing.T) {
	display, _, valid := previewValueForType("2124037125711300644", "bigint")
	if !valid || display != "2124037125711300644" {
		t.Errorf("previewValueForType(19-digit, bigint) = (%q, %v), want (%q, true)", display, valid, "2124037125711300644")
	}
}
