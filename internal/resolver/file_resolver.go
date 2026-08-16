package resolver

import (
	"context"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ErrUnresolvedCases is wrapped by FileResolver.Resolve whenever it writes
// a non-empty report; callers (the CLI) should exit non-zero and point the
// user at ReportPath.
var ErrUnresolvedCases = errors.New("unresolved cases require review")

// FileResolver is the first Resolver implementation: it writes every
// UnresolvedCase to a human-readable YAML report and returns immediately,
// resolving nothing inline. A human (or a Claude Code session) edits a
// companion resolutions file out of band; `migrate resolve --apply` merges
// it back into the config. This keeps the tool scriptable — no long-lived
// blocking process.
type FileResolver struct {
	ReportPath string
}

type unresolvedReportCase struct {
	Table        string   `yaml:"table"`
	Column       string   `yaml:"column"`
	DeclaredType string   `yaml:"declared_type"`
	Samples      []string `yaml:"samples"`
	Reason       string   `yaml:"reason"`
	Findings     []struct {
		Heuristic     string  `yaml:"heuristic"`
		SuggestedType string  `yaml:"suggested_type"`
		Confidence    float64 `yaml:"confidence"`
		Rationale     string  `yaml:"rationale"`
	} `yaml:"findings"`
}

func (fr FileResolver) Resolve(ctx context.Context, cases []UnresolvedCase) (map[string]Resolution, error) {
	if len(cases) == 0 {
		return nil, nil
	}

	report := make([]unresolvedReportCase, 0, len(cases))
	for _, c := range cases {
		rc := unresolvedReportCase{
			Table:        c.Table,
			Column:       c.Column,
			DeclaredType: c.DeclaredType,
			Reason:       c.Reason,
		}
		for _, s := range c.Samples {
			rc.Samples = append(rc.Samples, fmt.Sprintf("%v", s))
		}
		for _, f := range c.Findings {
			rc.Findings = append(rc.Findings, struct {
				Heuristic     string  `yaml:"heuristic"`
				SuggestedType string  `yaml:"suggested_type"`
				Confidence    float64 `yaml:"confidence"`
				Rationale     string  `yaml:"rationale"`
			}{
				Heuristic:     f.Heuristic,
				SuggestedType: f.SuggestedType,
				Confidence:    f.Confidence,
				Rationale:     f.Rationale,
			})
		}
		report = append(report, rc)
	}

	data, err := yaml.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("marshaling unresolved report: %w", err)
	}
	if err := os.WriteFile(fr.ReportPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing unresolved report to %s: %w", fr.ReportPath, err)
	}

	return nil, fmt.Errorf("%w: see %s", ErrUnresolvedCases, fr.ReportPath)
}
