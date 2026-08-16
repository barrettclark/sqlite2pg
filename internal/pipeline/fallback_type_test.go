package pipeline

import (
	"testing"
	"time"

	"sqlite2pg/internal/profiler"
)

func TestFallbackTypeFor_PrefersTheActualRuntimeValueTypeOverDeclaredType(t *testing.T) {
	// Regression: found via dogfooding against chinook.db. A column
	// declared NUMERIC(10,2) has genuinely numeric runtime values
	// (modernc.org/sqlite returns float64 for it), so it must map to
	// double precision — not to a declared-type-only guess. The inverse
	// case (DATETIME declared, but modernc.org/sqlite returns time.Time)
	// is exactly why runtime type must win over the declared type string.
	cases := []struct {
		name     string
		declared string
		samples  []profiler.Value
		want     string
	}{
		{"numeric declared, float64 runtime", "NUMERIC(10,2)", []profiler.Value{float64(0.99), float64(1.99)}, "double precision"},
		{"datetime declared, time.Time runtime", "DATETIME", []profiler.Value{time.Now()}, "timestamptz"},
		{"unknown declared, int64 runtime", "SOME_UNKNOWN_TYPE", []profiler.Value{int64(1), int64(2)}, "integer"},
		{"unknown declared, string runtime", "BOOLEAN", []profiler.Value{"true"}, "text"},
		{"blob declared, []byte runtime", "BLOB", []profiler.Value{[]byte{0x01}}, "bytea"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := fallbackTypeFor(c.declared, c.samples)
			if got != c.want {
				t.Errorf("fallbackTypeFor(%q, %v) = %q, want %q", c.declared, c.samples, got, c.want)
			}
		})
	}
}

func TestFallbackTypeFor_FallsBackToDeclaredTypeWhenNoSamplesAreAvailable(t *testing.T) {
	// An empty table (or an all-NULL column) gives no runtime evidence —
	// fall back to a safe declared-type guess, defaulting to text for
	// anything that isn't an unambiguous INT/BLOB/REAL declared type.
	cases := map[string]string{
		"INTEGER":  "integer",
		"BLOB":     "bytea",
		"REAL":     "double precision",
		"DATETIME": "text",
		"":         "text",
	}
	for declared, want := range cases {
		if got := fallbackTypeFor(declared, nil); got != want {
			t.Errorf("fallbackTypeFor(%q, nil) = %q, want %q", declared, got, want)
		}
	}
}
