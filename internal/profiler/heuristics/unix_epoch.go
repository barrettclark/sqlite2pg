package heuristics

import (
	"regexp"
	"strings"

	"sqlite2pg/internal/profiler"
)

var epochNamePattern = regexp.MustCompile(`(?i)(_at|_time|_date)$|^last_`)

// plausible Unix epoch second range: 2000-01-01 through 2035-01-01.
const (
	epochMin = 946684800
	epochMax = 2051222400
)

// UnixEpoch detects integer columns storing Unix epoch timestamps in
// seconds, identified by column name and by most sampled values falling in
// a plausible date range. A minority of out-of-range values (e.g. a stray
// 86400 representing 1970-01-02) is tolerated rather than disqualifying.
type UnixEpoch struct{}

func (UnixEpoch) Name() string { return "unix_epoch_seconds" }

func (UnixEpoch) AppliesTo(meta profiler.ColumnMeta) bool {
	if !strings.Contains(strings.ToUpper(meta.DeclaredType), "INT") {
		return false
	}
	return epochNamePattern.MatchString(meta.Name)
}

func (UnixEpoch) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, plausible int
	for _, v := range samples {
		i, ok := asInt64(v)
		if !ok {
			continue
		}
		total++
		if i >= epochMin && i <= epochMax {
			plausible++
		}
	}
	if total == 0 || float64(plausible)/float64(total) < 0.5 {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.85,
		Rationale:     "column name matches a timestamp-like pattern and most sampled values fall in a plausible epoch-seconds range",
		TransformExpr: "unix_epoch_seconds",
	}, true
}

func asInt64(v profiler.Value) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func init() { profiler.Register(UnixEpoch{}) }
