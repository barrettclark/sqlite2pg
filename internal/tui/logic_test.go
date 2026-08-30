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

func TestColumnSampleValues_ExtractsOneColumnInRowOrder(t *testing.T) {
	tv := review.TableView{
		Columns: []review.ColumnView{{Column: "a"}, {Column: "b"}},
		Rows:    [][]string{{"1", "x"}, {"2", "y"}},
	}
	got := columnSampleValues(tv, "b")
	want := []string{"x", "y"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestColumnSampleValues_ReturnsNilForUnknownColumn(t *testing.T) {
	tv := review.TableView{Columns: []review.ColumnView{{Column: "a"}}, Rows: [][]string{{"1"}}}
	if got := columnSampleValues(tv, "missing"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestPreviewValueForType_CoercesNumericValuesRatherThanJustFlagging(t *testing.T) {
	cases := []struct {
		value, targetType, wantDisplay string
		wantValid                      bool
	}{
		{"3.7", "integer", "3", true},
		{"3", "double precision", "3.0", true},
		{"3.5", "double precision", "3.5", true},
		{"-2.9", "bigint", "-2", true},
		{"not-a-number", "integer", "not-a-number", false},
		{"NULL", "integer", "NULL", true},
	}
	for _, c := range cases {
		display, valid := previewValueForType(c.value, c.targetType)
		if display != c.wantDisplay || valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) = (%q, %v), want (%q, %v)",
				c.value, c.targetType, display, valid, c.wantDisplay, c.wantValid)
		}
	}
}

func TestPreviewValueForType_ValidityForNonNumericTypes(t *testing.T) {
	cases := []struct {
		value, targetType string
		wantValid         bool
	}{
		{"1", "boolean", true},
		{"true", "boolean", true},
		{"90b141b9-c39f-4a26", "boolean", false},
		{"2024-01-02", "date", true},
		{"90b141b9-c39f-4a26", "date", false},
		{"anything at all", "text", true},
		{"NULL", "date", true},
	}
	for _, c := range cases {
		_, valid := previewValueForType(c.value, c.targetType)
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, %q) valid = %v, want %v", c.value, c.targetType, valid, c.wantValid)
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
		_, valid := previewValueForType(c.value, "uuid")
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"uuid\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
	}
}

func TestValidTypesForColumn_FiltersOutTypesAnySampleFails(t *testing.T) {
	// Every value is a plain non-negative integer string, so the numeric
	// and text-like types validate; boolean/date/timestamptz don't, since
	// "12"/"34" aren't boolean-shaped or date-formatted.
	values := []string{"12", "34", "0"}
	got := validTypesForColumn(values, "integer")
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
			t.Errorf("validTypesForColumn(%v, \"integer\") contains %q = %v, want %v", values, typ, gotSet[typ], wantPresent)
		}
	}
}

func TestFirstNonNullValue_SkipsNullAndEmpty(t *testing.T) {
	if got := firstNonNullValue([]string{"NULL", "", "3.7", "4"}); got != "3.7" {
		t.Errorf("expected \"3.7\", got %q", got)
	}
}

func TestFirstNonNullValue_ReturnsEmptyWhenNoneQualify(t *testing.T) {
	if got := firstNonNullValue([]string{"NULL", "", "NULL"}); got != "" {
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
	// seconds integer. currentType is deliberately "integer", not
	// "timestamptz", to prove timestamptz is offered because
	// unix_epoch_seconds actually converts it — not merely because it
	// happens to already be the column's current type (the bug: the old
	// implementation ran the raw text through date-string parsing
	// directly, which 1712345678 could never pass, so timestamptz was
	// missing from the picker for any column that didn't already have it
	// as its current type).
	values := []string{"1712345678"}
	got := validTypesForColumn(values, "integer")
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
	got := validTypesForColumn(values, "integer")
	for _, typ := range got {
		if typ == "timestamptz" || typ == "date" {
			t.Errorf("did not expect %q to be offered for ordinary small integers, got %v", typ, got)
		}
	}
}

func TestPreviewValueForType_TimestamptzViaUnixEpochSecondsTransform(t *testing.T) {
	display, valid := previewValueForType("1712345678", "timestamptz")
	if !valid {
		t.Fatal("expected 1712345678 to be valid as timestamptz via unix_epoch_seconds")
	}
	want := "2024-04-05T19:34:38Z"
	if display != want {
		t.Errorf("previewValueForType(1712345678, timestamptz) display = %q, want %q", display, want)
	}
}

func TestValidTypesForColumn_ExcludesSmallintForOutOfRangeValues(t *testing.T) {
	// 70000 is outside int2's range (-32768..32767); offering smallint
	// here would let the picker promise a type the real COPY then rejects
	// with "value out of range for type smallint" (issue #27).
	values := []string{"70000"}
	got := validTypesForColumn(values, "integer")
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
		_, valid := previewValueForType(c.value, "smallint")
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
		_, valid := previewValueForType(c.value, "integer")
		if valid != c.wantValid {
			t.Errorf("previewValueForType(%q, \"integer\") valid = %v, want %v", c.value, valid, c.wantValid)
		}
	}
}

func TestValidTypesForColumn_AlwaysIncludesCurrentTypeEvenIfInvalid(t *testing.T) {
	values := []string{"not-a-number-at-all"}
	got := validTypesForColumn(values, "integer")
	found := false
	for _, typ := range got {
		if typ == "integer" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected currentType %q always included, got %v", "integer", got)
	}
	for _, typ := range got {
		if typ != "integer" && typ != "text" && typ != "jsonb" && typ != "bytea" {
			t.Errorf("unexpected type %q included for a non-numeric, non-date-like string", typ)
		}
	}
}
