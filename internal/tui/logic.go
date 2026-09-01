// Package tui is the terminal UI a human uses to approve or override the
// profiler's column-type decisions before a load proceeds: one screen per
// table shows real sample data and every column's type decision together,
// so a proposed type is always judged next to the data it describes.
package tui

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/review"
)

// uuidPattern mirrors the canonical-UUID check the uuid_format heuristic
// uses (internal/profiler/heuristics/uuid_format.go) — kept as its own
// copy since that package is internal to the profiler and not meant to be
// imported for a display-only check here.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// epoch/day-number plausibility bounds mirror the same-named heuristics in
// internal/profiler/heuristics (unix_epoch.go, unix_epoch_millis.go,
// unix_epoch_micros.go, excel_serial_date.go, julian_day.go) — kept as
// local copies for the same reason uuidPattern above is: that package is
// internal to the profiler and isn't meant to be imported for a
// display-only check here.
//
// Without these bounds, feeding a raw value straight through
// copywriter.Transform's numeric date transforms would "succeed" for
// nearly any integer or float — unix_epoch_seconds converts ANY int64 into
// *some* time.Time with no error, so an ordinary small integer like "12"
// would validate as timestamptz just as readily as a genuine epoch value.
// dateTransformPreview below applies the same real-world-magnitude check
// the assigning heuristic itself requires before it would ever suggest
// that transform, so previewValueForType only credits a transform when the
// raw value actually looks like its target shape.
const (
	epochSecondsMin = 946684800
	epochSecondsMax = 2051222400
	epochMillisMin  = epochSecondsMin * 1000
	epochMillisMax  = epochSecondsMax * 1000
	epochMicrosMin  = epochSecondsMin * 1000000
	epochMicrosMax  = epochSecondsMax * 1000000
	excelSerialMin  = 36526
	excelSerialMax  = 49310
	julianDayMin    = 1721425.5
	julianDayMax    = 2816787.5
)

// timeFromTransform runs raw through copywriter.Transform under the named
// transform — the exact function the real COPY would use — and reports
// the resulting time.Time, or ok=false if the transform errored or (for
// e.g. a mis-plumbed transform) didn't produce a time.Time at all.
func timeFromTransform(transform string, raw any) (time.Time, bool) {
	result, err := copywriter.Transform(transform, raw)
	if err != nil {
		return time.Time{}, false
	}
	tm, ok := result.(time.Time)
	return tm, ok
}

