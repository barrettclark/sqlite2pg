package heuristics

import (
	"testing"

	"sqlite2pg/internal/profiler"
)

func TestInit_RegistersAllHeuristicsWithTheDefaultRegistry(t *testing.T) {
	// This package's init() functions self-register into profiler.Default
	// on import, which is how the CLI is meant to pick them all up without
	// any explicit wiring per heuristic.
	meta := profiler.ColumnMeta{Table: "t", Name: "c", DeclaredType: "int32"}
	findings := profiler.Default.ProfileColumn(meta, nil)
	if len(findings) == 0 {
		t.Fatal("expected at least one heuristic (esri_typename_mapping) to have self-registered and fired for an int32 column")
	}
}
