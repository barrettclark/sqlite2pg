package pipeline

import "testing"

// TestValuesMatch_LargeIntVsFloatEqual reproduces the deeper version of the
// bug 9de206a only partially fixed: valuesMatch's cross-type fallback uses
// fmt.Sprintf("%v", ...), and Go's default float formatting switches to
// scientific notation above a certain magnitude
// (fmt.Sprintf("%v", float64(2454348)) == "2.454348e+06") while the int64
// side stays plain decimal ("2454348") — so two numerically identical
// values compare as different strings and valuesMatch reports a
// false-positive mismatch. Reproduced against sqliterepo.db's
// event.mtime/omtime/ftsdocs.mtime Julian-day-number columns (one
// int-affinity row mixed into an otherwise-REAL column).
func TestValuesMatch_LargeIntVsFloatEqual(t *testing.T) {
	if !valuesMatch(int64(2454348), float64(2454348)) {
		t.Error("valuesMatch(int64(2454348), float64(2454348)) = false, want true (same number, scientific-notation formatting must not matter)")
	}
	if !valuesMatch(float64(2454348), int64(2454348)) {
		t.Error("valuesMatch(float64(2454348), int64(2454348)) = false, want true (same number, scientific-notation formatting must not matter)")
	}
}

// TestValuesMatch_LargeIntVsFloatUnequal guards against the fix becoming
// too permissive: genuinely different large numbers must still be reported
// as a mismatch.
func TestValuesMatch_LargeIntVsFloatUnequal(t *testing.T) {
	if valuesMatch(int64(2454348), float64(2454349)) {
		t.Error("valuesMatch(int64(2454348), float64(2454349)) = true, want false (these are genuinely different numbers)")
	}
}

// TestSortKeyFor_LargeIntVsFloatShareKey confirms sortKeyFor (which
// 9de206a's partial fix made mirror valuesMatch's own flawed
// fmt.Sprintf("%v", ...) fallback) doesn't inherit the same
// scientific-notation divergence for large numbers.
func TestSortKeyFor_LargeIntVsFloatShareKey(t *testing.T) {
	got1 := sortKeyFor(int64(2454348))
	got2 := sortKeyFor(float64(2454348))
	if got1 != got2 {
		t.Errorf("sortKeyFor(int64(2454348)) = %q, sortKeyFor(float64(2454348)) = %q; want equal keys", got1, got2)
	}
}

// TestCompareColumnUnordered_NoFalsePositiveForLargeIntVsFloat is the
// end-to-end (within-package) reproduction of the sqliterepo.db
// event.mtime false positive via the no-PK aggregate comparison path.
func TestCompareColumnUnordered_NoFalsePositiveForLargeIntVsFloat(t *testing.T) {
	expected := []any{int64(2454348), int64(2454300), int64(2454999)}
	actual := []any{float64(2454999), float64(2454348), float64(2454300)}

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 0 {
		t.Errorf("compareColumnUnordered reported %d false-positive mismatch(es): %+v", len(mismatches), mismatches)
	}
}

// TestCompareColumnUnordered_StillDetectsGenuineLargeNumericMismatch guards
// the aggregate path against becoming too permissive for large numbers too.
func TestCompareColumnUnordered_StillDetectsGenuineLargeNumericMismatch(t *testing.T) {
	expected := []any{int64(2454348), int64(1)}
	actual := []any{float64(1), float64(9999999)} // genuinely different from 2454348

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 1 {
		t.Fatalf("compareColumnUnordered found %d mismatch(es), want exactly 1: %+v", len(mismatches), mismatches)
	}
}