// dateTransformPreview reports whether value would convert to a real date
// or timestamp under any transform a profiler heuristic could plausibly
// have assigned it for targetType — by actually running it through
// copywriter.Transform, not by re-deriving date-string parsing here (issue
// #27: this is what lets a Unix epoch integer like bikes.last_reported's
// raw 1712345678 validate as timestamptz even when timestamptz isn't
// already the column's current type). Each purely-numeric transform is
// only tried when value's magnitude falls in that transform's own
// real-world plausibility window, so an ordinary small integer or float
// doesn't "convert" its way into looking like a date.
//
// The returned transform name is the transform that actually produced the
// preview (issue #41): only transforms targeting targetType are ever
// tried, since e.g. iso8601_to_timestamptz and iso8601_to_date parse the
// same input but produce values suited to different Postgres columns —
// running the wrong one would hand onTypeSelected a transform that
// doesn't match the type the human actually picked.
func dateTransformPreview(value, targetType string) (time.Time, string, bool) {
	// Parsed via ParseFloat, not ParseInt, even though every epoch bound
	// below is a whole number: review.formatSampleValue renders a
	// float64 (a REAL-affinity epoch column, entirely plausible — e.g.
	// bikes.last_reported stored as REAL) through %v, which switches to
	// scientific notation for anything this large
	// (fmt.Sprintf("%v", float64(1712345678)) == "1.712345678e+09").
	// strconv.ParseInt doesn't understand that form at all and would
	// reject it outright, silently skipping every epoch check below for
	// exactly the large-magnitude values they exist to catch (issue #92's
	// audit, finding L6). ParseFloat parses both plain-integer and
	// scientific-notation text; f == math.Trunc(f) keeps this from
	// treating a genuinely fractional value as an epoch integer, the same
	// thing ParseInt's own strictness did. Every epoch bound here is far
	// below float64's 2^53 exact-integer range, so int64(f) loses no
	// precision.
	if f, err := strconv.ParseFloat(value, 64); err == nil && targetType == "timestamptz" && f == math.Trunc(f) {
		n := int64(f)
		switch {
		case n >= epochSecondsMin && n <= epochSecondsMax:
			if tm, ok := timeFromTransform("unix_epoch_seconds", n); ok {
				return tm, "unix_epoch_seconds", true
			}
		case n >= epochMillisMin && n <= epochMillisMax:
			if tm, ok := timeFromTransform("unix_epoch_millis", n); ok {
				return tm, "unix_epoch_millis", true
			}
		case n >= epochMicrosMin && n <= epochMicrosMax:
			if tm, ok := timeFromTransform("unix_epoch_micros", n); ok {
				return tm, "unix_epoch_micros", true
			}
		}
	}
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		switch {
		case targetType == "timestamptz" && f >= excelSerialMin && f <= excelSerialMax:
			if tm, ok := timeFromTransform("excel_serial_to_timestamptz", f); ok {
				return tm, "excel_serial_to_timestamptz", true
			}
		case targetType == "date" && f >= julianDayMin && f <= julianDayMax:
			if tm, ok := timeFromTransform("julian_day_to_date", f); ok {
				return tm, "julian_day_to_date", true
			}
		}
	}
	// String-shaped transforms are self-limiting (time.Parse against a
	// fixed layout), so no extra plausibility window is needed for these.
	stringTransforms := []string{"iso8601_to_timestamptz", "dayfirst_to_timestamptz"}
	if targetType == "date" {
		stringTransforms = []string{"iso8601_to_date", "yyyymmdd_to_date"}
	}
	for _, transform := range stringTransforms {
		if tm, ok := timeFromTransform(transform, value); ok {
			return tm, transform, true
		}
	}
	return time.Time{}, "", false
}

// findTable returns name's TableView from summary, or a zero-value
// TableView if not found.
func findTable(summary review.ReviewSummary, name string) review.TableView {
	for _, t := range summary.Tables {
		if t.Name == name {
			return t
		}
	}
	return review.TableView{}
}

// columnSampleValues extracts one column's sample values (in row order)
// from tv's preview grid, for display and validity checking.
func columnSampleValues(tv review.TableView, columnName string) []string {
	idx := -1
	for i, c := range tv.Columns {
		if c.Column == columnName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}
	values := make([]string, 0, len(tv.Rows))
	for _, row := range tv.Rows {
		if idx < len(row) {
			values = append(values, row[idx])
		}
	}
	return values
}

