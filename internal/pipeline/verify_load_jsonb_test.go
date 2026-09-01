package pipeline

import "testing"

// TestCanonicalJSON_IndependentOfKeyOrderAndWhitespace covers the core of
// issue #61: text_to_jsonb is validate-only, so the SQLite side keeps
// whatever spelling the source row had while Postgres canonicalizes on
// storage — comparing the two raw strings reported every jsonb row as a
// mismatch. Routing both sides through canonicalJSON must collapse
// key-order, whitespace and number-format differences.
func TestCanonicalJSON_IndependentOfKeyOrderAndWhitespace(t *testing.T) {
	a := canonicalJSON(`{"type":"Point","coordinates":[1,2]}`)
	b := canonicalJSON(`{ "coordinates": [1, 2], "type": "Point" }`)
	if a != b {
		t.Errorf("canonicalJSON gave different results for the same document:\n  %s\n  %s", a, b)
	}
}

// TestCanonicalJSON_NumberFormsThatPostgresConsidersEqual: Postgres jsonb
// treats 1e3 and 1000 as the same number; canonicalJSON must too (both
// sides pass through Go's encoder, so 123.0 vs 123 also collapses).
func TestCanonicalJSON_NumberFormsThatPostgresConsidersEqual(t *testing.T) {
	if canonicalJSON(`{"a":1e3}`) != canonicalJSON(`{"a":1000}`) {
		t.Errorf("canonicalJSON(1e3)=%q != canonicalJSON(1000)=%q", canonicalJSON(`{"a":1e3}`), canonicalJSON(`{"a":1000}`))
	}
	if canonicalJSON(`{"a":123.0}`) != canonicalJSON(`{"a":123}`) {
		t.Errorf("canonicalJSON(123.0)=%q != canonicalJSON(123)=%q", canonicalJSON(`{"a":123.0}`), canonicalJSON(`{"a":123}`))
	}
}

// TestCanonicalJSON_DistinctDocumentsStayDistinct is the guard: genuinely
// different JSON must not collapse to the same canonical form.
func TestCanonicalJSON_DistinctDocumentsStayDistinct(t *testing.T) {
	if canonicalJSON(`{"a":1}`) == canonicalJSON(`{"a":2}`) {
		t.Error(`canonicalJSON collapsed {"a":1} and {"a":2}`)
	}
}

// TestCanonicalJSON_LargeIntegersCompareExactly is the Copilot PR #72
// finding: numbers must not round-trip through float64, or a jsonb
// document holding a large integer ID/counter (> 2^53) can collapse with a
// neighbouring value and verify misses a real corruption inside the JSON.
func TestCanonicalJSON_LargeIntegersCompareExactly(t *testing.T) {
	a := canonicalJSON(`{"id":9007199254740993}`) // 2^53 + 1
	b := canonicalJSON(`{"id":9007199254740992}`) // 2^53
	c := canonicalJSON(`{"id":9007199254740994}`) // 2^53 + 2
	if a == b {
		t.Errorf("canonicalJSON collapsed 2^53+1 and 2^53: %q", a)
	}
	if a == c {
		t.Errorf("canonicalJSON collapsed 2^53+1 and 2^53+2: %q", a)
	}
	// An exact 20-digit integer must survive intact.
	x := canonicalJSON(`{"id":12345678901234567890}`)
	y := canonicalJSON(`{"id":12345678901234567891}`)
	if x == y {
		t.Errorf("canonicalJSON collapsed two distinct 20-digit integers: %q", x)
	}
}

// TestCanonicalJSON_NonJSONPassesThrough: a value that isn't valid JSON is
// returned unchanged (defensive — a validated jsonb load never produces
// one, but canonicalJSON must not eat it).
func TestCanonicalJSON_NonJSONPassesThrough(t *testing.T) {
	if got := canonicalJSON("not json"); got != "not json" {
		t.Errorf("canonicalJSON(%q) = %q, want it unchanged", "not json", got)
	}
}
