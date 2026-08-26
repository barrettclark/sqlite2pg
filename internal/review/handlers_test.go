package review

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestHandleSummary_ReturnsReviewSummaryAsJSON(t *testing.T) {
	st, _ := newTestState(t)
	mux := NewMux(st)

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var summary ReviewSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary.NeedsReviewCount != 1 {
		t.Errorf("expected 1 needs-review column, got %d", summary.NeedsReviewCount)
	}
}

func TestHandleDecision_UpdatesAndPersistsTheConfigImmediately(t *testing.T) {
	st, path := newTestState(t)
	mux := NewMux(st)

	body, _ := json.Marshal(DecisionRequest{TargetType: "boolean", Rationale: "human confirmed"})
	req := httptest.NewRequest(http.MethodPost, "/api/columns/bikes/is_installed/decision", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
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

func TestHandleDecision_OverridingTargetTypeAlsoUpdatesTheTransform(t *testing.T) {
	// Regression: overriding a boolean-guess column (transform int_to_bool)
	// to a plain integer must not leave the stale int_to_bool transform in
	// place — that would encode a Go bool into an integer column at load
	// time and fail. Found via dogfooding against chinook.db's
	// invoice_items.Quantity, ambiguous as boolean01 but really a count.
	st, path := newTestState(t)
	mux := NewMux(st)

	body, _ := json.Marshal(DecisionRequest{TargetType: "integer", Transform: "", Rationale: "clearly a count, not a flag"})
	req := httptest.NewRequest(http.MethodPost, "/api/columns/bikes/is_installed/decision", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
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

func TestHandleFinish_MarksRemainingColumnsReviewedAndSignalsDone(t *testing.T) {
	st, path := newTestState(t)
	mux := NewMux(st)

	req := httptest.NewRequest(http.MethodPost, "/api/finish", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
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

func TestHandleCancel_SignalsDoneWithoutMarkingColumnsReviewed(t *testing.T) {
	// Cancel must abort the run without the bulk "accept everything else
	// as-is" that Finish does — a column no human looked at should stay
	// unreviewed, since the caller (`migrate run`) will not proceed to load
	// at all when cancelled.
	st, path := newTestState(t)
	mux := NewMux(st)

	req := httptest.NewRequest(http.MethodPost, "/api/cancel", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

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
