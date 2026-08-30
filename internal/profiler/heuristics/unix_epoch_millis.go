package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// plausible Unix epoch millisecond range: the same 2000-01-01 through
// 2035-01-01 window UnixEpoch uses for seconds, scaled x1000 — three
// orders of magnitude away from the seconds range, so a genuine
// epoch-seconds value can never be mistaken for milliseconds.
const (
	epochMillisMin = epochMin * 1000
	epochMillisMax = epochMax * 1000
)

// UnixEpochMillis detects integer columns storing Unix epoch timestamps in
// milliseconds (common from JS-originated data, e.g. Date.now()) —
// identified the same way UnixEpoch identifies epoch-seconds columns: by
// column name and by most sampled values falling in a plausible
// millisecond-scale date range.
type UnixEpochMillis struct{}

func (UnixEpochMillis) Name() string { return "unix_epoch_millis" }

func (UnixEpochMillis) AppliesTo(meta profiler.ColumnMeta) bool {
	if !strings.Contains(strings.ToUpper(meta.DeclaredType), "INT") {
		return false
	}
	return epochNamePattern.MatchString(meta.Name)
}

func (UnixEpochMillis) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, plausible int
	for _, v := range samples {
		i, ok := asInt64(v)
		if !ok {
			continue
		}
		total++
		if i >= epochMillisMin && i <= epochMillisMax {
			plausible++
		}
	}
	if total == 0 || float64(plausible)/float64(total) < 0.5 {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.85,
		Rationale:     "column name matches a timestamp-like pattern and most sampled values fall in a plausible epoch-milliseconds range",
		TransformExpr: "unix_epoch_millis",
	}, true
}

func init() { profiler.Register(UnixEpochMillis{}) }
