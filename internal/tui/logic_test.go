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