// previewValueForType returns what value would look like under targetType:
// for numeric target types, the actual coerced number (truncated for
// integer types, decimal-formatted for floating-point types, and
// range-checked against copywriter.FitsRange for smallint/integer/bigint —
// issue #27) rather than a bare valid/invalid flag, so a human can see
// e.g. what "3.7" becomes under "integer" or what "3" becomes under
// "double precision". "date"/"timestamptz" likewise preview the real
// converted timestamp whenever some transform a profiler heuristic could
// plausibly have assigned (dateTransformPreview) actually converts value,
// not just when value is already a recognized date string — sharing
// copywriter.Transform rather than re-deriving date-string parsing here is
// what lets a Unix epoch integer validate as timestamptz even when
// timestamptz isn't already the column's current type. For every other
// non-numeric target type it falls back to a validity check — whether the
// raw text would parse as that Postgres type with no transform applied —
// since there's no meaningful "conversion" to preview for e.g. a UUID
// string under "boolean". "NULL" (the preview grid's placeholder for a nil
// value) always displays as-is and is always valid, since NULL is valid
// for any nullable column.
//
// The returned transform is the transform name (copywriter.Transform's
// vocabulary) that produced this preview. It's "" only for text/bytea and
// the plain float target types (real/double precision/numeric), whose raw
// value is directly compatible with pgx's COPY protocol unconverted;
// integer/bigint/smallint, boolean, and jsonb each carry a real transform
// too now (numeric_text_to_integer, int_to_bool, text_to_jsonb — issue
// #80's audit, finding M1), since a raw int64/string reaching pgx
// unconverted for those types fails at COPY time despite superficially
// looking "directly compatible." onTypeSelected (issue #41) attaches this
// transform to the decision it applies: a type the picker only offers BECAUSE some
// transform makes it work (date/timestamptz via dateTransformPreview,
// uuid[] via uuid_list_format) must carry that same transform forward when
// selected, or the real COPY fails on the untransformed raw value.
func previewValueForType(value, targetType string) (display, transform string, valid bool) {
	if value == "NULL" {
		return value, "", true
	}
	switch targetType {
	case "integer", "bigint", "smallint":
		// Routed through the real numeric_text_to_integer transform
		// (issue #80's audit, finding M1/M2) rather than
		// strconv.ParseFloat + int64(f): that used to silently corrupt
		// any value beyond float64's ~15-17 significant digits (the same
		// bug numeric_text_to_integer itself was fixed for, issue #15),
		// and it accepted a genuinely fractional value like "3.7" as
		// "valid, previews as 3" — a truncation the real load never
		// performs, since with no transform attached the raw value would
		// go to pgx unconverted. Any type this validates for must always
		// carry the transform that actually makes it work, or a human
		// selecting it here breaks the real COPY.
		result, err := copywriter.Transform("numeric_text_to_integer", value)
		if err != nil {
			return value, "", false
		}
		if result == nil {
			// numeric_text_to_integer treats "" as "no value on file"
			// (matching the numeric_text heuristic's own leniency), same
			// as a NULL sample.
			return "NULL", "numeric_text_to_integer", true
		}
		n := result.(int64)
		if !copywriter.FitsRange(n, targetType) {
			// e.g. 70000 parses fine as a number but is outside
			// smallint's (int2) range — offering smallint here would
			// let the picker promise a type the real COPY then rejects
			// with "value out of range for type smallint" (issue #27).
			return value, "", false
		}
		return strconv.FormatInt(n, 10), "numeric_text_to_integer", true
	case "real", "double precision", "numeric":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, "", false
		}
		formatted := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(formatted, ".") {
			formatted += ".0"
		}
		return formatted, "", true
	case "boolean":
		// Routed through the real int_to_bool transform (issue #80's
		// audit, finding M1): picking boolean on a column whose current
		// target isn't already boolean used to attach no transform at
		// all, so pgx would try to binary-encode the raw int64/string
		// straight into bool and fail — reachable on the single most
		// common review action this tool exists for (converting a 0/1
		// integer column to boolean). value here is always a Go string
		// (this preview only ever has a display string to work with), so
		// this always hits int_to_bool's string branch, which only
		// recognizes "0"/"1" literally — narrower than the "true"/"t"/"f"
		// this preview used to accept for display purposes only; those
		// were never actually convertible before either. (int_to_bool
		// itself also accepts numeric int64/int/float64 input via a
		// separate branch — any nonzero value is true — but that's for
		// the real raw SQLite value at load time, never reachable from
		// this string-only preview.)
		result, err := copywriter.Transform("int_to_bool", value)
		if err != nil {
			return value, "", false
		}
		return strconv.FormatBool(result.(bool)), "int_to_bool", true
	case "date", "timestamptz":
		tm, usedTransform, ok := dateTransformPreview(value, targetType)
		if !ok {
			return value, "", false
		}
		if targetType == "date" {
			return tm.Format("2006-01-02"), usedTransform, true
		}
		return tm.Format(time.RFC3339), usedTransform, true
	case "uuid":
		if !uuidPattern.MatchString(value) {
			return value, "", false
		}
		// A raw string never satisfies pgx's UUID codec (it requires
		// UUIDValuer, which plain string doesn't implement) — uuid_format
		// is what parses the canonical text form into the [16]byte pgx
		// needs, so it's always required at COPY time, not merely when
		// the sample happens to look unusual.
		return value, "uuid_format", true
	case "uuid[]":
		// Mirrors the uuid_list heuristic's own check: normalize
		// beets' real-world "\␀" escape (see
		// heuristics.escapedNulSeparator's doc comment) to a raw NUL,
		// split on it, and require every part to be a canonical UUID.
		// A plain single-UUID value (no separator at all) still
		// validates here too — splitting on a separator that isn't
		// present just returns the one-element slice — so a human can
		// preview uuid[] against a column that's currently all
		// single-UUID values and see it as a valid (if degenerate,
		// one-element-list) choice.
		normalized := strings.ReplaceAll(value, "\\␀", "\x00")
		for _, p := range strings.Split(normalized, "\x00") {
			if !uuidPattern.MatchString(p) {
				return value, "", false
			}
		}
		// uuid_list_format is always required at COPY time (issue #41):
		// the raw NUL-joined string never satisfies pgx's array codec on
		// its own, the same reason uuid_format is always required above.
		return value, "uuid_list_format", true
	case "jsonb":
		// Previously fell into the default: arm below, so any string —
		// including plain prose — validated as jsonb with no check at
		// all; COPY would then fail with "invalid input syntax for type
		// json" (issue #80's audit, finding M1). text_to_jsonb's own
		// json.Valid check is the real validation the load path runs, so
		// route the preview through it and attach it as the transform —
		// unlike the numeric/boolean cases above, text_to_jsonb doesn't
		// reshape the value pgx receives, but the validation still needs
		// to happen at COPY time, exactly like it does for a
		// heuristic-suggested jsonb column (issue #22).
		if _, err := copywriter.Transform("text_to_jsonb", value); err != nil {
			return value, "", false
		}
		return value, "text_to_jsonb", true
	default:
		// text, bytea: any string is valid, displayed as-is, and passed
		// through unconverted.
		return value, "", true
	}
}

