package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestGeoJSON_DetectsGeoJSONText(t *testing.T) {
	h := GeoJSON{}
	meta := profiler.ColumnMeta{Table: "austinroadconstruction", Name: "geometry", DeclaredType: "TEXT"}
	if !h.AppliesTo(meta) {
		t.Fatal("expected AppliesTo to return true for a TEXT column")
	}

	samples := []profiler.Value{
		`{"type":"Point","coordinates":[-97.74,30.27]}`,
		`{"type":"LineString","coordinates":[[-97.74,30.27],[-97.75,30.28]]}`,
	}
	finding, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if !ok {
		t.Fatal("expected a finding for GeoJSON text")
	}
	if finding.SuggestedType != "jsonb" {
		t.Errorf("expected suggested type jsonb, got %q", finding.SuggestedType)
	}
}

func TestGeoJSON_NoOpinionOnPlainJSONWithoutGeoKeys(t *testing.T) {
	h := GeoJSON{}
	samples := []profiler.Value{`{"name":"foo","value":1}`}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for JSON that lacks type/coordinates keys")
	}
}

func TestGeoJSON_NoOpinionOnNonJSONText(t *testing.T) {
	h := GeoJSON{}
	samples := []profiler.Value{"just some text"}
	_, ok := h.Evaluate(profiler.ColumnMeta{}, samples)
	if ok {
		t.Fatal("expected no opinion for non-JSON text")
	}
}
