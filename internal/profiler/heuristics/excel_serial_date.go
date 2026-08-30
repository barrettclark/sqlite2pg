package heuristics

import (
	"strings"

	"sqlite2pg/internal/profiler"
)

// plausible Excel/Access serial-date range: 2000-01-01 through 2035-01-01,
// expressed as days since the Excel epoch (1899-12-30) — the same
// real-world window UnixEpoch uses for epoch-seconds, just in a
// completely different numeric magnitude (10^4-10^5 vs 10^9), so the two
// never collide on the same value.
const (
	excelSerialMin = 36526
	excelSerialMax = 49310
)

// ExcelSerialDate detects INTEGER/REAL columns storing Excel/Access serial
// date numbers (days since 1899-12-30, with an optional fractional part
// for time-of-day) — a common shape for spreadsheet-originated exports,
// identified the same way UnixEpoch identifies epoch-seconds columns: by a
// timestamp-like column name and most sampled values falling in a
// plausible date range.
type ExcelSerialDate struct{}

func (ExcelSerialDate) Name() string { return "excel_serial_date" }

func (ExcelSerialDate) AppliesTo(meta profiler.ColumnMeta) bool {
	d := strings.ToUpper(meta.DeclaredType)
	if !strings.Contains(d, "INT") && !strings.Contains(d, "REAL") && !strings.Contains(d, "FLOA") && !strings.Contains(d, "DOUB") {
		return false
	}
	return epochNamePattern.MatchString(meta.Name)
}

func (ExcelSerialDate) Evaluate(meta profiler.ColumnMeta, samples []profiler.Value) (profiler.Finding, bool) {
	var total, plausible int
	for _, v := range samples {
		f, ok := asFloat64(v)
		if !ok {
			continue
		}
		total++
		if f >= excelSerialMin && f <= excelSerialMax {
			plausible++
		}
	}
	if total == 0 || float64(plausible)/float64(total) < 0.5 {
		return profiler.Finding{}, false
	}
	return profiler.Finding{
		SuggestedType: "timestamptz",
		Confidence:    0.85,
		Rationale:     "column name matches a timestamp-like pattern and most sampled values fall in a plausible Excel/Access serial-date range",
		TransformExpr: "excel_serial_to_timestamptz",
	}, true
}

func init() { profiler.Register(ExcelSerialDate{}) }
