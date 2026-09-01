package pipeline

// Reproduces issue #83 (whole-codebase audit, finding M4): a text-target
// column (fallbackTypeFor returns "text" for a TEXT-declared column whose
// sample is all strings, and "text" is exempt from storage-class checks)
// can still hold a BLOB row somewhere in the full table, since SQLite's
// dynamic typing doesn't enforce column affinity. pgx's TextCodec accepts
// []byte for a text column at COPY time without complaint, so the load
// succeeds — but expected (raw, untransformed, straight from SQLite) is
// []byte while actual (scanned back via newPgColumnScanner's pgtype.Text
// fallback) is string, and valuesMatch's []byte case required actual to
// also be []byte, falling through to the %v fallback which renders the
// two sides differently ([]byte("hi") -> "[104 105]" vs "hi") and reports
// a false mismatch on every such row.

import "testing"

func TestValuesMatch_BytesExpectedStringActual_Equal(t *testing.T) {
	if !valuesMatch([]byte("hi"), "hi") {
		t.Error(`valuesMatch([]byte("hi"), "hi") = false, want true (same text content, just a []byte/string shape mismatch from a BLOB row in a text-target column)`)
	}
}

func TestValuesMatch_BytesExpectedStringActual_Unequal(t *testing.T) {
	if valuesMatch([]byte("hi"), "bye") {
		t.Error(`valuesMatch([]byte("hi"), "bye") = true, want false (these are genuinely different content)`)
	}
}

func TestSortKeyFor_BytesAndEqualStringShareKey(t *testing.T) {
	got1 := sortKeyFor([]byte("hi"))
	got2 := sortKeyFor("hi")
	if got1 != got2 {
		t.Errorf("sortKeyFor([]byte(\"hi\")) = %q, sortKeyFor(\"hi\") = %q; want equal keys", got1, got2)
	}
}

func TestCompareColumnUnordered_NoFalsePositiveForBytesVsString(t *testing.T) {
	expected := []any{[]byte("alpha"), "beta", []byte("gamma")}
	actual := []any{"gamma", "alpha", "beta"}

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 0 {
		t.Errorf("compareColumnUnordered reported %d false-positive mismatch(es): %+v", len(mismatches), mismatches)
	}
}

func TestCompareColumnUnordered_StillDetectsGenuineBytesVsStringMismatch(t *testing.T) {
	expected := []any{[]byte("alpha"), "beta"}
	actual := []any{"alpha", "wrong"}

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 1 {
		t.Fatalf("compareColumnUnordered found %d mismatch(es), want exactly 1: %+v", len(mismatches), mismatches)
	}
}
