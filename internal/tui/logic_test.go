package tui

import (
	"testing"

	"sqlite2pg/internal/review"
)

func TestFindTable_ReturnsMatchByName(t *testing.T) {
	summary := review.ReviewSummary{Tables: []review.TableView{{Name: "bikes"}, {Name: "trips"}}}
	tv := findTable(summary, "trips")
	if tv.Name != "trips" {
		t.Fatalf("expected trips, got %q", tv.Name)
	}
}

func TestFindTable_ReturnsZeroValueWhenNotFound(t *testing.T) {
	tv := findTable(review.ReviewSummary{}, "missing")
	if tv.Name != "" {
		t.Fatalf("expected zero-value TableView, got %+v", tv)
	}
}

func TestColumnSampleCells_ExtractsOneColumnInRowOrder(t *testing.T) {
	tv := review.TableView{
		Columns: []review.ColumnView{{Column: "a"}, {Column: "b"}},
		Rows:    [][]string{{"1", "x"}, {"2", "y"}},
	}
	got := sampleValues(columnSampleCells(tv, "b"))
	want := []string{"x", "y"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestColumnSampleCells_ReturnsNilForUnknownColumn(t *testing.T) {
	tv := review.TableView{Columns: []review.ColumnView{{Column: "a"}}, Rows: [][]string{{"1"}}}
	if got := columnSampleCells(tv, "missing"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestPreviewValueForType_CoercesNumericValuesRatherThanJustFlagging(t *testing.T) {
	cases := []struct {
		value, targetType, wantDisplay string
		wantValid                      bool
	}{
		{"3", "integer", "3", true},
		{"3", "double precision", "3.0", true},
		{"3.5", "double precision", "3.5", true},
		{"-2", "bigint", "-2", true},
		{"not-a-number", "integer", "not-a-number", false},
		{"NULL", "integer", "NULL", true},
	}
	for _, c := range cases {
		display, _, valid := previewValueForType(c.value, c.targetType, "", false)
		if display != c.wantDisplay || valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) = (%q, %v), want (%q, %v)",
				c.value, c.targetType, display, valid, c.wantDisplay, c.wantValid)
		}
	}
}

// TestPreviewValueForType_RejectsFractionalValuesForIntegerTypes is issue
// #80's (audit finding M1) regression: previewValueForType used to
// "preview" a genuinely fractional value like "3.7" as "3" under
// integer — a truncation the real load never performs, since with no
// transform attached the raw value goes to pgx unconverted and fails.
// Fractional values must be rejected outright, not silently truncated.
func TestPreviewValueForType_RejectsFractionalValuesForIntegerTypes(t *testing.T) {
	cases := []string{"integer", "bigint", "smallint"}
	for _, targetType := range cases {
		if _, _, valid := previewValueForType("3.7", targetType, "", false); valid {
			t.Errorf("previewValueForType(%q, %q): expected invalid, not a silent truncation", "3.7", targetType)
		}
	}
}

// TestPreviewValueForType_IntegerPreservesExactPrecisionBeyondFloat64 is
// issue #81's (audit finding M2) regression: previewValueForType used to
// route integer previews through strconv.ParseFloat + int64(f), silently
// corrupting any value beyond float64's ~15-17 significant digits — the
// same bug numeric_text_to_integer itself was fixed for (issue #15), just
// never mirrored in the TUI.
func TestPreviewValueForType_IntegerPreservesExactPrecisionBeyondFloat64(t *testing.T) {
	display, _, valid := previewValueForType("2124037125711300644", "bigint", "", false)
	if !valid {
		t.Fatal("expected valid")
	}
	if display != "2124037125711300644" {
		t.Errorf("previewValueForType: got %q, want the exact 19-digit value unchanged (precision lost)", display)
	}
}

// The picker must not offer a float type for a text value the COPY path
// would reject as non-finite.
func TestPreviewValueForType_RejectsNonFiniteFloatText(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "+Inf", "-Infinity"} {
		for _, targetType := range []string{"real", "double precision", "numeric"} {
			if _, _, valid := previewValueForType(value, targetType, "", true); valid {
				t.Errorf("previewValueForType(%q, %q): expected invalid", value, targetType)
			}
		}
	}
}

