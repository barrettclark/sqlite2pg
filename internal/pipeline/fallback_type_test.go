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

func TestFallbackTypeFor_PrefersDoublePrecisionWhenSamplesMixIntAndFloatStorageClasses(t *testing.T) {
	// Regression: found via dogfooding against northwind_small.sqlite.
	// SQLite's dynamic typing lets a single DECIMAL-declared column store
	// some rows as INTEGER storage class (18) and others as REAL (21.35).
	// Looking only at the first sample previously locked the column to
	// "integer" whenever a whole-number row happened to come first,
	// silently truncating any later fractional value at COPY time.
	samples := []profiler.Value{int64(18), int64(19), int64(10), float64(21.35), int64(25)}
	got := fallbackTypeFor("DECIMAL", samples)
	if got != "double precision" {
		t.Errorf("fallbackTypeFor(DECIMAL, mixed int/float samples) = %q, want double precision", got)
	}
}

func TestFallbackTypeFor_UsesBigintWhenAnIntegerSampleExceedsInt4Range(t *testing.T) {
	// Regression: found via dogfooding against sample-types.sqlite.
	// SQLite INTEGER columns hold the full 8-byte int64 range, but
	// Postgres "integer" is only 4 bytes (±2147483647). A value like
	// -9007199254740992 (well within int64, far outside int4) previously
	// mapped straight to "integer" and failed at COPY time with "less than
	// minimum value for int4".
	samples := []profiler.Value{int64(42), int64(-9007199254740992), int64(0)}
	got := fallbackTypeFor("INTEGER", samples)
	if got != "bigint" {
		t.Errorf("fallbackTypeFor(INTEGER, out-of-int4-range samples) = %q, want bigint", got)
	}
}

func TestFallbackTypeFor_UsesIntegerWhenAllValuesFitInInt4Range(t *testing.T) {
	samples := []profiler.Value{int64(42), int64(-100), int64(2147483647)}
	got := fallbackTypeFor("INTEGER", samples)
	if got != "integer" {
		t.Errorf("fallbackTypeFor(INTEGER, in-range samples) = %q, want integer", got)
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
