package heuristics

import (
	"encoding/json"
	"strings"

	"sqlite2pg/internal/profiler"
)

// GeoJSON detects TEXT columns storing GeoJSON geometry as plain text.
type GeoJSON struct{}

func (GeoJSON) Name() string { return "geojson_text" }

func (GeoJSON) AppliesTo(meta profiler.ColumnMeta) bool {
	return strings.Contains(strings.ToUpper(meta.DeclaredType), "TEXT") ||
		strings.Contains(strings.ToUpper(meta.DeclaredType), "CHAR")
}

func (GeoJSON) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, matched int
	for _, v := range samples {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		total++
		if isGeoJSON(s) {
			matched++
		}
	}
	if total == 0 || matched != total {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "jsonb",
		Confidence:    0.9,
		Rationale:     `all sampled values parse as JSON with "type" and "coordinates" keys`,
		TransformExpr: "text_to_jsonb",
	}, true
}

func isGeoJSON(s string) bool {
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return false
	}
	_, hasType := doc["type"]
	_, hasCoords := doc["coordinates"]
	return hasType && hasCoords
}

func init() { profiler.Register(GeoJSON{}) }
