package pipeline

import "testing"

// TestValuesMatch_LargeDistinctInt64sNotEqual reproduces the precision-loss
// regression in valuesMatch/numericValue: two DISTINCT int64 values above
// 2^53 (float64's exact-integer precision limit) both round-trip to the
// identical float64 when numericValue converts them unconditionally, so
// valuesMatch previously (incorrectly) reported them as equal — a silent
// false negative that would let real data corruption in a bigint/rowid
// column go undetected. Same-type int64-vs-int64 comparison must be exact,
// never routed through float64 at all.
func TestValuesMatch_LargeDistinctInt64sNotEqual(t *testing.T) {
	a := int64(9223372036854775807) // math.MaxInt64
	b := int64(9223372036854775806) // one less — distinct, but collides with a as float64
	if float64(a) != float64(b) {
		t.Fatalf("test setup invalid: expected these two int64s to collide when converted to float64, but float64(%d)=%v != float64(%d)=%v", a, float64(a), b, float64(b))
	}
	if valuesMatch(a, b) {
		t.Errorf("valuesMatch(%d, %d) = true, want false — these are genuinely different int64 values that must not be compared via lossy float64 conversion", a, b)
	}
	if valuesMatch(b, a) {
		t.Errorf("valuesMatch(%d, %d) = true, want false", b, a)
	}
}

// TestValuesMatch_LargeEqualInt64sStillEqual is the sanity counterpart:
// two genuinely equal large int64 values must still compare equal.
func TestValuesMatch_LargeEqualInt64sStillEqual(t *testing.T) {
	a := int64(9223372036854775807)
	b := int64(9223372036854775807)
	if !valuesMatch(a, b) {
		t.Errorf("valuesMatch(%d, %d) = false, want true (identical values)", a, b)
	}
}

// TestValuesMatch_CrossTypeLargeNumberStillWorks confirms e6bc33e's
// original fix (int64-vs-float64 cross-type equality for the same number,
// no scientific-notation formatting mismatch) still works after this fix.
func TestValuesMatch_CrossTypeLargeNumberStillWorks(t *testing.T) {
	if !valuesMatch(int64(2454348), float64(2454348)) {
		t.Error("valuesMatch(int64(2454348), float64(2454348)) = false, want true")
	}
	if !valuesMatch(float64(2454348), int64(2454348)) {
		t.Error("valuesMatch(float64(2454348), int64(2454348)) = false, want true")
	}
}

// TestSortKeyFor_LargeDistinctInt64sDoNotCollide is sortKeyFor's version of
// TestValuesMatch_LargeDistinctInt64sNotEqual: two distinct large int64
// values must not produce the same sort key, or compareColumnUnordered
// (the no-primary-key comparison path) would silently treat them as
// matching too.
func TestSortKeyFor_LargeDistinctInt64sDoNotCollide(t *testing.T) {
	a := int64(9223372036854775807)
	b := int64(9223372036854775806)
	keyA := sortKeyFor(a)
	keyB := sortKeyFor(b)
	if keyA == keyB {
		t.Errorf("sortKeyFor(%d) = %q, sortKeyFor(%d) = %q; want distinct keys for distinct int64 values", a, keyA, b, keyB)
	}
}

// TestSortKeyFor_CrossTypeLargeNumberStillShared confirms sortKeyFor still
// gives the same key to an int64 and an equal-valued float64 (the
// e6bc33e/9de206a cross-type fix) after this precision fix.
func TestSortKeyFor_CrossTypeLargeNumberStillShared(t *testing.T) {
	got1 := sortKeyFor(int64(2454348))
	got2 := sortKeyFor(float64(2454348))
	if got1 != got2 {
		t.Errorf("sortKeyFor(int64(2454348)) = %q, sortKeyFor(float64(2454348)) = %q; want equal keys", got1, got2)
	}
}

// TestCompareColumnUnordered_DetectsLargeInt64Corruption is the end-to-end
// (within package) reproduction: a bigint column holding two distinct
// large values, one corrupted on the Postgres side to a different large
// value that happens to collide under float64 rounding, must be reported
// as a mismatch by the no-PK aggregate path.
func TestCompareColumnUnordered_DetectsLargeInt64Corruption(t *testing.T) {
	expected := []any{int64(9223372036854775807), int64(1)}
	actual := []any{int64(1), int64(9223372036854775806)} // corrupted: differs from 9223372036854775807 only above 2^53 precision

	mismatches := compareColumnUnordered(expected, actual)
	if len(mismatches) != 1 {
		t.Fatalf("compareColumnUnordered found %d mismatch(es), want exactly 1 (the large-int64 corruption): %+v", len(mismatches), mismatches)
	}
}