// A REAL ±Inf is a valid float8 and numeric on PG14+, so it previews as
// either with no transform; only text tokens are refused (see RejectsNonFiniteFloatText).
func TestPreviewValueForType_AcceptsREALInfinity(t *testing.T) {
	for _, targetType := range []string{"double precision", "numeric"} {
		for _, value := range []string{"+Inf", "-Inf"} {
			t.Run(targetType+"/"+value, func(t *testing.T) {
				display, transform, valid := previewValueForType(value, targetType, "REAL", false)
				if !valid {
					t.Fatalf("previewValueForType(%q, %s, non-text): expected valid", value, targetType)
				}
				if transform != "" {
					t.Errorf("transform = %q, want none", transform)
				}
				if display != value {
					t.Errorf("display = %q, want %q", display, value)
				}
			})
		}
	}
}

func TestValidTypesForColumn_OffersDoublePrecisionOnlyForREALInfinity(t *testing.T) {
	has := func(types []string, want string) bool {
		for _, typ := range types {
			if typ == want {
				return true
			}
		}
		return false
	}
	if got := validTypesForColumn([]sampleCell{{value: "+Inf", isText: false}}, "REAL", false); !has(got, "double precision") {
		t.Errorf("REAL +Inf: double precision not offered, got %v", got)
	}
	if got := validTypesForColumn([]sampleCell{{value: "+Inf", isText: true}}, "TEXT", false); has(got, "double precision") {
		t.Errorf("text \"+Inf\": double precision offered, got %v", got)
	}
}

// A column sent to review by the full-table check can have a type that a
// sampled token rejects (e.g. "+Inf" in TEXT); that type must not be offered.
func TestValidTypesForColumn_NeverOffersInvalidCurrentType(t *testing.T) {
	cases := []struct {
		name        string
		cells       []sampleCell
		currentType string
		declared    string
		wantOffered bool
	}{
		{"TEXT +Inf sample, current double precision", []sampleCell{{value: "+Inf", isText: true}}, "double precision", "TEXT", false},
		{"TEXT +Inf sample, current numeric", []sampleCell{{value: "+Inf", isText: true}}, "numeric", "TEXT", false},
		{"REAL +Inf sample, current double precision", []sampleCell{{value: "+Inf", isText: false}}, "double precision", "REAL", true},
		{"TEXT 1.5 sample, current double precision", []sampleCell{{value: "1.5", isText: true}}, "double precision", "TEXT", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validTypesForColumn(tc.cells, tc.declared, false)
			offered := false
			for _, typ := range got {
				if typ == tc.currentType {
					offered = true
				}
			}
			if offered != tc.wantOffered {
				t.Errorf("%q offered = %v, want %v (got %v)", tc.currentType, offered, tc.wantOffered, got)
			}
		})
	}
}

// "" is skipped for validity, so a numeric-looking sample still offers the integer
// types (numeric_text_to_integer accepts ""). The float types are excluded
// because their pass-through transform cannot encode "" (see the empty-row test).
func TestValidTypesForColumn_EmptySamplesAreSkipped(t *testing.T) {
	got := validTypesForColumn([]sampleCell{{value: ""}, {value: "7"}, {value: "NULL"}}, "TEXT", false)
	for _, want := range []string{"integer", "bigint", "smallint"} {
		if !containsType(got, want) {
			t.Errorf("%q not offered for samples with empty string, got %v", want, got)
		}
	}
}

func containsType(types []string, want string) bool {
	for _, typ := range types {
		if typ == want {
			return true
		}
	}
	return false
}

func TestPreviewValueForType_ValidityForNonNumericTypes(t *testing.T) {
	cases := []struct {
		value, targetType string
		wantValid         bool
	}{
		{"1", "boolean", true},
		{"0", "boolean", true},
		// "true"/"t"/"f" are no longer accepted: previewValueForType
		// always calls copywriter.Transform("int_to_bool", ...) with a Go
		// string (issue #80's audit finding M1), and int_to_bool's own
		// string branch only recognizes "0"/"1" literally — the transform
		// itself also accepts numeric int64/int/float64 input (any
		// nonzero is true), but that path is never reachable from here,
		// since this preview only ever has a display string to pass it.
		{"true", "boolean", false},
		{"90b141b9-c39f-4a26", "boolean", false},
		{"2024-01-02", "date", true},
		{"90b141b9-c39f-4a26", "date", false},
		{"anything at all", "text", true},
		{"NULL", "date", true},
	}
	for _, c := range cases {
		_, _, valid := previewValueForType(c.value, c.targetType, "", false)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) valid = %v, want %v", c.value, c.targetType, valid, c.wantValid)
		}
	}
}

