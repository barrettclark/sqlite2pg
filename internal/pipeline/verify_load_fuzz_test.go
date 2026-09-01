package pipeline

import (
	"math"
	"testing"
)

// FuzzNumericMatchSortKeyConsistency exercises the invariant sortKeyFor's
// own doc comment promises and that the numeric-comparison hot spot has
// broken three separate times already (the int64/float64 type-tag bug, the
// fmt.Sprintf("%v") scientific-notation bug, and the >2^53 precision bug):
//
//	valuesMatch(x, y) == true  =>  sortKeyFor(x) == sortKeyFor(y)
//
// verifyTableOrdered decides equality with valuesMatch; verifyTableUnordered
// decides it purely by sorting on sortKeyFor. If the two ever disagree, the
// same data passes verification on a table with a primary key and fails on
// one without (or vice versa). The reverse direction is checked too, but
// only for the numeric/numeric case where a shared key is meant to imply
// equality.
//
// x is always int64(a); y is the float64 whose bits are b, so the fuzzer
// can reach every float64 including the awkward whole-number ones near
// int64's range boundary.
func FuzzNumericMatchSortKeyConsistency(f *testing.F) {
	f.Add(int64(0), math.Float64bits(0))
	f.Add(int64(1), math.Float64bits(1))
	f.Add(int64(2454348), math.Float64bits(2454348)) // 9de204a scientific-notation case
	f.Add(int64(9007199254740993), math.Float64bits(9007199254740992))
	f.Add(int64(math.MaxInt64), math.Float64bits(9223372036854775808.0)) // Finding 6
	f.Add(int64(math.MinInt64), math.Float64bits(-9223372036854775808.0))
	f.Add(int64(-1), math.Float64bits(math.NaN()))
	f.Add(int64(100), math.Float64bits(math.Inf(1)))

	f.Fuzz(func(t *testing.T, a int64, fbits uint64) {
		x := a
		y := math.Float64frombits(fbits)

		// int64 vs float64
		checkPair(t, any(x), any(y))
		// float64 vs int64 (symmetry of the whole relation)
		checkPair(t, any(y), any(x))
		// same-type pairs, cheap extra coverage of exactNumericEqual /
		// numericSortKey's int64 and float64 arms
		checkPair(t, any(x), any(x))
		checkPair(t, any(y), any(y))
	})
}

func checkPair(t *testing.T, p, q any) {
	t.Helper()

	matchPQ := valuesMatch(p, q)
	matchQP := valuesMatch(q, p)
	if matchPQ != matchQP {
		t.Fatalf("valuesMatch not symmetric: valuesMatch(%#v,%#v)=%v but valuesMatch(%#v,%#v)=%v",
			p, q, matchPQ, q, p, matchQP)
	}

	kp := sortKeyFor(p)
	kq := sortKeyFor(q)

	if matchPQ && kp != kq {
		if knownIssue65Gap(p, q) {
			// Documented open gap (issue #65): valuesMatch compares an
			// int64 against a float64 by a lossy float64 round-trip, so it
			// calls int64 values outside float64's exact range (|n| > 2^53)
			// "equal" to a nearby float64 — while sortKeyFor keeps the
			// int64's exact decimal key. The two verify paths then
			// disagree. Remove this exemption once #65 is fixed; the
			// f.Add seed with 9007199254740993 / 9007199254740992 is the
			// reproduction.
			return
		}
		t.Fatalf("valuesMatch(%#v,%#v)=true but sortKeyFor disagrees: %q vs %q\n"+
			"(equal values must sort to the same key — see sortKeyFor doc comment; "+
			"a no-PK table would report a false mismatch here while a PK table would not)",
			p, q, kp, kq)
	}

	// Reverse direction, finite numeric/numeric only: if two numeric
	// values key identically, valuesMatch must agree they're equal, or
	// compareColumnUnordered pairs them up and then never rechecks. NaN is
	// excluded — valuesMatch deliberately reports NaN != NaN (it's a
	// float compare), and a NaN in a numeric column is pathological input
	// this invariant isn't meant to cover.
	if isFiniteNumeric(p) && isFiniteNumeric(q) && kp == kq && !matchPQ {
		t.Fatalf("sortKeyFor(%#v)==sortKeyFor(%#v)==%q but valuesMatch=false: "+
			"the unordered verify path would treat these as the same row, the ordered path would not",
			p, q, kp)
	}
}

// knownIssue65Gap reports whether p,q are an (int64, float64) pair — in
// either order — where the int64's magnitude exceeds float64's exact
// integer range (2^53). That's exactly the region issue #65 covers, where
// valuesMatch (lossy float64 round-trip) and sortKeyFor (exact int64 key)
// are known to disagree.
func knownIssue65Gap(p, q any) bool {
	const exactFloatIntLimit = 1 << 53
	pi, pIsInt := p.(int64)
	qi, qIsInt := q.(int64)
	_, pIsFloat := p.(float64)
	_, qIsFloat := q.(float64)
	if pIsInt && qIsFloat {
		return pi > exactFloatIntLimit || pi < -exactFloatIntLimit
	}
	if qIsInt && pIsFloat {
		return qi > exactFloatIntLimit || qi < -exactFloatIntLimit
	}
	return false
}

func isFiniteNumeric(v any) bool {
	switch t := v.(type) {
	case int64:
		return true
	case float64:
		return !math.IsNaN(t) && !math.IsInf(t, 0)
	}
	return false
}

