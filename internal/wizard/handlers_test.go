package wizard

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
}
