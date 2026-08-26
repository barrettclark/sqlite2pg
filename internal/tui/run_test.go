package tui

import (
	"context"

	"sqlite2pg/internal/review"
)

// runSignature is a compile-time check that Run's signature stays
// Run(ctx, st) error — cmd/migrate/main.go depends on it exactly. Running
// Run itself requires a real terminal, so it's exercised via the manual
// smoke test in the plan instead of an automated test.
var _ func(context.Context, *review.State) error = Run
