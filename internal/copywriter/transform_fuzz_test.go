package copywriter

import (
	"math"
	"math/big"
	"strings"
	"testing"
	"time"

	"sqlite2pg/internal/profiler"
)

// FuzzNumericTextToInteger targets issue #15's precision-loss class: the
// old implementation ran the text through strconv.ParseFloat and cast to
// int64, silently corrupting any value past float64's ~15-17 significant
// digits and saturating on overflow with no error. The property: whenever
// numeric_text_to_integer / parseWholeNumberText returns a value with no
// error, that int64 must be the EXACT integer the input text denotes —
// verified here against math/big, which cannot lose precision.
func FuzzNumericTextToInteger(f *testing.F) {
	f.Add("0")
	f.Add("42")
	f.Add("-1")
	f.Add("9223372036854775807")   // MaxInt64
	f.Add("-9223372036854775808")  // MinInt64
	f.Add("9223372036854775808")   // MaxInt64+1, must error not saturate
	f.Add("1234567890123456789")   // bikes.db.legacy_id shape, 19 digits
	f.Add("12345678901234567890")  // 20 digits, overflows int64
	f.Add("1998.0")                // trailing ".0" is accepted (whole number)
	f.Add("1998.5")                // non-zero fraction must error
	f.Add("  5  ")
	f.Add("0x10")
	f.Add("1e3")

	f.Fuzz(func(t *testing.T, s string) {
		got, err := parseWholeNumberText(s) // must not panic
		if err != nil {
			return // rejected — nothing to check
		}

		// It accepted s. Reconstruct the exact integer s denotes and
		// require an exact match. parseWholeNumberText trims a
		// zeros-only fractional part, so mirror that here.
		intPart := s
		if i := strings.IndexByte(s, '.'); i >= 0 {
			intPart = s[:i]
		}
		want, ok := new(big.Int).SetString(intPart, 10)
		if !ok {
			t.Fatalf("parseWholeNumberText(%q) returned %d with nil error, but %q is not a base-10 integer",
				s, got, intPart)
		}
		if want.Cmp(big.NewInt(got)) != 0 {
			t.Fatalf("parseWholeNumberText(%q) = %d, want %s — precision lost or wrong value", s, got, want)
		}

		// And the round-trip through the public Transform entrypoint
		// must agree (string input path).
		tv, terr := Transform("numeric_text_to_integer", profiler.Value(s))
		if terr != nil {
			t.Fatalf("parseWholeNumberText(%q) succeeded (%d) but Transform(numeric_text_to_integer, ...) failed: %v", s, got, terr)
		}
		if tv != any(got) {
			t.Fatalf("Transform(numeric_text_to_integer, %q) = %#v, want int64(%d)", s, tv, got)
		}
	})
}

// FuzzFitsRangeMatchesStrconv checks FitsRange against the source of truth
// for each integer target's real range — a value FitsRange says fits
// "smallint"/"integer" must actually round-trip through int16/int32, and
// one it rejects must not. FitsRange gates both the TUI picker (issue #27)
// and verify's full-table range check (issue #15), so an off-by-one here
// is a wrong type promised to the user or a missed overflow.
func FuzzFitsRangeMatchesStrconv(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(math.MaxInt16))
	f.Add(int64(math.MaxInt16) + 1)
	f.Add(int64(math.MinInt16))
	f.Add(int64(math.MaxInt32))
	f.Add(int64(math.MaxInt32) + 1)
	f.Add(int64(math.MinInt64))

	f.Fuzz(func(t *testing.T, n int64) {
		if got, want := FitsRange(n, "smallint"), n >= math.MinInt16 && n <= math.MaxInt16; got != want {
			t.Fatalf("FitsRange(%d, smallint) = %v, want %v", n, got, want)
		}
		if got, want := FitsRange(n, "integer"), n >= math.MinInt32 && n <= math.MaxInt32; got != want {
			t.Fatalf("FitsRange(%d, integer) = %v, want %v", n, got, want)
		}
		if !FitsRange(n, "bigint") {
			t.Fatalf("FitsRange(%d, bigint) = false, every int64 fits bigint", n)
		}
	})
}

