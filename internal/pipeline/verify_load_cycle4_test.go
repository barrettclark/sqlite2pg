package pipeline

import (
	"math"
	"testing"
)

// issue #148 (audit cycle 4 L6): the documented invariant is
// valuesMatch(x, y) => sortKeyFor(x) == sortKeyFor(y). sortKeyFor's
// numeric case only listed int64 and float64, so a plain int or a
// float32 fell to the "\x09%T:%v" default and keyed apart from the
// "\x06num:" form the Postgres side (always int64/float64) uses — while
// valuesMatch's %v fallback still reported them equal. Latent (the
// pipeline normalises to 64-bit forms) but the invariant should hold by
// construction.
func TestValuesMatch_SortKeyInvariant_NarrowNumericTypes(t *testing.T) {
	cases := []struct {
		a, b any
	}{
		{int(5), int64(5)},
		{int(5), float64(5)},
		{float32(5), int64(5)},
		{float32(5), float64(5)},
		{int(-9000), int64(-9000)},
	}
	for _, c := range cases {
		if valuesMatch(c.a, c.b) && sortKeyFor(c.a) != sortKeyFor(c.b) {
			t.Errorf("valuesMatch(%[1]T %[1]v, %[2]T %[2]v) true but keys differ: %q vs %q",
				c.a, c.b, sortKeyFor(c.a), sortKeyFor(c.b))
		}
	}
}

// A float32 and a float64 that are genuinely different numbers (0.1 is
// not exactly representable, and the two widths round it differently)
// must be reported unequal by both paths — the old %v fallback rendered
// both as "0.1" and wrongly matched them.
func TestValuesMatch_Float32VsFloat64_DistinctValueReportedUnequal(t *testing.T) {
	a := float32(0.1)
	b := float64(0.1)
	if valuesMatch(a, b) {
		t.Errorf("valuesMatch(float32(0.1), float64(0.1)) = true; they are different numbers (%v vs %v)", float64(a), b)
	}
	if math.Float64bits(float64(a)) == math.Float64bits(b) {
		t.Skip("float32(0.1) widened equals float64(0.1) on this platform; nothing to prove")
	}
	if sortKeyFor(a) == sortKeyFor(b) {
		t.Errorf("sortKeyFor collapsed two distinct values to one key: %q", sortKeyFor(a))
	}
}
