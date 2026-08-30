package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// plausible Unix epoch microsecond range: the same 2000-01-01 through
// 2035-01-01 window UnixEpoch uses for seconds, scaled x1,000,000 — six
// orders of magnitude away from the seconds range and three away from the
// milliseconds range, so none of the three can be mistaken for each other.
const (
	epochMicrosMin = epochMin * 1000000
	epochMicrosMax = epochMax * 1000000
)

// UnixEpochMicros detects integer columns storing Unix epoch timestamps in
// microseconds — identified the same way UnixEpoch identifies
// epoch-seconds columns: by column name and by most sampled values
// falling in a plausible microsecond-scale date range.
type UnixEpochMicros struct{}

func (UnixEpochMicros) Name() string { return "unix_epoch_micros" }

func (UnixEpochMicros) AppliesTo(meta profiler.ColumnMeta) bool {
	if !strings.Contains(strings.ToUpper(meta.DeclaredType), "INT") {
		return false
	}
	return epochNamePattern.MatchString(meta.Name)
}

func (UnixEpochMicros) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, plausible int
	for _, v := range samples {
		i, ok := asInt64(v)
		if !ok {
			continue
		}
		total++
		if i >= epochMicrosMin && i <= epochMicrosMax {
			plausible++
		}
	}
	if total == 0 || float64(plausible)/float64(total) < 0.5 {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.85,
		Rationale:     "column name matches a timestamp-like pattern and most sampled values fall in a plausible epoch-microseconds range",
		TransformExpr: "unix_epoch_micros",
	}, true
}

func init() { profiler.Register(UnixEpochMicros{}) }
