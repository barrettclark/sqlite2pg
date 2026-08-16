package resolver

import (
	"context"

	"sqlite2pg/internal/profiler"
)

// UnresolvedCase is a column the deterministic profiler + Decide couldn't
// confidently resolve on its own.
type UnresolvedCase struct {
	Table        string
	Column       string
	DeclaredType string
	Samples      []profiler.Value
	Findings     []profiler.Finding
	Reason       string
}

// Resolution is the outcome of escalating an UnresolvedCase, regardless of
// which Resolver implementation produced it.
type Resolution struct {
	Type       string
	Rationale  string
	Confidence float64
	Source     string // "human" now; "llm:<model>" reserved for a future resolver
}

// Resolver escalates UnresolvedCases to get a human (or, in a future
// implementation, an LLM) decision. The ctx parameter exists so a future
// API-calling resolver can slot into the same call site as FileResolver
// without any change to callers.
type Resolver interface {
	// Resolve returns a map keyed by "table.column" for every case it could
	// resolve. FileResolver resolves none inline — it writes a report and
	// returns ErrUnresolvedCases, expecting a human to supply resolutions
	// out of band via a separate `resolve --apply` step.
	Resolve(ctx context.Context, cases []UnresolvedCase) (map[string]Resolution, error)
}