// FuzzIso8601ToDate checks the two invariants that matter for the
// date-vs-timestamptz work (issues #14/#42): the transform never panics on
// arbitrary text, and when it DOES accept an input it returns a real
// midnight-UTC time.Time whose date re-parses to the same day. Issue #42
// made a non-midnight time-of-day an error rather than a silent truncation
// — so any accepted value must genuinely be at 00:00:00.
func FuzzIso8601ToDate(f *testing.F) {
	f.Add("2021-06-01")
	f.Add("2021-06-01T00:00:00Z")
	f.Add("2021-06-01T13:45:00Z") // non-midnight -> must error
	f.Add("2021-06-01 00:00:00")
	f.Add("not a date")
	f.Add("0000-00-00")
	f.Add("2021-06-01T00:00:00.000000001Z")

	f.Fuzz(func(t *testing.T, s string) {
		got, err := Transform("iso8601_to_date", profiler.Value(s)) // must not panic
		if err != nil {
			return
		}
		tm, ok := got.(time.Time)
		if !ok {
			t.Fatalf("Transform(iso8601_to_date, %q) returned %T, want time.Time", s, got)
		}
		h, m, sec := tm.Clock()
		if h != 0 || m != 0 || sec != 0 || tm.Nanosecond() != 0 {
			t.Fatalf("Transform(iso8601_to_date, %q) = %s — accepted a value with a non-midnight time-of-day (issue #42)", s, tm.Format(time.RFC3339Nano))
		}
		// The date must survive a format/reparse round-trip.
		day := tm.Format("2006-01-02")
		if _, perr := time.Parse("2006-01-02", day); perr != nil {
			t.Fatalf("Transform(iso8601_to_date, %q) = %s, whose date %q does not re-parse: %v", s, tm, day, perr)
		}
	})
}

// FuzzEpochScaleTransforms exercises the three unix-epoch transforms and
// excel_serial_to_timestamptz over the full int64 range: they must never
// panic and must return a time.Time (or a typed error), never a bare
// non-time value, since verify re-runs the identical transform and
// compares time.Time instants.
func FuzzEpochScaleTransforms(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(1_700_000_000))
	f.Add(int64(1_700_000_000_000))
	f.Add(int64(1_700_000_000_000_000))
	f.Add(int64(math.MaxInt64))
	f.Add(int64(math.MinInt64))

	names := []string{
		"unix_epoch_seconds",
		"unix_epoch_millis",
		"unix_epoch_micros",
		"excel_serial_to_timestamptz",
	}

	f.Fuzz(func(t *testing.T, n int64) {
		for _, name := range names {
			got, err := Transform(name, profiler.Value(n)) // must not panic
			if err != nil {
				continue
			}
			if _, ok := got.(time.Time); !ok {
				t.Fatalf("Transform(%s, %d) returned %T (%v), want time.Time", name, n, got, got)
			}
		}
		// A float-shaped Excel serial too.
		fv := float64(n) / 1000.0
		if got, err := Transform("excel_serial_to_timestamptz", profiler.Value(fv)); err == nil {
			if _, ok := got.(time.Time); !ok {
				t.Fatalf("Transform(excel_serial_to_timestamptz, %v) returned %T, want time.Time", fv, got)
			}
		}
	})
}

// FuzzTransformArmsNeverSilentlyPassThrough guards issue #103's specific
// reachable failure: PR #97/#98 rewrote five string-only transform arms to
// error on an unexpected type instead of `return raw, nil`; four siblings
// (iso8601_to_timestamptz, dayfirst_to_timestamptz,
// numeric_text_to_integer, numeric_text_to_double) were left with the
// fall-through and are fixed in this batch. Across all nine string-ish
// arms this asserts: (1) no arm ever hands a raw []byte straight back —
// that is the storage class the paired heuristics tolerate with
// `continue`, so it is the one that actually reaches these arms and makes
// verifyTransformAgainstFullTable a silent no-op; and (2) no arm panics on
// any []byte / int64 / float64 input. It does NOT constrain the int64 /
// float64 results: strip_commas, numeric_text_to_double and others
// legitimately accept an already-numeric value and return it (or a
// same-typed conversion) unchanged.
func FuzzTransformArmsNeverSilentlyPassThrough(f *testing.F) {
	f.Add([]byte("2019-03-04"), int64(42), 3.5)
	f.Add([]byte{0x00, 0xff}, int64(0), 0.0)
	f.Add([]byte("31/07/2006"), int64(-9e18), 1e300)

	arms := []string{
		"iso8601_to_timestamptz",
		"dayfirst_to_timestamptz",
		"numeric_text_to_integer",
		"numeric_text_to_double",
		"iso8601_to_date",
		"strip_commas",
		"strip_commas_float",
		"text_to_jsonb",
		"nullif_sentinels",
	}

	check := func(t *testing.T, arm string, in profiler.Value) {
		got, err := Transform(arm, in) // must not panic
		if err != nil {
			return
		}
		// Accepted with no error. The result must not be the raw input
		// passed straight back.
		switch in.(type) {
		case []byte:
			if b, ok := got.([]byte); ok {
				t.Fatalf("Transform(%s, %T) returned the raw []byte unchanged (%q) — silent pass-through (issue #103)", arm, in, b)
			}
		}
	}

	f.Fuzz(func(t *testing.T, b []byte, n int64, fl float64) {
		for _, arm := range arms {
			check(t, arm, profiler.Value(b))
			check(t, arm, profiler.Value(n))
			check(t, arm, profiler.Value(fl))
		}
	})
}
