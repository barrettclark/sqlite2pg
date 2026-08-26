package review

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticRoutes_ServeTheEmbeddedIndexAndAppJS(t *testing.T) {
	st, _ := newTestState(t)
	mux := NewMux(st)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Review column mappings") {
		t.Errorf("expected index.html content, got:\n%s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.js: expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "loadSummary") {
		t.Errorf("expected app.js content, got:\n%s", rec.Body.String())
	}
}
