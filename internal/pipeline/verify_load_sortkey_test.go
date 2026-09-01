package pipeline

import "testing"

// TestSortKeyFor_IntAndFloatEqualValuesShareAKey reproduces the exact
// sample-types.sqlite/type_demo.c_numeric false positive found by the
// final validation campaign: a NUMERIC column stored dynamically in
// SQLite as an integer (int64(100)), transformed straight through to a
// Postgres `double precision` target and scanned back as float64(100).
// valuesMatch already treats these as equal (its int64/float64 typed
// cases don't fire across differing concrete types, so it falls to the
// fmt.Sprintf("%v", ...) fallback, which renders both as "100") —
// sortKeyFor must agree, or compareColumnUnordered sorts them into
// different buckets and reports a spurious mismatch.
func TestSortKeyFor_IntAndFloatEqualValuesShareAKey(t *testing.T) {
	if !valuesMatch(int64(100), float64(100)) {
		t.Fatal("test setup invalid: valuesMatch itself no longer considers int64(100) and float64(100) equal")
	}
	got1 := sortKeyFor(int64(100))
	got2 := sortKeyFor(float64(100))
	if got1 != got2 {
		t.Errorf("sortKeyFor(int64(100)) = %q, sortKeyFor(float64(100)) = %q; want equal keys since valuesMatch treats them as equal", got1, got2)
	}
}

// TestSortKeyFor_AgreesWithValuesMatch_AcrossNumericTypePairs checks every
// int64/float64 pair valuesMatch's own logic considers equal or unequal,
// confirming sortKeyFor's invariant ("two values valuesMatch considers
// equal always produce the same key") holds generally, not just for the
// one reported 100/100.0 case, and that genuinely different values still
// sort apart (so the fix isn't accidentally too permissive).
func TestSortKeyFor_AgreesWithValuesMatch_AcrossNumericTypePairs(t *testing.T) {
	cases := []struct {
		name string
		a, b any
	}{
		{"int64 vs equal float64", int64(100), float64(100)},
		{"float64 vs equal int64", float64(42), int64(42)},
		{"int64 zero vs float64 zero", int64(0), float64(0)},
		{"negative int64 vs equal float64", int64(-7), float64(-7)},
		{"equal int64s", int64(100), int64(100)},
		{"equal float64s", float64(3.5), float64(3.5)},
		{"unequal int64s", int64(100), int64(200)},
		{"unequal float64s", float64(100), float64(100.5)},
		{"int64 vs unequal float64", int64(100), float64(100.5)},
		{"float64 vs unequal int64", float64(100.5), int64(100)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantEqualKeys := valuesMatch(c.a, c.b)
			keyA := sortKeyFor(c.a)
			keyB := sortKeyFor(c.b)
			gotEqualKeys := keyA == keyB
			if gotEqualKeys != wantEqualKeys {
				t.Errorf("valuesMatch(%#v, %#v) = %v, but sortKeyFor equality = %v (keys %q vs %q)",
					c.a, c.b, wantEqualKeys, gotEqualKeys, keyA, keyB)
			}
		})
	}
}

// TestCompareColumnUnordered_NoFalsePositiveForIntVsFloatEqualValue is the
// end-to-end (within the package, no DB needed) reproduction of the
// type_demo.c_numeric false positive: one column's expected slice holds
// an int64, the actual slice (as Postgres/pgx would hand back a `double
// precision` scan) holds the equal-valued float64. Before the fix,
// compareColumnUnordered reported this as a mismatch despite both values
// printing identically.
func TestCompareColumnUnordered_NoFalsePositiveForIntVsFloatEqualValue(t *testing.T) {
	expected := []any{int64(100), int64(1), int64(2)}
	actual := []any{float64(2), float64(100), float64(1)}

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 0 {
		t.Errorf("compareColumnUnordered reported %d false-positive mismatch(es): %+v", len(mismatches), mismatches)
	}
}

// TestCompareColumnUnordered_StillDetectsGenuineNumericMismatch guards
// against the fix becoming too permissive: an actual value that is
// genuinely different (not just differently-typed-but-equal) must still
// be reported. Values are chosen so lexicographic sort-key order (what
// compareColumnUnordered actually sorts by) matches numeric order for
// both slices, keeping this test focused purely on the int64-vs-float64
// equal/unequal distinction rather than incidentally exercising sortKeyFor's
// separate (pre-existing, out of scope here) lexicographic-vs-numeric
// string-sort quirk.
func TestCompareColumnUnordered_StillDetectsGenuineNumericMismatch(t *testing.T) {
	expected := []any{int64(100), int64(1)}
	actual := []any{float64(1), float64(105)} // 105 != 100, a real corruption

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 1 {
		t.Fatalf("compareColumnUnordered found %d mismatch(es), want exactly 1 (the genuine 100-vs-105 difference): %+v", len(mismatches), mismatches)
	}
	if mismatches[0].Expected != int64(100) || mismatches[0].Actual != float64(105) {
		t.Errorf("unexpected mismatch content: %+v", mismatches[0])
	}
}
