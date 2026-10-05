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
	"sqlite2pg/internal/profiler"
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
	// thing ParseInt's own strictness did.
	//
	// The magnitude guard (f within [epochSecondsMin, epochMicrosMax]) is
	// load-bearing, not just an optimization: ParseFloat also accepts
	// "Inf" and values past 2^63, and math.Trunc(±Inf) == ±Inf, so the
	// f == math.Trunc(f) test passes for infinity — int64(f) on that is
	// implementation-dependent per the Go spec, the exact class the
	// transform.go PR #98 round guarded against in six places (issue #112
	// / L3). Restricting entry to the epoch window means int64(f) only
	// ever runs on a value below 2^53, where it is exact and defined; a
	// non-finite or out-of-window f falls through to the float-shaped
	// transforms below (which pass f straight to copywriter.Transform,
	// where transform.go's own guards handle it).
	if f, err := strconv.ParseFloat(value, 64); err == nil && targetType == "timestamptz" &&
		f == math.Trunc(f) && f >= float64(epochSecondsMin) && f <= float64(epochMicrosMax) {
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

// sampleCell is one sampled value as shown in the preview grid, plus
// whether SQLite stored it as TEXT (the grid's string is ambiguous: a REAL
// +Inf and the text "+Inf" both render as "+Inf").
type sampleCell struct {
	value  string
	isText bool
}

// isNull reports a SQL NULL: the grid shows NULL for nil, and a TEXT "NULL" is a value.
func (c sampleCell) isNull() bool {
	return c.value == "NULL" && !c.isText
}

// columnSampleCells extracts one column's sample cells (in row order)
// from tv's preview grid, for display and validity checking.
func columnSampleCells(tv review.TableView, columnName string) []sampleCell {
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
	// Fail closed: without a matching IsText, every cell is treated as text.
	textKnown := len(tv.IsText) == len(tv.Rows)
	cells := make([]sampleCell, 0, len(tv.Rows))
	for r, row := range tv.Rows {
		if idx < len(row) {
			cell := sampleCell{value: row[idx], isText: true}
			if textKnown && idx < len(tv.IsText[r]) {
				cell.isText = tv.IsText[r][idx]
			}
			cells = append(cells, cell)
		}
	}
	return cells
}

// sampleValues returns the display strings of cells, in order.
func sampleValues(cells []sampleCell) []string {
	values := make([]string, len(cells))
	for i, c := range cells {
		values[i] = c.value
	}
	return values
}

// sqliteNumericAffinity reports whether declaredType gives the column
// INTEGER, REAL, or NUMERIC affinity per SQLite's rules
// (sqlite.org/datatype3.html#determination_of_column_affinity) — the
// affinities where the driver hands back an int64/float64 that %v renders
// (a large float64 in scientific notation). TEXT and BLOB affinity, and a
// column with no declared type, preserve a row's literal string, so a
// sample like "1e+06" there is text the row actually stores — not a
// float64 rendering to normalize (issue #156).
func sqliteNumericAffinity(declaredType string) bool {
	t := strings.ToUpper(declaredType)
	switch {
	case strings.Contains(t, "INT"):
		return true
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"):
		return false
	case strings.Contains(t, "BLOB"), t == "":
		return false
	default:
		return true // REAL / FLOA / DOUB, or NUMERIC (the catch-all)
	}
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
//
// declaredType is the column's raw SQLite declared type, used only to
// tell a float64 the driver returned (rendered by %v, possibly in
// scientific notation) from a string the row literally stores that
// happens to look the same — see the integer arm (issue #156).
// isText is the sample's SQLite storage class; the float arms refuse a TEXT
// non-finite spelling.
func previewValueForType(value, targetType, declaredType string, isText bool) (display, transform string, valid bool) {
	if value == "NULL" && !isText {
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
		// A REAL/NUMERIC-affinity sample renders through fmt's %v, which
		// switches to scientific notation past 1e6 ("1.712345678e+09").
		// numeric_text_to_integer's exact-integer parse rejects an
		// exponent outright, so without this the picker drops
		// integer/bigint/smallint for a large whole-number REAL column
		// (issue #139). Only normalize when the column's SQLite affinity
		// is numeric — on a TEXT/BLOB-affinity column "1e+06" is a
		// literal string the row stores, and coercing it into an integer
		// transform makes COPY abort on the raw value (issue #156).
		// A plain digit string carries no exponent and is untouched.
		numText := value
		if strings.ContainsAny(numText, "eE") && sqliteNumericAffinity(declaredType) {
			if f, perr := strconv.ParseFloat(numText, 64); perr == nil {
				numText = strconv.FormatFloat(f, 'f', -1, 64)
			}
		}
		// For a float64 sample above 2^53 the exact-text parse below and
		// the load-time int64(f) conversion can differ by a few units
		// (issue #164 / L7) — the previewed integer is then slightly off
		// from what is stored. Display-only: both sides of `verify`
		// recompute the same value, so there is no mismatch. Not worth
		// the float64 round-trip to make the preview exact.
		result, err := copywriter.Transform("numeric_text_to_integer", numText)
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
		// numeric would round a TEXT decimal through float64; a precision-preserving
		// transform is a follow-up.
		if targetType == "numeric" && isText {
			return value, "", false
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, "", false
		}
		// A REAL ±Inf is a valid float8 (and numeric on PG14+), so only a
		// text token's infinity is refused.
		if math.IsNaN(f) || (math.IsInf(f, 0) && isText) {
			return value, "", false
		}
		if math.IsInf(f, 0) {
			return strconv.FormatFloat(f, 'f', -1, 64), "", true
		}
		formatted := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(formatted, ".") {
			formatted += ".0"
		}
		// pgx cannot encode a TEXT number as float8; numeric_text_to_double parses it.
		if isText {
			return formatted, "numeric_text_to_double", true
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

// firstNonNullCell returns the first cell in cells that isn't the preview
// grid's "NULL" placeholder or empty, or a zero cell if none qualify — used
// to pick one representative sample to preview under each candidate type
// in the picker.
func firstNonNullCell(cells []sampleCell) sampleCell {
	for _, c := range cells {
		if !c.isNull() && c.value != "" {
			return c
		}
	}
	return sampleCell{}
}

// representativeValue is one value of each type; standardTransform derives the
// transform a type needs from it, so a column with no sample still gets one.
var representativeValue = map[string]string{
	"text": "x", "bytea": "x",
	"integer": "1", "bigint": "1", "smallint": "1", "boolean": "1",
	"real": "1.5", "double precision": "1.5", "numeric": "1.5",
	"date": "2021-01-01", "timestamptz": "2021-01-01T00:00:00Z",
	"jsonb":  "{}",
	"uuid":   "00000000-0000-0000-0000-000000000000",
	"uuid[]": "00000000-0000-0000-0000-000000000000",
}

// standardTransform returns the transform previewValueForType attaches to the
// type's representative value; ok is false if that value does not validate.
func standardTransform(typeName, declaredType string) (transform string, ok bool) {
	_, transform, ok = previewValueForType(representativeValue[typeName], typeName, declaredType, false)
	return transform, ok
}

// hasEmptyCell reports whether any sample is the empty string.
func hasEmptyCell(cells []sampleCell) bool {
	for _, c := range cells {
		if c.value == "" {
			return true
		}
	}
	return false
}

// commonTransformForType returns the transform all samples agree on, or the standard
// transform when there is no non-empty sample. ok is false when a sample does not
// validate as typeName or the samples disagree on a transform (issue #64).
func commonTransformForType(cells []sampleCell, typeName, declaredType string) (transform string, ok bool) {
	seen := false
	for _, c := range cells {
		if c.isNull() || c.value == "" {
			continue
		}
		_, t, valid := previewValueForType(c.value, typeName, declaredType, c.isText)
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
	if !seen {
		return standardTransform(typeName, declaredType)
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
// and `sqlite2pg resolve --apply` (cmd/sqlite2pg/main.go's runResolve, issue
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

// validTypesForColumn returns the types the samples can be saved as. A type needs
// the shared transform (commonTransformForType) and, with "" rows, a transform
// that accepts "" (transformAcceptsEmpty), so the list matches onTypeSelected.
func validTypesForColumn(cells []sampleCell, declaredType string, rejectNull bool) []string {
	emptyRows := false
	for _, c := range cells {
		if c.value == "" {
			emptyRows = true
		}
	}
	var result []string
	for _, t := range review.TypeOptions {
		if typeLoadsSamples(cells, t, declaredType, emptyRows, rejectNull) {
			result = append(result, t)
		}
	}
	return result
}

// typeLoadsSamples reports whether the samples can be saved as typeName. It is
// gated on commonTransformForType (ok is false for invalid or disagreeing
// samples), and when emptyRows its transform must also accept "".
func typeLoadsSamples(cells []sampleCell, typeName, declaredType string, emptyRows, rejectNull bool) bool {
	if _, ok := commonTransformForType(cells, typeName, declaredType); !ok {
		return false
	}
	sawValue := false
	for _, c := range cells {
		if c.isNull() || c.value == "" {
			continue
		}
		_, transform, valid := previewValueForType(c.value, typeName, declaredType, c.isText)
		if !valid {
			return false
		}
		if emptyRows && !transformAcceptsEmpty(typeName, transform, rejectNull) {
			return false
		}
		sawValue = true
	}
	// With no non-empty sample, "" rows get the type's standard transform.
	if emptyRows && !sawValue {
		t, ok := standardTransform(typeName, declaredType)
		return ok && transformAcceptsEmpty(typeName, t, rejectNull)
	}
	return true
}

// transformAcceptsEmpty reports whether a "" row loads under transform for
// typeName. An empty transform passes "" through, which only text and bytea
// accept. A transform that turns "" into NULL is refused when rejectNull, since
// the PRIMARY KEY or NOT NULL column then aborts COPY.
func transformAcceptsEmpty(typeName, transform string, rejectNull bool) bool {
	if transform == "" {
		return typeName == "text" || typeName == "bytea"
	}
	out, err := copywriter.Transform(transform, "")
	if err != nil {
		return false
	}
	return out != nil || !rejectNull
}

// emptyNullMarker is the picker row note; emptyNullStatus is the full status-bar line.
const (
	emptyNullMarker = `SQLite "" is not NULL; "" rows load as NULL`
	emptyNullStatus = `empty-string rows load as NULL (SQLite "" is not NULL)`
)

// emptyRowsBecomeNull reports whether transform stores a "" sample as NULL. It
// only sees "" rows inside the preview sample, so rows past it can be missed.
func emptyRowsBecomeNull(cells []sampleCell, transform string) bool {
	if transform == "" || !hasEmptyCell(cells) {
		return false
	}
	out, err := copywriter.Transform(transform, "")
	return err == nil && out == nil
}

// storedTransformFits reports whether a stored transform can be kept for the
// samples: each non-NULL sample must convert without error and, unless the
// transform turns it into NULL, pass previewValueForType (range and date
// windows included). A stored "" is never kept, so it is re-derived.
func storedTransformFits(cells []sampleCell, typeName, declaredType, transform string, rejectNull bool) bool {
	if transform == "" {
		return false
	}
	for _, c := range cells {
		if c.isNull() {
			continue
		}
		out, err := copywriter.Transform(transform, rawSample(c))
		if err != nil {
			return false
		}
		if out == nil {
			if rejectNull {
				return false
			}
			continue
		}
		if _, _, valid := previewValueForType(c.value, typeName, declaredType, c.isText); !valid {
			return false
		}
	}
	return true
}

// rawSample is a sample as the loader sees it: TEXT as a string, otherwise a
// float64 when it parses.
func rawSample(c sampleCell) profiler.Value {
	if c.isText {
		return c.value
	}
	if f, err := strconv.ParseFloat(c.value, 64); err == nil {
		return f
	}
	return c.value
}