// firstNonNullValue returns the first value in values that isn't the
// preview grid's "NULL" placeholder or empty, or "" if none qualify — used
// to pick one representative sample to preview under each candidate type
// in the picker.
func firstNonNullValue(values []string) string {
	for _, v := range values {
		if v != "NULL" && v != "" {
			return v
		}
	}
	return ""
}

// commonTransformForType derives the transform previewValueForType would
// attach for typeName across EVERY non-NULL sample, returning it only if
// they all agree (issue #64).
//
// validTypesForColumn offers date/timestamptz whenever every sample
// converts to it — but different rows can need different transforms
// ("2021-06-01" via iso8601_to_date, "20210704" via yyyymmdd_to_date;
// "1712345678" via unix_epoch_seconds, "40000" via
// excel_serial_to_timestamptz). config.ColumnConfig.Transform is a single
// value, so onTypeSelected can't honour a per-row choice; deriving it from
// one sample (the old firstNonNullValue behaviour) attached a transform
// that then failed the real COPY on every row of the other format.
//
// ok is false when the non-NULL samples disagree on a transform, OR when
// any non-NULL sample doesn't validate for typeName at all — either way
// the caller should refuse the type rather than persist a config that can
// break the load. When ok is true, transform is the shared one (or "" when
// no non-NULL sample needs a transform, e.g. text, or the column is all
// NULL). Non-date types are unaffected: previewValueForType returns a
// fixed transform per type ("" for text/integer, uuid_format for uuid), so
// those always agree.
func commonTransformForType(values []string, typeName string) (transform string, ok bool) {
	seen := false
	for _, v := range values {
		if v == "NULL" || v == "" {
			continue
		}
		_, t, valid := previewValueForType(v, typeName)
		if !valid {
			return "", false
		}
		if !seen {
			transform, seen = t, true
			continue
		}
		if t != transform {
			return "", false
		}
	}
	return transform, true
}

