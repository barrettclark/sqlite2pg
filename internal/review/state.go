package review

import (
	"fmt"
	"sync"

	"sqlite2pg/internal/config"
)

// DecisionRequest is one column's reviewed decision. Transform must be set
// explicitly alongside TargetType (empty means passthrough) — a stale
// transform from the prior heuristic guess (e.g. int_to_bool) is never
// implicitly carried over to a new target type.
type DecisionRequest struct {
	TargetType string `json:"target_type"`
	Transform  string `json:"transform"`
	Rationale  string `json:"rationale"`
}

// Outcome is how a review session ended.
type Outcome int

const (
	// OutcomePending means the session hasn't ended yet.
	OutcomePending Outcome = iota
	// OutcomeConfirmed means the human clicked "Finish Review" / "Confirm
	// & Import" — callers should proceed (e.g. `migrate run` continues to
	// the load step).
	OutcomeConfirmed
	// OutcomeCancelled means the human clicked "Cancel" — callers must not
	// proceed to load. Any per-column edits already applied remain saved
	// (each decision persists immediately), but no bulk "accept the rest"
	// happens.
	OutcomeCancelled
)

// State holds the in-progress review session: the config being reviewed,
// where it's persisted, and the signal that fires once the human clicks
// "Finish Review"/"Confirm & Import" or "Cancel" — the mechanism
// `migrate review` and `migrate run` use to unblock and return control to
// the CLI.
type State struct {
	mu        sync.Mutex
	path      string
	cfg       *config.MigrationConfig
	threshold float64
	done      chan struct{}
	doneOnce  sync.Once
	outcome   Outcome
	grid      GridData
}

// gridPreviewLimit is how many preview rows are sampled per table — enough
// to fill a scrollable terminal grid without a slow/large query.
const gridPreviewLimit = 30

// NewState loads the config at path for review, and best-effort re-samples
// the source file so the preview screen can show real data alongside each
// column's decision.
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
		grid:      sampleGridData(cfg, gridPreviewLimit),
	}, nil
}

// Done is closed once the review session ends, however it ended — check
// Outcome() to see which.
func (s *State) Done() <-chan struct{} {
	return s.done
}

// Outcome reports how the session ended. Only meaningful after Done() has
// fired; returns OutcomePending before that.
func (s *State) Outcome() Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outcome
}

// Summary returns the current ReviewSummary for the config as it stands.
func (s *State) Summary() ReviewSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BuildReviewSummary(s.cfg, s.threshold, s.grid)
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
	col.Transform = req.Transform
	col.Rationale = req.Rationale
	col.Source = "human_override"
	col.Reviewed = true
	// col.NeedsReview is deliberately left untouched (issue #53): it's a
	// permanently-stable profiler verdict, not a to-do flag cleared by
	// resolution — see internal/tui/logic.go's flaggedColumns comment and
	// cmd/migrate/main.go's runResolve, which share this same contract.

	tc.Columns[column] = col
	s.cfg.Tables[table] = tc

	return config.Save(s.cfg, s.path)
}

// Finish marks every remaining unreviewed column reviewed (the bulk
// "accept everything else as-is" action), persists, records
// OutcomeConfirmed, and signals Done.
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

	s.doneOnce.Do(func() {
		s.outcome = OutcomeConfirmed
		close(s.done)
	})
	return nil
}

// Cancel aborts the session: records OutcomeCancelled and signals Done,
// without the bulk "accept everything else" Finish does. Callers (notably
// `migrate run`) must not proceed to load when Outcome() is
// OutcomeCancelled.
func (s *State) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.doneOnce.Do(func() {
		s.outcome = OutcomeCancelled
		close(s.done)
	})
}
