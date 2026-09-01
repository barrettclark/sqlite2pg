package pipeline

import "testing"

// TestCompareColumnUnordered_UnequalLengths_DoesNotPanic covers issue #67:
// compareColumnUnordered looped `for i := range sortedExpected` and
// indexed `sortedActual[i]`, so if the Postgres side came back with fewer
// rows than the SQLite side — a row DELETEd by a concurrent writer between
// VerifyTable's COUNT(*) and its SELECT — it panicked with
// index-out-of-range instead of surfacing the discrepancy. It must not
// panic regardless of which side is shorter.
func TestCompareColumnUnordered_UnequalLengths_DoesNotPanic(t *testing.T) {
	cases := []struct {
		name             string
		expected, actual []any
	}{
		{"expected longer", []any{int64(1), int64(2), int64(3)}, []any{int64(1)}},
		{"actual longer", []any{int64(1)}, []any{int64(1), int64(2), int64(3)}},
		{"expected empty", nil, []any{int64(1)}},
		{"actual empty", []any{int64(1)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Must return, not panic. Content of the result is not
			// asserted here — verifyTableUnordered is what turns the
			// length discrepancy into a reported error.
			_ = compareColumnUnordered(c.expected, c.actual)
		})
	}
}

// TestCompareColumnUnordered_EqualLengths_Unchanged confirms the panic
// guard doesn't alter the normal equal-length path.
func TestCompareColumnUnordered_EqualLengths_Unchanged(t *testing.T) {
	m := compareColumnUnordered([]any{int64(1), int64(2)}, []any{int64(2), int64(1)})
	if len(m) != 0 {
		t.Errorf("compareColumnUnordered of two equal multisets reported %d mismatch(es): %+v", len(m), m)
	}
	m = compareColumnUnordered([]any{int64(1), int64(2)}, []any{int64(1), int64(3)})
	if len(m) != 1 {
		t.Errorf("compareColumnUnordered found %d mismatch(es), want 1", len(m))
	}
}
