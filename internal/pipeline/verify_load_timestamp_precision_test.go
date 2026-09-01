package pipeline

import (
	"testing"
	"time"
)

// TestValuesMatch_TimestampSubMicrosecondMatchesPostgres covers issue #63:
// excel_serial_to_timestamptz (and iso8601_to_timestamptz on RFC3339Nano
// input) produces a time.Time with nanosecond precision, but Postgres
// timestamptz has only microsecond resolution and ROUNDS on input. verify
// re-runs the identical transform, so it compares the nanosecond value
// against Postgres's rounded-to-µs value and reports one mismatch per row
// on a load that is as correct as timestamptz allows. valuesMatch must
// compare both sides at microsecond resolution.
func TestValuesMatch_TimestampSubMicrosecondMatchesPostgres(t *testing.T) {
	// transform output (what verify recomputes)
	transformed := time.Date(2023, 3, 15, 10, 57, 38, 880000212, time.UTC)
	// what Postgres stored and hands back: same instant rounded to µs
	stored := transformed.Round(time.Microsecond)

	if !valuesMatch(transformed, stored) {
		t.Errorf("valuesMatch(%s, %s) = false, want true — they differ only below Postgres's microsecond resolution",
			transformed.Format(time.RFC3339Nano), stored.Format(time.RFC3339Nano))
	}
	if !valuesMatch(stored, transformed) {
		t.Errorf("valuesMatch not symmetric for sub-µs timestamps")
	}

	// A case that rounds UP across a µs boundary (Postgres rounds, not truncates).
	up := time.Date(2020, 1, 1, 2, 57, 46, 598400800, time.UTC)
	if !valuesMatch(up, up.Round(time.Microsecond)) {
		t.Errorf("valuesMatch(%s, %s) = false, want true (rounds up to .598401)",
			up.Format(time.RFC3339Nano), up.Round(time.Microsecond).Format(time.RFC3339Nano))
	}
}

// TestValuesMatch_TimestampMicrosecondDifferenceStillMismatch guards
// against the fix becoming too permissive: two timestamps a full
// microsecond (or more) apart are a genuine difference and must still be
// reported.
func TestValuesMatch_TimestampMicrosecondDifferenceStillMismatch(t *testing.T) {
	base := time.Date(2023, 3, 15, 10, 57, 38, 880000000, time.UTC)
	oneMicroLater := base.Add(time.Microsecond)
	if valuesMatch(base, oneMicroLater) {
		t.Errorf("valuesMatch(%s, %s) = true, want false — a full microsecond apart is a real difference",
			base.Format(time.RFC3339Nano), oneMicroLater.Format(time.RFC3339Nano))
	}
}

// TestSortKeyFor_TimestampSubMicrosecondSharesKey is sortKeyFor's half of
// the same invariant: two timestamps valuesMatch treats as equal (equal
// after µs rounding) must produce the same sort key, or the no-PK verify
// path reports a mismatch the PK path would not.
func TestSortKeyFor_TimestampSubMicrosecondSharesKey(t *testing.T) {
	transformed := time.Date(2023, 3, 15, 10, 57, 38, 880000212, time.UTC)
	stored := transformed.Round(time.Microsecond)

	if k1, k2 := sortKeyFor(transformed), sortKeyFor(stored); k1 != k2 {
		t.Errorf("sortKeyFor(%s) = %q, sortKeyFor(%s) = %q; want equal keys",
			transformed.Format(time.RFC3339Nano), k1, stored.Format(time.RFC3339Nano), k2)
	}
}

// TestSortKeyFor_TimestampMicrosecondApartDistinctKeys keeps the guard:
// genuinely different timestamps must still key apart.
func TestSortKeyFor_TimestampMicrosecondApartDistinctKeys(t *testing.T) {
	base := time.Date(2023, 3, 15, 10, 57, 38, 880000000, time.UTC)
	later := base.Add(time.Microsecond)
	if sortKeyFor(base) == sortKeyFor(later) {
		t.Errorf("sortKeyFor gave two timestamps a microsecond apart the same key %q", sortKeyFor(base))
	}
}