// typeShortcuts maps every review.TypeOptions entry to a distinct
// mnemonic rune for the type picker's single-key selection — pressing the
// rune jumps straight to that type without arrowing through the list
// first. Picked to stay memorable and collision-free across all 14
// options at once (not just whichever subset a given column's sample data
// happens to validate as): "g" for bigint ("biG int"), "f" for double
// precision (its common colloquial name, "float"; "d" was needed for
// date), "x" for bytea (Postgres itself prints bytea in \x-prefixed hex),
// and "a" for uuid[] ("array" — the first and, so far, only array target
// type this tool offers, so the generic mnemonic is unambiguous).
var typeShortcuts = map[string]rune{
	"text":             't',
	"integer":          'i',
	"bigint":           'g',
	"smallint":         's',
	"boolean":          'b',
	"double precision": 'f',
	"real":             'r',
	"numeric":          'n',
	"date":             'd',
	"timestamptz":      'z',
	"jsonb":            'j',
	"bytea":            'x',
	"uuid":             'u',
	"uuid[]":           'a',
}

// flaggedColumn identifies one column flagged for review, by its table and
// column name.
type flaggedColumn struct {
	Table  string
	Column string
}

// flaggedColumns returns every column across summary's tables whose
// NeedsReview is true, in table order then declared column order.
// NeedsReview reflects the confidence the profiler originally computed and
// never changes once a human overrides a column (only Reviewed does), so
// this list is stable for the life of a session: a column already
// resolved stays on it, so jumping back to something already decided is
// always possible, not just the columns still outstanding. This is a
// documented, shared contract, not local to the TUI: review.State.ApplyDecision
// and `migrate resolve --apply` (cmd/migrate/main.go's runResolve, issue
// #53) both leave NeedsReview untouched on override for the same reason —
// it's a permanent profiler verdict, and Reviewed is what tracks whether a
// human has acted on the column.
func flaggedColumns(summary review.ReviewSummary) []flaggedColumn {
	var flagged []flaggedColumn
	for _, t := range summary.Tables {
		for _, c := range t.Columns {
			if c.NeedsReview {
				flagged = append(flagged, flaggedColumn{Table: t.Name, Column: c.Column})
			}
		}
	}
	return flagged
}

// nextFlaggedColumn returns the flagged column to jump to from current,
// stepping forward (or, if forward is false, backward) through flagged in
// a wraparound cycle. If current isn't itself in flagged (e.g. nothing
// selected yet, or the selection is on an auto-approved column), it
// returns flagged's first entry going forward or its last going backward.
// ok is false only when flagged is empty.
func nextFlaggedColumn(flagged []flaggedColumn, current flaggedColumn, forward bool) (flaggedColumn, bool) {
	if len(flagged) == 0 {
		return flaggedColumn{}, false
	}
	idx := -1
	for i, f := range flagged {
		if f == current {
			idx = i
			break
		}
	}
	var next int
	switch {
	case idx == -1 && forward:
		next = 0
	case idx == -1:
		next = len(flagged) - 1
	case forward:
		next = (idx + 1) % len(flagged)
	default:
		next = (idx - 1 + len(flagged)) % len(flagged)
	}
	return flagged[next], true
}

// validTypesForColumn returns the subset of review.TypeOptions that every
// one of values would load successfully as (per previewValueForType),
// always including currentType even if it fails that check — so the type
// picker is never empty and never forces a human off their column's
// current assignment.
func validTypesForColumn(values []string, currentType string) []string {
	var result []string
	for _, t := range review.TypeOptions {
		ok := true
		for _, v := range values {
			if _, _, valueValid := previewValueForType(v, t); !valueValid {
				ok = false
				break
			}
		}
		if ok || t == currentType {
			result = append(result, t)
		}
	}
	return result
}
