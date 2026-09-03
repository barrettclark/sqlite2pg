package pipeline

import (
	"math"
	"testing"
)

// TestExactNumericEqual_NaNMatchesNaN is issue #122's (L13) regression:
// numericSortKey renders every NaN float64 to the same key ("NaN"), but
// exactNumericEqual returned `NaN == NaN` — false in Go — so the
// documented sortKeyFor invariant ("two values VerifyTable considers
// equal always produce the same key") was broken in the harmless
// direction (equal keys, "unequal" values). compareColumnUnordered
// compares keys only, so no wrong verdict resulted today; this pins the
// two back in agreement, matching PostgreSQL's own `NaN = NaN` = true.
func TestExactNumericEqual_NaNMatchesNaN(t *testing.T) {
	equal, ok := exactNumericEqual(math.NaN(), math.NaN())
	if !ok {
		t.Fatal("expected ok=true for two float64 values")
	}
	if !equal {
		t.Error("exactNumericEqual(NaN, NaN) = false; want true (numericSortKey keys them the same, and Postgres defines NaN = NaN as true)")
	}

	// And the sort keys still agree, both directions of the invariant.
	k1, _ := numericSortKey(math.NaN())
	k2, _ := numericSortKey(math.NaN())
	if k1 != k2 {
		t.Errorf("numericSortKey NaN keys differ: %q vs %q", k1, k2)
	}

	// A NaN and a real number must NOT be equal, and must NOT share a key.
	if eq, _ := exactNumericEqual(math.NaN(), 1.0); eq {
		t.Error("exactNumericEqual(NaN, 1.0) = true; want false")
	}
	if nk, _ := numericSortKey(1.0); nk == k1 {
		t.Errorf("numericSortKey(1.0) collided with the NaN key %q", k1)
	}
}
