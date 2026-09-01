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

// TestCanonicalJSON_NonJSONPassesThrough: a value that isn't valid JSON is
// returned unchanged (defensive — a validated jsonb load never produces
// one, but canonicalJSON must not eat it).
func TestCanonicalJSON_NonJSONPassesThrough(t *testing.T) {
	if got := canonicalJSON("not json"); got != "not json" {
		t.Errorf("canonicalJSON(%q) = %q, want it unchanged", "not json", got)
	}
}
