package review

import (
	"path/filepath"
	"testing"

	"sqlite2pg/internal/config"
)

func newTestState(t *testing.T) (*State, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.migration.yaml")
	cfg := sampleConfig()
	if err := config.Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := NewState(path, 0.9)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return st, path
}

func TestApplyDecision_UpdatesAndPersistsTheConfigImmediately(t *testing.T) {
	st, path := newTestState(t)

	err := st.ApplyDecision("bikes", "is_installed", DecisionRequest{TargetType: "boolean", Rationale: "human confirmed"})
	if err != nil {
		t.Fatalf("ApplyDecision: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["is_installed"]
	if !col.Reviewed {
		t.Error("expected the column to be marked reviewed after a decision")
	}
	if col.Source != "human_override" {
		t.Errorf("expected source human_override, got %q", col.Source)
	}
	if col.OriginalSuggestion == nil || col.OriginalSuggestion.Source != "heuristic:boolean01" {
		t.Errorf("expected the prior heuristic suggestion to be preserved, got %+v", col.OriginalSuggestion)
	}
}

func TestApplyDecision_OverridingTargetTypeAlsoUpdatesTheTransform(t *testing.T) {
	// Regression: overriding a boolean-guess column (transform int_to_bool)
	// to a plain integer must not leave the stale int_to_bool transform in
	// place.
	st, path := newTestState(t)

	err := st.ApplyDecision("bikes", "is_installed", DecisionRequest{TargetType: "integer", Transform: "", Rationale: "clearly a count, not a flag"})
	if err != nil {
		t.Fatalf("ApplyDecision: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["bikes"].Columns["is_installed"]
	if col.Transform != "" {
		t.Errorf("expected the stale int_to_bool transform to be cleared, got %q", col.Transform)
	}
}

func TestFinish_MarksRemainingColumnsReviewedAndSignalsDone(t *testing.T) {
	st, path := newTestState(t)

	if err := st.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for tableName, tc := range loaded.Tables {
		for colName, col := range tc.Columns {
			if !col.Reviewed {
				t.Errorf("expected %s.%s to be reviewed after Finish, got reviewed=false", tableName, colName)
			}
		}
	}

	select {
	case <-st.Done():
	default:
		t.Error("expected Finish to signal Done()")
	}

	if st.Outcome() != OutcomeConfirmed {
		t.Errorf("expected outcome OutcomeConfirmed after Finish, got %v", st.Outcome())
	}
}

func TestCancel_SignalsDoneWithoutMarkingColumnsReviewed(t *testing.T) {
	// Cancel must abort the run without the bulk "accept everything else
	// as-is" that Finish does.
	st, path := newTestState(t)

	st.Cancel()

	select {
	case <-st.Done():
	default:
		t.Fatal("expected Cancel to signal Done()")
	}
	if st.Outcome() != OutcomeCancelled {
		t.Errorf("expected outcome OutcomeCancelled after Cancel, got %v", st.Outcome())
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Tables["bikes"].Columns["is_installed"].Reviewed {
		t.Error("expected Cancel to leave an untouched column reviewed=false")
	}
}
