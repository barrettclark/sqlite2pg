// Package tui is the terminal UI a human uses to approve or override the
// profiler's column-type decisions before a load proceeds: one screen per
// table shows real sample data and every column's type decision together,
// so a proposed type is always judged next to the data it describes.
package tui

import (
	"strconv"
	"strings"
	"time"

	"sqlite2pg/internal/review"
)

// dateLayouts are the formats previewValueForType accepts for
// "date"/"timestamptz" — matching what a plain COPY (no transform) would
// need to parse, not every format Postgres itself understands.
var dateLayouts = []string{
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02",
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
// integer types, decimal-formatted for floating-point types) rather than a
// bare valid/invalid flag, so a human can see e.g. what "3.7" becomes under
// "integer" or what "3" becomes under "double precision". For non-numeric
// target types it falls back to a validity check — whether the raw text
// would parse as that Postgres type with no transform applied — since
// there's no meaningful "conversion" to preview for e.g. a UUID string
// under "boolean". "NULL" (the preview grid's placeholder for a nil value)
// always displays as-is and is always valid, since NULL is valid for any
// nullable column.
func previewValueForType(value, targetType string) (display string, valid bool) {
	if value == "NULL" {
		return value, true
	}
	switch targetType {
	case "integer", "bigint", "smallint":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, false
		}
		return strconv.FormatInt(int64(f), 10), true
	case "real", "double precision", "numeric":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, false
		}
		formatted := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(formatted, ".") {
			formatted += ".0"
		}
		return formatted, true
	case "boolean":
		switch strings.ToLower(value) {
		case "0", "1", "true", "false", "t", "f":
			return value, true
		}
		return value, false
	case "date", "timestamptz":
		for _, layout := range dateLayouts {
			if _, err := time.Parse(layout, value); err == nil {
				return value, true
			}
		}
		return value, false
	default:
		// text, jsonb, bytea: any string is valid, displayed as-is.
		return value, true
	}
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
			if _, valueValid := previewValueForType(v, t); !valueValid {
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
