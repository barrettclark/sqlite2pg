package tui

import "testing"

// issue #156 (audit cycle 5 M1): PR #151's exponent-normalization in the
// integer arm of previewValueForType assumed the display string is always
// fmt's %v of a float64. It isn't — for a *string* SQLite value %v is the
// string verbatim, and "1e+06" is byte-identical to %v of float64(1e6).
// A TEXT-affinity column literally holding "1e+06" would get bigint +
// numeric_text_to_integer committed by the picker, then COPY aborts on
// the raw string. The discriminator is the column's SQLite affinity.
func TestPreviewValueForType_IntegerRejectsScientificNotationOnTextAffinityColumn(t *testing.T) {
	// TEXT / BLOB / no-affinity column: "1e+06" is a literal the row
	// stores, so numeric_text_to_integer fails it at COPY — the picker
	// must not offer an integer type.
	for _, declared := range []string{"TEXT", "VARCHAR(20)", "BLOB", ""} {
		for _, v := range []string{"1e+06", "1.5e3", "1.712345678e+09", "1.23e+05"} {
			for _, typ := range []string{"integer", "bigint", "smallint"} {
				if _, _, valid := previewValueForType(v, typ, declared); valid {
					t.Errorf("previewValueForType(%q, %q, %q) = valid; a TEXT-affinity row stores the string literally and COPY rejects it", v, typ, declared)
				}
			}
		}
	}
}

// On a REAL/NUMERIC-affinity column the same scientific-notation string
// really is fmt's %v of a float64 the driver returned, so #139's picker
// behaviour is preserved.
func TestPreviewValueForType_IntegerAcceptsScientificNotationOnRealAffinityColumn(t *testing.T) {
	for _, c := range []struct {
		value, declared, want string
	}{
		{"1e+06", "REAL", "1000000"},
		{"1.712345678e+09", "REAL", "1712345678"},
		{"1.712345678e+09", "DOUBLE PRECISION", "1712345678"},
		{"1e+06", "NUMERIC", "1000000"},
	} {
		display, transform, valid := previewValueForType(c.value, "bigint", c.declared)
		if !valid || display != c.want || transform != "numeric_text_to_integer" {
			t.Errorf("previewValueForType(%q, bigint, %q) = (%q, %q, %v), want (%q, numeric_text_to_integer, true)",
				c.value, c.declared, display, transform, valid, c.want)
		}
	}
}

// sqliteNumericAffinity follows SQLite's determination-of-column-affinity
// rules directly.
func TestSqliteNumericAffinity(t *testing.T) {
	numeric := []string{"INTEGER", "INT", "BIGINT", "int32", "REAL", "FLOAT", "DOUBLE", "NUMERIC", "DECIMAL(10,2)", "DATE", "realdate"}
	for _, d := range numeric {
		if !sqliteNumericAffinity(d) {
			t.Errorf("sqliteNumericAffinity(%q) = false, want true", d)
		}
	}
	textOrBlob := []string{"TEXT", "VARCHAR(80)", "CHARACTER(20)", "CLOB", "NVARCHAR", "BLOB", ""}
	for _, d := range textOrBlob {
		if sqliteNumericAffinity(d) {
			t.Errorf("sqliteNumericAffinity(%q) = true, want false", d)
		}
	}
}

// Plain digit strings (incl. a 19-digit exact ID) are still untouched on
// any affinity.
func TestPreviewValueForType_IntegerPlainDigitStringsUnaffectedByCycle5Guard(t *testing.T) {
	for _, declared := range []string{"TEXT", "REAL", "INTEGER", ""} {
		display, _, valid := previewValueForType("2124037125711300644", "bigint", declared)
		if !valid || display != "2124037125711300644" {
			t.Errorf("previewValueForType(19-digit, bigint, %q) = (%q, %v), want (%q, true)", declared, display, valid, "2124037125711300644")
		}
	}
}
