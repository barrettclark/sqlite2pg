package wizard

import (
	"fmt"
	"sync"

	"sqlite2pg/internal/config"
)

// State holds the in-progress review session: the config being reviewed,
// where it's persisted, and the signal that fires once the human clicks
// "Finish Review" — the mechanism `migrate review` uses to unblock and
// return control to the CLI.
type State struct {
	mu        sync.Mutex
	path      string
	cfg       *config.MigrationConfig
	threshold float64
	done      chan struct{}
	doneOnce  sync.Once
}

// NewState loads the config at path for review.
func NewState(path string, threshold float64) (*State, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return &State{
		path:      path,
		cfg:       cfg,
		threshold: threshold,
		done:      make(chan struct{}),
	}, nil
}

// Done is closed once the review session is finished (via /api/finish).
func (s *State) Done() <-chan struct{} {
	return s.done
}

// Summary returns the current ReviewSummary for the config as it stands.
func (s *State) Summary() ReviewSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BuildReviewSummary(s.cfg, s.threshold)
}

// ApplyDecision records a human override for one column and persists the
// config to disk immediately, so a closed browser tab never loses progress.
// The heuristic's original suggestion is preserved in OriginalSuggestion.
func (s *State) ApplyDecision(table, column string, req DecisionRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tc, ok := s.cfg.Tables[table]
	if !ok {
		return fmt.Errorf("no such table %q", table)
	}
	col, ok := tc.Columns[column]
	if !ok {
		return fmt.Errorf("no such column %s.%s", table, column)
	}

	if col.OriginalSuggestion == nil {
		col.OriginalSuggestion = &config.Suggestion{
			TargetType: col.TargetType,
			Confidence: col.Confidence,
			Source:     col.Source,
		}
	}
	col.TargetType = req.TargetType
	col.Rationale = req.Rationale
	col.Source = "human_override"
	col.Reviewed = true

	tc.Columns[column] = col
	s.cfg.Tables[table] = tc

	return config.Save(s.cfg, s.path)
}

// Finish marks every remaining unreviewed column reviewed (the bulk
// "accept everything else as-is" action), persists, and signals Done.
func (s *State) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for tableName, tc := range s.cfg.Tables {
		for colName, col := range tc.Columns {
			if !col.Reviewed {
				col.Reviewed = true
				tc.Columns[colName] = col
			}
		}
		s.cfg.Tables[tableName] = tc
	}

	if err := config.Save(s.cfg, s.path); err != nil {
		return err
	}

	s.doneOnce.Do(func() { close(s.done) })
	return nil
}
