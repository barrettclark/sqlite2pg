package pipeline

import (
	"math"
	"testing"
)

// TestValuesMatch_CrossTypeInt64BeyondFloat64Precision covers issue #65:
// an int64 whose value is NOT exactly representable in float64 (|n| > 2^53)
// must not compare equal to the nearest float64. That float64 is exactly
// what a `double precision` column stored — i.e. the load lost precision —
// and verify exists to catch precisely that. The old code converted the
// int64 through float64 unconditionally (numericValue) and so reported a
// false match.
func TestValuesMatch_CrossTypeInt64BeyondFloat64Precision(t *testing.T) {
	n := int64(9007199254740993)    // 2^53 + 1 — not representable in float64
	f := float64(9007199254740992)  // 2^53 — what `double precision` would actually hold
	if valuesMatch(n, f) {
		t.Errorf("valuesMatch(int64(%d), float64(%v)) = true, want false — the int64 is not exactly this float64; the load lost precision", n, f)
	}
	if valuesMatch(f, n) {
		t.Errorf("valuesMatch(float64(%v), int64(%d)) = true, want false (symmetry)", f, n)
	}
}

// TestValuesMatch_CrossTypeInt64WithinFloat64Precision is the counterpart:
// an int64 that IS exactly representable in float64 must still compare
// equal to it — e6bc33e's legitimate cross-type case (a SQLite NUMERIC
// integer row against a `double precision` target) keeps working.
func TestValuesMatch_CrossTypeInt64WithinFloat64Precision(t *testing.T) {
	cases := []int64{0, 1, -1, 100, 2454348, 1 << 52, 1 << 53, -(1 << 53), 9007199254740994 /* 2^53+2, representable */}
	for _, n := range cases {
		if !valuesMatch(n, float64(n)) {
			t.Errorf("valuesMatch(int64(%d), float64(%d)) = false, want true (exactly representable in float64)", n, n)
		}
		if !valuesMatch(float64(n), n) {
			t.Errorf("valuesMatch(float64(%d), int64(%d)) = false, want true (symmetry)", n, n)
		}
	}
}

// TestValuesMatch_SortKeyInvariant_CrossType asserts sortKeyFor's
// documented invariant directly for int64/float64 pairs across the 2^53
// boundary: valuesMatch(x,y) ⇒ sortKeyFor(x) == sortKeyFor(y). A violation
// means the same data passes verify on a table with a primary key and
// fails on one without.
func TestValuesMatch_SortKeyInvariant_CrossType(t *testing.T) {
	pairs := []struct {
		n int64
		f float64
	}{
		{9007199254740993, 9007199254740992},
		{9007199254740992, 9007199254740992},
		{100, 100},
		{math.MaxInt64, 9223372036854775808.0},
		{math.MinInt64, -9223372036854775808.0},
	}
	for _, p := range pairs {
		if valuesMatch(any(p.n), any(p.f)) && sortKeyFor(any(p.n)) != sortKeyFor(any(p.f)) {
			t.Errorf("valuesMatch(int64(%d), float64(%v)) = true but sortKeyFor keys differ: %q vs %q",
				p.n, p.f, sortKeyFor(any(p.n)), sortKeyFor(any(p.f)))
		}
	}
}

// TestCompareColumnUnordered_FlagsCrossTypePrecisionLoss is the
// end-to-end (within-package) no-PK reproduction: a source value above
// 2^53 stored into `double precision` as a rounded float64 must be
// reported as a mismatch, not silently passed.
func TestCompareColumnUnordered_FlagsCrossTypePrecisionLoss(t *testing.T) {
	expected := []any{int64(9007199254740993), int64(5)}
	actual := []any{float64(9007199254740992), float64(5)}

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 1 {
		t.Fatalf("compareColumnUnordered found %d mismatch(es), want exactly 1 (the cross-type precision loss): %+v", len(mismatches), mismatches)
	}
}
