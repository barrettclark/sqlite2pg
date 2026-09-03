package tui

import "testing"

// issue #139 (audit cycle 4 M1): a REAL-affinity column of large whole
// numbers renders its samples through fmt's %v, which switches to
// scientific notation past 1e6 ("1.712345678e+09"). Commit 5522180 routed
// the integer arm through numeric_text_to_integer, whose exact-integer
// parse rejects an exponent outright — so integer/bigint/smallint
// silently vanished from the type picker for exactly the column shape the
// repo's own bikes.last_reported example has, while timestamptz and
// double precision stayed on offer.
func TestPreviewValueForType_IntegerAcceptsScientificNotationWholeNumber(t *testing.T) {
	cases := []struct {
		value      string
		targetType string
		wantValid  bool
		wantResult string
	}{
		{"1.712345678e+09", "bigint", true, "1712345678"},
		{"1.712345678e+09", "integer", true, "1712345678"},
		{"1e+06", "integer", true, "1000000"},
		{"1e+06", "smallint", false, ""}, // 1000000 is outside int2
		// "1.5e+02" is NOT what %v prints for float64(150) ("150"), so
		// per issue #156 it is treated as a literal TEXT value that
		// numeric_text_to_integer rejects at COPY — not a float64
		// rendering to normalize.
		{"1.5e+02", "smallint", false, ""},
		{"1.712345678e+02", "integer", false, ""}, // 171.23… has a real fraction
	}
	for _, c := range cases {
		display, transform, valid := previewValueForType(c.value, c.targetType)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) valid = %v, want %v", c.value, c.targetType, valid, c.wantValid)
			continue
		}
		if c.wantValid {
			if display != c.wantResult {
				t.Errorf("previewValueForType(%q, %q) display = %q, want %q", c.value, c.targetType, display, c.wantResult)
			}
			if transform != "numeric_text_to_integer" {
				t.Errorf("previewValueForType(%q, %q) transform = %q, want numeric_text_to_integer", c.value, c.targetType, transform)
			}
		}
	}
}

// The scientific-notation normalization must not weaken issue #15's
// protection for exact integers past float64's precision: a plain digit
// string has no exponent and must still take the exact-parse path
// unchanged.
func TestPreviewValueForType_ScientificNormalizationLeavesExactDigitStringsAlone(t *testing.T) {
	display, _, valid := previewValueForType("2124037125711300644", "bigint")
	if !valid || display != "2124037125711300644" {
		t.Errorf("previewValueForType(19-digit) = (%q, %v), want (%q, true)", display, valid, "2124037125711300644")
	}
}

// issue #139, picker-level: integer/bigint must reappear in the offered
// list for a scientific-notation sample.
func TestValidTypesForColumn_OffersIntegerTypesForScientificNotationSample(t *testing.T) {
	got := validTypesForColumn([]string{"1.712345678e+09"}, "double precision")
	var haveBigint, haveInteger bool
	for _, typ := range got {
		switch typ {
		case "bigint":
			haveBigint = true
		case "integer":
			haveInteger = true
		}
	}
	if !haveBigint || !haveInteger {
		t.Errorf("expected bigint and integer offered for a scientific-notation whole-number sample, got %v", got)
	}
}