// TestPreviewValueForType_JsonbValidatesRealJSON is issue #80's (audit
// finding M1) regression: jsonb used to fall into the default: catch-all
// (meant for text/bytea), so any string — including plain prose —
// validated as jsonb with no check at all; COPY would then fail with
// "invalid input syntax for type json".
func TestPreviewValueForType_JsonbValidatesRealJSON(t *testing.T) {
	cases := []struct {
		value     string
		wantValid bool
	}{
		{`{"type":"Point","coordinates":[1,2]}`, true},
		{"plain prose, not JSON at all", false},
		{"NULL", true},
	}
	for _, c := range cases {
		_, transform, valid := previewValueForType(c.value, "jsonb", "", false)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"jsonb\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
		if valid && c.value != "NULL" && transform != "text_to_jsonb" {
			t.Errorf("previewValueForType(%q, \"jsonb\") transform = %q, want %q", c.value, transform, "text_to_jsonb")
		}
	}
}

func TestPreviewValueForType_UUID(t *testing.T) {
	cases := []struct {
		value     string
		wantValid bool
	}{
		{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", true},
		{"E4EFF6F3-3F1A-4D6E-9C1E-7C3D2A5B9E10", true}, // uppercase, still valid
		{"not-a-uuid", false},
		{"NULL", true},
	}
	for _, c := range cases {
		_, _, valid := previewValueForType(c.value, "uuid", "", false)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"uuid\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
	}
}

func TestPreviewValueForType_UUIDList(t *testing.T) {
	cases := []struct {
		value     string
		wantValid bool
	}{
		{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", true}, // single UUID, no NUL: still a valid (1-element) list
		{"cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022", true},
		{"7113aab7-628f-4050-ae49-dbecac110ca8\\␀a5d79c54-81c3-4a73-af6a-ad5c143d3f21", true}, // real beets escaped separator
		{"cc75b164-273c-4dce-9cdf-292045a0d38b\x00not-a-uuid", false},
		{"not-a-uuid", false},
		{"NULL", true},
	}
	for _, c := range cases {
		_, _, valid := previewValueForType(c.value, "uuid[]", "", false)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"uuid[]\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
	}
}

func TestValidTypesForColumn_FiltersOutTypesAnySampleFails(t *testing.T) {
	// Every value is a plain non-negative integer string, so the numeric
	// and text-like types validate; boolean/date/timestamptz don't, since
	// "12"/"34" aren't boolean-shaped or date-formatted.
	values := []string{"12", "34", "0"}
	got := validTypesForColumn(plainCells(values...), "", false)
	want := map[string]bool{
		"integer": true, "bigint": true, "smallint": true,
		"real": true, "double precision": true, "numeric": true,
		"text": true, "jsonb": true, "bytea": true,
		"boolean": false, "date": false, "timestamptz": false,
	}
	gotSet := map[string]bool{}
	for _, typ := range got {
		gotSet[typ] = true
	}
	for typ, wantPresent := range want {
		if gotSet[typ] != wantPresent {
			t.Errorf("validTypesForColumn(%v, \"\") contains %q = %v, want %v", values, typ, gotSet[typ], wantPresent)
		}
	}
}

func TestFirstNonNullCell_SkipsNullAndEmpty(t *testing.T) {
	if got := firstNonNullCell(plainCells("NULL", "", "3.7", "4")).value; got != "3.7" {
		t.Errorf("expected \"3.7\", got %q", got)
	}
}

func TestFirstNonNullCell_ReturnsEmptyWhenNoneQualify(t *testing.T) {
	if got := firstNonNullCell(plainCells("NULL", "", "NULL")).value; got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTypeShortcuts_CoversEveryTypeOptionWithNoDuplicateRune(t *testing.T) {
	seen := map[rune]string{}
	for _, t2 := range review.TypeOptions {
		r, ok := typeShortcuts[t2]
		if !ok {
			t.Errorf("no shortcut defined for %q", t2)
			continue
		}
		if other, dup := seen[r]; dup {
			t.Errorf("shortcut %q used by both %q and %q", r, other, t2)
		}
		seen[r] = t2
	}
}

func TestFlaggedColumns_ReturnsOnlyNeedsReviewColumnsInTableOrder(t *testing.T) {
	summary := review.ReviewSummary{Tables: []review.TableView{
		{Name: "albums", Columns: []review.ColumnView{
			{Column: "AlbumId", NeedsReview: false},
			{Column: "ArtistId", NeedsReview: true},
		}},
		{Name: "tracks", Columns: []review.ColumnView{
			{Column: "Flag", NeedsReview: true},
		}},
	}}
	got := flaggedColumns(summary)
	want := []flaggedColumn{
		{Table: "albums", Column: "ArtistId"},
		{Table: "tracks", Column: "Flag"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFlaggedColumns_IncludesAlreadyReviewedColumns(t *testing.T) {
	// NeedsReview reflects original confidence, not whether a human has
	// since resolved it — a reviewed column must stay in the flagged list
	// so it's still reachable via the jump command.
	summary := review.ReviewSummary{Tables: []review.TableView{
		{Name: "t", Columns: []review.ColumnView{
			{Column: "c", NeedsReview: true, Reviewed: true},
		}},
	}}
	got := flaggedColumns(summary)
	if len(got) != 1 || got[0].Column != "c" {
		t.Errorf("expected the reviewed-but-flagged column to still appear, got %v", got)
	}
}

func TestNextFlaggedColumn_StepsForwardAndWrapsAround(t *testing.T) {
	flagged := []flaggedColumn{{Table: "a", Column: "x"}, {Table: "a", Column: "y"}, {Table: "b", Column: "z"}}

	next, ok := nextFlaggedColumn(flagged, flagged[0], true)
	if !ok || next != flagged[1] {
		t.Errorf("expected %+v, got %+v (ok=%v)", flagged[1], next, ok)
	}

	next, ok = nextFlaggedColumn(flagged, flagged[2], true)
	if !ok || next != flagged[0] {
		t.Errorf("expected wraparound to %+v, got %+v (ok=%v)", flagged[0], next, ok)
	}
}

func TestNextFlaggedColumn_StepsBackwardAndWrapsAround(t *testing.T) {
	flagged := []flaggedColumn{{Table: "a", Column: "x"}, {Table: "a", Column: "y"}, {Table: "b", Column: "z"}}

	prev, ok := nextFlaggedColumn(flagged, flagged[0], false)
	if !ok || prev != flagged[2] {
		t.Errorf("expected wraparound to %+v, got %+v (ok=%v)", flagged[2], prev, ok)
	}
}

func TestNextFlaggedColumn_CurrentNotInListStartsAtFirstOrLast(t *testing.T) {
	flagged := []flaggedColumn{{Table: "a", Column: "x"}, {Table: "a", Column: "y"}}
	notFlagged := flaggedColumn{Table: "a", Column: "unflagged"}

	next, ok := nextFlaggedColumn(flagged, notFlagged, true)
	if !ok || next != flagged[0] {
		t.Errorf("forward from an unflagged column: expected first entry %+v, got %+v", flagged[0], next)
	}

	prev, ok := nextFlaggedColumn(flagged, notFlagged, false)
	if !ok || prev != flagged[len(flagged)-1] {
		t.Errorf("backward from an unflagged column: expected last entry %+v, got %+v", flagged[len(flagged)-1], prev)
	}
}

func TestNextFlaggedColumn_EmptyListReturnsNotOK(t *testing.T) {
	if _, ok := nextFlaggedColumn(nil, flaggedColumn{}, true); ok {
		t.Error("expected ok=false for an empty flagged list")
	}
}

func TestValidTypesForColumn_OffersTimestamptzForAPlausibleUnixEpochValueNotAlreadyTimestamptz(t *testing.T) {
	// bikes.last_reported's raw value (issue #27): a plausible Unix epoch
	// seconds integer. timestamptz must be offered because unix_epoch_seconds
	// converts it, not because the column already has that type (the old
	// implementation ran raw text through date-string parsing, which 1712345678
	// could never pass).
	values := []string{"1712345678"}
	got := validTypesForColumn(plainCells(values...), "", false)
	found := false
	for _, typ := range got {
		if typ == "timestamptz" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected timestamptz to be offered for a plausible Unix epoch value, got %v", got)
	}
}

func TestValidTypesForColumn_DoesNotOfferTimestamptzForOrdinarySmallIntegers(t *testing.T) {
	// Guards against the naive fix of just running every integer through
	// unix_epoch_seconds unconditionally: an ordinary small integer (e.g.
	// a count) is not remotely epoch-shaped and must not "validate" as
	// timestamptz just because Transform happens not to error on it.
	values := []string{"12", "34", "0"}
	got := validTypesForColumn(plainCells(values...), "", false)
	for _, typ := range got {
		if typ == "timestamptz" || typ == "date" {
			t.Errorf("did not expect %q to be offered for ordinary small integers, got %v", typ, got)
		}
	}
}

func TestPreviewValueForType_TimestamptzViaUnixEpochSecondsTransform(t *testing.T) {
	display, transform, valid := previewValueForType("1712345678", "timestamptz", "", false)
	if !valid {
		t.Fatal("expected 1712345678 to be valid as timestamptz via unix_epoch_seconds")
	}
	want := "2024-04-05T19:34:38Z"
	if display != want {
		t.Errorf("previewValueForType(1712345678, timestamptz) display = %q, want %q", display, want)
	}
	if transform != "unix_epoch_seconds" {
		t.Errorf("previewValueForType(1712345678, timestamptz) transform = %q, want %q", transform, "unix_epoch_seconds")
	}
}

// TestPreviewValueForType_TimestamptzViaScientificNotationEpoch is issue
// #92's (audit finding L6) regression: review.formatSampleValue renders a
// REAL-affinity epoch column's value through %v, which switches to
// scientific notation for anything this large
// (fmt.Sprintf("%v", float64(1712345678)) == "1.712345678e+09", confirmed
// empirically). strconv.ParseInt (dateTransformPreview's old parse call)
// doesn't understand that form and rejects it outright, silently skipping
// every epoch-seconds/millis/micros check for exactly the large-magnitude
// values they exist to catch.
func TestPreviewValueForType_TimestamptzViaScientificNotationEpoch(t *testing.T) {
	display, transform, valid := previewValueForType("1.712345678e+09", "timestamptz", "", false)
	if !valid {
		t.Fatal("expected the scientific-notation form of a valid epoch-seconds value to still validate as timestamptz")
	}
	want := "2024-04-05T19:34:38Z"
	if display != want {
		t.Errorf("previewValueForType(1.712345678e+09, timestamptz) display = %q, want %q", display, want)
	}
	if transform != "unix_epoch_seconds" {
		t.Errorf("previewValueForType(1.712345678e+09, timestamptz) transform = %q, want %q", transform, "unix_epoch_seconds")
	}
}

// TestPreviewValueForType_ReturnsTheTransformUsedToProduceEachPreview
// covers issue #41: previewValueForType must report which transform (if
// any) it used to validate/preview each candidate type, so onTypeSelected
// can attach that same transform to the decision it applies instead of
// discarding it. "" means the type is directly compatible with the raw
// value and needs no transform at COPY time.
func TestPreviewValueForType_ReturnsTheTransformUsedToProduceEachPreview(t *testing.T) {
	cases := []struct {
		value, targetType, wantTransform string
	}{
		{"3", "integer", "numeric_text_to_integer"},         // issue #80/#81
		{"1", "boolean", "int_to_bool"},                     // issue #80
		{`{"a":1}`, "jsonb", "text_to_jsonb"},               // issue #80
		{"anything at all", "text", ""},                     // native text passthrough
		{"1712345678", "timestamptz", "unix_epoch_seconds"}, // issue #27/#41
		{"2024-01-02T03:04:05Z", "timestamptz", "iso8601_to_timestamptz"},
		{"2024-01-02", "date", "iso8601_to_date"},
		{"20240102", "date", "yyyymmdd_to_date"},
		{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "uuid", "uuid_format"},
		{"90b141b9-c39f-4a26-8f5d-9d3c1e2a7b10", "uuid[]", "uuid_list_format"}, // issue #12/#41
		{"cc75b164-273c-4dce-9cdf-292045a0d38b\x003422ac1a-8dbb-4f23-a337-0bd0a0150022", "uuid[]", "uuid_list_format"},
	}
	for _, c := range cases {
		_, transform, valid := previewValueForType(c.value, c.targetType, "", false)
		if !valid {
			t.Errorf("previewValueForType(%q, %q): expected valid", c.value, c.targetType)
			continue
		}
		if transform != c.wantTransform {
			t.Errorf("previewValueForType(%q, %q) transform = %q, want %q", c.value, c.targetType, transform, c.wantTransform)
		}
	}
}

// TestDateTransformPreview_OnlyTriesTransformsThatMatchTheRequestedTargetType
// guards against a subtle regression: iso8601_to_timestamptz and
// iso8601_to_date both parse the same ISO-shaped input, but only one
// produces a value suited to the target column. Requesting a preview for
// "date" must never come back with a timestamptz-only transform (or vice
// versa) — onTypeSelected would attach a transform mismatched to the type
// the human actually picked.
func TestDateTransformPreview_OnlyTriesTransformsThatMatchTheRequestedTargetType(t *testing.T) {
	_, transform, ok := dateTransformPreview("2024-01-02", "date")
	if !ok || transform != "iso8601_to_date" {
		t.Errorf("dateTransformPreview(2024-01-02, date) = (_, %q, %v), want (_, iso8601_to_date, true)", transform, ok)
	}
	_, transform, ok = dateTransformPreview("2024-01-02", "timestamptz")
	if !ok || transform != "iso8601_to_timestamptz" {
		t.Errorf("dateTransformPreview(2024-01-02, timestamptz) = (_, %q, %v), want (_, iso8601_to_timestamptz, true)", transform, ok)
	}
	// A plausible Julian day number is date-only (julian_day_to_date has no
	// timestamptz counterpart) — requesting timestamptz for it must not
	// fall through to some other, wrong transform.
	_, _, ok = dateTransformPreview("2451545", "timestamptz")
	if ok {
		t.Error("expected a Julian day value to not validate as timestamptz (julian_day_to_date targets date only)")
	}
}

func TestValidTypesForColumn_ExcludesSmallintForOutOfRangeValues(t *testing.T) {
	// 70000 is outside int2's range (-32768..32767); offering smallint
	// here would let the picker promise a type the real COPY then rejects
	// with "value out of range for type smallint" (issue #27).
	values := []string{"70000"}
	got := validTypesForColumn(plainCells(values...), "", false)
	for _, typ := range got {
		if typ == "smallint" {
			t.Errorf("did not expect smallint to be offered for out-of-range value 70000, got %v", got)
		}
	}
	for _, want := range []string{"integer", "bigint"} {
		found := false
		for _, typ := range got {
			if typ == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q still offered for 70000, got %v", want, got)
		}
	}
}

func TestPreviewValueForType_SmallintRangeCheck(t *testing.T) {
	cases := []struct {
		value     string
		wantValid bool
	}{
		{"70000", false},
		{"32767", true},
		{"-32768", true},
		{"-32769", false},
	}
	for _, c := range cases {
		_, _, valid := previewValueForType(c.value, "smallint", "", false)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"smallint\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
	}
}

func TestPreviewValueForType_IntegerRangeCheck(t *testing.T) {
	cases := []struct {
		value     string
		wantValid bool
	}{
		{"2147483648", false}, // math.MaxInt32 + 1
		{"2147483647", true},
		{"-2147483648", true},
	}
	for _, c := range cases {
		_, _, valid := previewValueForType(c.value, "integer", "", false)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"integer\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
	}
}

func TestValidTypesForColumn_ExcludesIntegerWhenSampleIsNotAnInteger(t *testing.T) {
	values := []string{"not-a-number-at-all"}
	got := validTypesForColumn(plainCells(values...), "", false)
	for _, typ := range got {
		if typ == "integer" {
			t.Errorf("integer offered though its preview rejects the sample, got %v", got)
		}
	}
	for _, typ := range got {
		if typ != "integer" && typ != "text" && typ != "jsonb" && typ != "bytea" {
			t.Errorf("unexpected type %q included for a non-numeric, non-date-like string", typ)
		}
	}
}

// plainCells wraps values as non-TEXT samples, as a REAL/INTEGER column's
// sampled values would be.
func plainCells(values ...string) []sampleCell {
	cells := make([]sampleCell, len(values))
	for i, v := range values {
		cells[i] = sampleCell{value: v}
	}
	return cells
}

// A missing or mismatched IsText must fail closed: the cell counts as text,
// so a TEXT "+Inf" is never offered double precision.
func TestColumnSampleCells_MissingIsTextFailsClosed(t *testing.T) {
	cols := []review.ColumnView{{Column: "x"}}
	cases := []struct {
		name       string
		tv         review.TableView
		wantDouble bool
	}{
		{
			name:       "nil IsText with TEXT +Inf",
			tv:         review.TableView{Columns: cols, Rows: [][]string{{"+Inf"}}},
			wantDouble: false,
		},
		{
			name: "ragged IsText shorter than Rows",
			tv: review.TableView{
				Columns: cols,
				Rows:    [][]string{{"+Inf"}, {"1"}},
				IsText:  [][]bool{{false}},
			},
			wantDouble: false,
		},
		{
			name: "IsText row entry shorter than column index",
			tv: review.TableView{
				Columns: []review.ColumnView{{Column: "a"}, {Column: "x"}},
				Rows:    [][]string{{"1", "+Inf"}},
				IsText:  [][]bool{{false}},
			},
			wantDouble: false,
		},
		{
			name: "correct IsText with REAL +Inf",
			tv: review.TableView{
				Columns: cols,
				Rows:    [][]string{{"+Inf"}},
				IsText:  [][]bool{{false}},
			},
			wantDouble: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validTypesForColumn(columnSampleCells(tc.tv, "x"), "REAL", false)
			has := false
			for _, typ := range got {
				if typ == "double precision" {
					has = true
				}
			}
			if has != tc.wantDouble {
				t.Errorf("double precision offered = %v, want %v (types %v)", has, tc.wantDouble, got)
			}
		})
	}
}

func TestValidTypesForColumn_AllEmptyTextOffersOnlyTypesAcceptingEmpty(t *testing.T) {
	cells := []sampleCell{{value: ""}, {value: ""}}
	nullable := []string{"text", "integer", "bigint", "smallint", "bytea", "uuid", "uuid[]"}
	if got := validTypesForColumn(cells, "TEXT", false); !equalTypes(got, nullable) {
		t.Errorf("nullable all-empty column: got %v, want %v", got, nullable)
	}
	if got := validTypesForColumn(cells, "TEXT", true); !equalTypes(got, []string{"text", "bytea"}) {
		t.Errorf("NOT NULL all-empty column: got %v, want [text bytea]", got)
	}
}

func equalTypes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestValidTypesForColumn_AllNullColumnOffersEveryType(t *testing.T) {
	cases := []struct {
		declared, want string
	}{
		{"INTEGER", "integer"},
		{"REAL", "real"},
		{"TEXT", "date"},
	}
	for _, tc := range cases {
		t.Run(tc.declared+" to "+tc.want, func(t *testing.T) {
			got := validTypesForColumn([]sampleCell{{value: "NULL"}, {value: "NULL"}}, tc.declared, false)
			if !containsType(got, tc.want) {
				t.Errorf("%q not offered for an all-NULL %s column, got %v", tc.want, tc.declared, got)
			}
		})
	}
}

// A NOT NULL or PRIMARY KEY column must not take a transform that turns ""
// into NULL, since COPY then aborts on the NULL.
func TestValidTypesForColumn_RejectNullColumnExcludesNullProducingTransforms(t *testing.T) {
	cases := []struct {
		name       string
		cells      []sampleCell
		rejectNull bool
		typ        string
		wantOffer  bool
	}{
		{"NOT NULL integer with empty row, integer", plainCells("1", ""), true, "integer", false},
		{"NOT NULL integer with empty row, bigint", plainCells("1", ""), true, "bigint", false},
		{"NOT NULL integer with empty row, smallint", plainCells("1", ""), true, "smallint", false},
		{"nullable integer with empty row, integer", plainCells("1", ""), false, "integer", true},
		{"NOT NULL integer without empty row, integer", plainCells("1", "2"), true, "integer", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validTypesForColumn(tc.cells, "INTEGER", tc.rejectNull)
			if containsType(got, tc.typ) != tc.wantOffer {
				t.Errorf("%q offered = %v, want %v (got %v)", tc.typ, !tc.wantOffer, tc.wantOffer, got)
			}
		})
	}
}

// The loader applies a column's transform to every row and does not map ""
// to NULL, so a "" row must not reach a transform that rejects it.
func TestValidTypesForColumn_EmptyRowsExcludeTypesWhoseTransformRejectsEmpty(t *testing.T) {
	cases := []struct {
		name      string
		values    []string
		typ       string
		wantOffer bool
	}{
		{"boolean rejects \"\" via int_to_bool", []string{"0", "1", ""}, "boolean", false},
		{"jsonb rejects \"\" via text_to_jsonb", []string{"{}", ""}, "jsonb", false},
		{"date rejects \"\" via iso8601_to_date", []string{"2021-06-01", ""}, "date", false},
		{"nullable integer accepts \"\" via numeric_text_to_integer", []string{"1", "2", ""}, "integer", true},
		{"double precision with pass-through transform rejects \"\"", []string{"1.5", ""}, "double precision", false},
		{"text accepts \"\"", []string{"abc", ""}, "text", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := validTypesForColumn(plainCells(tc.values...), "TEXT", false)
			offered := false
			for _, typ := range got {
				if typ == tc.typ {
					offered = true
				}
			}
			if offered != tc.wantOffer {
				t.Errorf("%q offered = %v, want %v (got %v)", tc.typ, offered, tc.wantOffer, got)
			}
		})
	}
}

func TestStandardTransform_CoversEveryTypeOption(t *testing.T) {
	want := map[string]string{
		"text": "", "bytea": "", "real": "", "double precision": "", "numeric": "",
		"integer": "numeric_text_to_integer", "bigint": "numeric_text_to_integer", "smallint": "numeric_text_to_integer",
		"boolean": "int_to_bool", "date": "iso8601_to_date", "timestamptz": "iso8601_to_timestamptz",
		"jsonb": "text_to_jsonb", "uuid": "uuid_format", "uuid[]": "uuid_list_format",
	}
	for _, typ := range review.TypeOptions {
		t.Run(typ, func(t *testing.T) {
			wantTransform, known := want[typ]
			if !known {
				t.Fatalf("no expected standard transform for %q", typ)
			}
			rep, ok := representativeValue[typ]
			if !ok {
				t.Fatalf("no representativeValue for %q", typ)
			}
			if _, _, valid := previewValueForType(rep, typ, "", false); !valid {
				t.Fatalf("representative %q does not validate as %q", rep, typ)
			}
			got, ok := standardTransform(typ, "")
			if !ok || got != wantTransform {
				t.Errorf("standardTransform(%q) = (%q, %v), want (%q, true)", typ, got, ok, wantTransform)
			}
		})
	}
}

// A TEXT "NULL" is a value; only a SQL NULL offers every type.
func TestValidTypesForColumn_TextNullIsValueNotSQLNull(t *testing.T) {
	textNull := validTypesForColumn([]sampleCell{{value: "NULL", isText: true}}, "TEXT", false)
	if containsType(textNull, "integer") {
		t.Errorf("integer offered for TEXT \"NULL\", got %v", textNull)
	}
	if !containsType(textNull, "text") {
		t.Errorf("text not offered for TEXT \"NULL\", got %v", textNull)
	}
	sqlNull := validTypesForColumn([]sampleCell{{value: "NULL", isText: false}}, "INTEGER", false)
	if !containsType(sqlNull, "integer") {
		t.Errorf("integer not offered for SQL NULL, got %v", sqlNull)
	}
}

func TestStoredTransformFits(t *testing.T) {
	cases := []struct {
		name       string
		cells      []sampleCell
		typ        string
		transform  string
		rejectNull bool
		want       bool
	}{
		{"valid stored integer transform is kept", []sampleCell{{value: "42", isText: true}}, "integer", "numeric_text_to_integer", false, true},
		{"out-of-range integer sample is not kept", []sampleCell{{value: "3000000000", isText: true}}, "integer", "numeric_text_to_integer", false, false},
		{"implausible epoch for timestamptz is not kept", []sampleCell{{value: "12", isText: true}}, "timestamptz", "unix_epoch_seconds", false, false},
		{"stored nullif_sentinels that nulls a sentinel is kept", []sampleCell{{value: "1.5", isText: true}, {value: "NA", isText: true}}, "double precision", "nullif_sentinels", false, true},
		{"stored empty transform is never kept", []sampleCell{{value: "1.5", isText: true}}, "double precision", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storedTransformFits(tc.cells, tc.typ, "", tc.transform, tc.rejectNull); got != tc.want {
				t.Errorf("storedTransformFits = %v, want %v", got, tc.want)
			}
		})
	}
}
