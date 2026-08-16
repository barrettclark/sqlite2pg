package resolver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestFileResolver_WritesUnresolvedReportAndReturnsSentinelError(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "unresolved_report.yaml")
	fr := FileResolver{ReportPath: reportPath}

	cases := []UnresolvedCase{
		{
			Table:        "bikes",
			Column:       "is_installed",
			DeclaredType: "INTEGER",
			Samples:      []profiler.Value{int64(0), int64(1)},
			Findings: []profiler.Finding{
				{Heuristic: "boolean01", SuggestedType: "boolean", Confidence: 0.55},
			},
			Reason: "confidence 0.55 below auto-approve threshold 0.90",
		},
	}

	_, err := fr.Resolve(context.Background(), cases)
	if err == nil {
		t.Fatal("expected FileResolver.Resolve to return an error signaling unresolved cases")
	}
	if !errors.Is(err, ErrUnresolvedCases) {
		t.Errorf("expected error to wrap ErrUnresolvedCases, got %v", err)
	}

	data, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		t.Fatalf("expected report file to be written: %v", readErr)
	}
	content := string(data)
	for _, want := range []string{"bikes", "is_installed", "boolean01", "0.55"} {
		if !strings.Contains(content, want) {
			t.Errorf("expected report to mention %q, got:\n%s", want, content)
		}
	}
}

func TestFileResolver_NoOpWhenNoUnresolvedCases(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "unresolved_report.yaml")
	fr := FileResolver{ReportPath: reportPath}

	resolutions, err := fr.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error for zero unresolved cases, got %v", err)
	}
	if len(resolutions) != 0 {
		t.Errorf("expected no resolutions, got %+v", resolutions)
	}
	if _, statErr := os.Stat(reportPath); statErr == nil {
		t.Error("expected no report file to be written when there are no unresolved cases")
	}
}
