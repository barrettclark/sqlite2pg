package profiler

import "testing"

type fakeHeuristic struct {
	name       string
	applies    bool
	finding    Finding
	hasOpinion bool
}

func (f fakeHeuristic) Name() string                   { return f.name }
func (f fakeHeuristic) AppliesTo(meta ColumnMeta) bool { return f.applies }
func (f fakeHeuristic) Evaluate(meta ColumnMeta, samples []Value) (Finding, bool) {
	return f.finding, f.hasOpinion
}

func TestProfileColumn_CollectsFindingsFromApplicableHeuristicsWithOpinions(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeHeuristic{
		name: "always_boolean", applies: true, hasOpinion: true,
		finding: Finding{SuggestedType: "boolean", Confidence: 0.7},
	})
	r.Register(fakeHeuristic{
		name: "does_not_apply", applies: false, hasOpinion: true,
		finding: Finding{SuggestedType: "text", Confidence: 0.9},
	})
	r.Register(fakeHeuristic{
		name: "applies_but_no_opinion", applies: true, hasOpinion: false,
		finding: Finding{SuggestedType: "integer", Confidence: 0.5},
	})

	meta := ColumnMeta{Table: "bikes", Name: "is_installed", DeclaredType: "INTEGER"}
	findings := r.ProfileColumn(meta, []Value{int64(0), int64(1), int64(1)})

	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].SuggestedType != "boolean" {
		t.Errorf("expected suggested type boolean, got %q", findings[0].SuggestedType)
	}
	if findings[0].Heuristic != "always_boolean" {
		t.Errorf("expected Heuristic field set to source heuristic name, got %q", findings[0].Heuristic)
	}
}
