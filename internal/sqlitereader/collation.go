package sqlitereader

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// ColumnCollations returns table's declared text-comparison collation for
// every column, keyed by column name — "BINARY" (SQLite's own default,
// used whenever a column has no explicit COLLATE clause) unless the
// column's CREATE TABLE definition names something else (most commonly
// NOCASE or RTRIM, SQLite's other two built-in collations, though a
// database can also register and use a custom one).
//
// SQLite has no PRAGMA that reports a column's declared collation directly
// — PRAGMA table_info/table_xinfo don't include it — so this reads it the
// only way available short of linking SQLite's C API: fetching the
// table's original CREATE TABLE text from sqlite_master.sql and parsing
// each column definition's own COLLATE clause out of it. This exists for
// internal/pipeline.VerifyTable: its primary-key ORDER BY comparison path
// forces Postgres's ORDER BY to COLLATE "C" (byte order) to match SQLite's
// default BINARY comparison, which is only actually a match when the
// source column really is BINARY-collated — a column declared COLLATE
// NOCASE or RTRIM sorts differently, and VerifyTable needs to know that to
// avoid comparing the two sides in genuinely different orders.
func ColumnCollations(db *sql.DB, table string) (map[string]string, error) {
	cols, err := readColumns(db, table)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(cols))
	for _, c := range cols {
		result[c.Name] = "BINARY"
	}

	var createSQL sql.NullString
	err = db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&createSQL)
	if err != nil {
		return nil, fmt.Errorf("reading CREATE TABLE text for %s: %w", table, err)
	}
	if !createSQL.Valid {
		// A table can lack sqlite_master.sql text (e.g. certain virtual
		// tables) — nothing to parse, every column stays at the BINARY
		// default already filled in above.
		return result, nil
	}

	// Only ever override a name readColumns already reported as a real
	// column — parseColumnCollations' top-level split can't distinguish a
	// genuine column definition from a table-level constraint clause (e.g.
	// `PRIMARY KEY (a, b)`, `FOREIGN KEY (...) REFERENCES ...`), but those
	// clauses' "leading identifier" (PRIMARY, FOREIGN, CHECK, ...) can
	// never collide with an actual column name, so filtering through this
	// known-column set is sufficient without a fuller SQL parser.
	for name, collation := range parseColumnCollations(createSQL.String) {
		if _, known := result[name]; known {
			result[name] = collation
		}
	}
	return result, nil
}

// collateClauseRe matches a COLLATE clause and captures its collation
// name, however that name is written: bare, or quoted with any of SQL's
// four identifier-quoting styles ("...", `...`, [...], or '...' — SQLite
// accepts all four for a collation name same as for any other identifier).
var collateClauseRe = regexp.MustCompile(`(?i)\bCOLLATE\s+(?:"([^"]+)"|` + "`([^`]+)`" + `|\[([^\]]+)\]|'([^']+)'|(\w+))`)

// parseColumnCollations parses a CREATE TABLE statement's text (as stored
// verbatim in sqlite_master.sql) into a map of column name to its declared
// collation name (uppercased). A column with no COLLATE clause is simply
// absent from the result — see ColumnCollations, which fills in the
// BINARY default for every real column before consulting this.
//
// This is a targeted parser, not a general SQL grammar: it splits the
// column-definition list on top-level commas (respecting nested
// parentheses and quoted strings/identifiers, so a comma inside a CHECK
// constraint's expression or a quoted name doesn't split a definition in
// two), then for each piece takes its leading identifier as the column
// name and searches the remainder for a COLLATE clause. Table-level
// constraint clauses (PRIMARY KEY (...), FOREIGN KEY (...), CHECK (...),
// UNIQUE (...), CONSTRAINT ...) pass through the same split and get a
// leading "identifier" too (e.g. PRIMARY, FOREIGN), but those can never
// collide with a real column name, and ColumnCollations only ever
// consults this map for names it already knows are real columns — so
// there's no need to specifically recognize and exclude them here.
func parseColumnCollations(createSQL string) map[string]string {
	result := map[string]string{}

	open := columnListOpenParen(createSQL)
	if open < 0 {
		return result
	}
	close := matchingParen(createSQL, open)
	if close < 0 {
		close = len(createSQL)
	}
	body := stripSQLComments(createSQL[open+1 : close])

	for _, part := range splitTopLevelCommas(body) {
		name, rest, ok := leadingIdentifier(strings.TrimSpace(part))
		if !ok {
			continue
		}
		if m := collateClauseRe.FindStringSubmatch(maskParensAndStringLiterals(rest)); m != nil {
			for _, g := range m[1:] {
				if g != "" {
					result[name] = strings.ToUpper(g)
					break
				}
			}
		}
	}
	return result
}

// maskParensAndStringLiterals returns s with every byte inside a
// parenthesised group (depth >= 1) or a string literal replaced by a
// space, so a COLLATE keyword in a CHECK expression or a `DEFAULT 'COLLATE
// NOCASE'` string is not mistaken for the column's own collation clause
// (issues #145, #160). A column's real COLLATE clause is always at the top
// level of its definition.
//
// Kept verbatim at top level:
//   - a `[…]` span — SQLite never treats brackets as a string literal, so
//     it is always an identifier (a collation name, or noise the COLLATE
//     search ignores anyway);
//   - a `'…'`, `"…"`, or backtick span that directly follows the COLLATE
//     keyword — the collation name (SQLite accepts all four quote styles,
//     `COLLATE 'NOCASE'` included).
//
// A `'…'` / `"…"` / backtick span *not* after COLLATE is a string literal
// (for `"…"` and backtick, via SQLite's double-quoted-string misfeature —
// a `DEFAULT` value takes this form) and is masked. Everything nested
// inside parens is masked regardless.
func maskParensAndStringLiterals(s string) string {
	b := []byte(s)
	depth := 0
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '\'', '"', '`', '[':
			j := skipQuoteOrComment(s, i)
			collationName := depth == 0 && (c == '[' || precededByCollateKeyword(s, i))
			if !collationName {
				for k := i; k < j; k++ {
					b[k] = ' '
				}
			}
			i = j
			continue
		case '(':
			depth++
			b[i] = ' '
		case ')':
			if depth > 0 {
				depth--
			}
			b[i] = ' '
		default:
			if depth > 0 {
				b[i] = ' '
			}
		}
		i++
	}
	return string(b)
}

// precededByCollateKeyword reports whether the token immediately before
// s[i] (skipping intervening whitespace) is the keyword COLLATE — i.e.
// s[i] opens the operand of a COLLATE clause. Used to tell
// `COLLATE 'NOCASE'` (a real collation name SQLite accepts) from a string
// DEFAULT that merely contains the word.
func precededByCollateKeyword(s string, i int) bool {
	k := i
	for k > 0 && (s[k-1] == ' ' || s[k-1] == '\t' || s[k-1] == '\n' || s[k-1] == '\r') {
		k--
	}
	const kw = "collate"
	if k < len(kw) || !strings.EqualFold(s[k-len(kw):k], kw) {
		return false
	}
	return k-len(kw) == 0 || isIdentBoundary(s[k-len(kw)-1])
}

// createTablePreambleRe matches CREATE TABLE's keyword preamble — CREATE
// TABLE [IF NOT EXISTS] — up to and including "IF NOT EXISTS" when
// present, everything before the table name itself.
var createTablePreambleRe = regexp.MustCompile(`(?i)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?`)

// createVirtualTableRe matches a CREATE VIRTUAL TABLE preamble.
// ColumnCollations' own sqlite_master query (`type = 'table'`) matches
// virtual tables too — SQLite gives them type='table' there — so a CREATE
// VIRTUAL TABLE statement reaches columnListOpenParen. A virtual table has
// no column-definition list at all: the parens after `USING <module>` are
// the module's argument list, not column definitions (issue #113 / L4).
var createVirtualTableRe = regexp.MustCompile(`(?i)^\s*CREATE\s+VIRTUAL\s+TABLE\b`)

// columnListOpenParen returns the index of the '(' that opens the
// column-definition list — the first '(' AFTER the table name, not simply
// the first '(' anywhere in createSQL. A table literally named e.g.
// "foo(bar)" makes the naive "first '(' in the string" search match the
// one inside the quoted table name instead, parsing the column-definition
// body from the wrong offset and silently leaving every column at its
// BINARY collation default (issue #91's audit, finding L5). Skips the
// CREATE TABLE keyword preamble, then the table name itself — quoted with
// any of SQLite's four identifier-quoting styles or bare, same as
// leadingIdentifier already handles for a column name — before searching
// for '(' from there, skipping any comment between the table name and the
// real column list (issue #104). Returns -1 if no '(' follows the table
// name, or if createSQL is a CREATE VIRTUAL TABLE statement (which has no
// column-definition list — issue #113).
func columnListOpenParen(createSQL string) int {
	if createVirtualTableRe.MatchString(createSQL) {
		return -1
	}
	rest := createSQL
	if loc := createTablePreambleRe.FindStringIndex(createSQL); loc != nil {
		rest = createSQL[loc[1]:]
	}
	if _, afterName, ok := leadingIdentifier(strings.TrimLeft(rest, " \t\n\r")); ok {
		rest = afterName
	}
	for k := 0; k < len(rest); {
		if j := skipQuoteOrComment(rest, k); j != k {
			k = j
			continue
		}
		if rest[k] == '(' {
			return len(createSQL) - len(rest) + k
		}
		k++
	}
	return -1
}

// skipQuoteOrComment returns the index just past a quoted string/identifier
// or comment starting at s[i], or i unchanged if s[i] opens neither.
// Recognized: double-, back- and single-quote strings (a doubled quote
// char escapes itself); [ ] brackets (SQLite bans ] inside, so no escape);
// -- comments to the next CR or LF; and /* ... */ comments. matchingParen
// and splitTopLevelCommas rely on this so a paren or comma inside a
// literal or comment isn't treated as structural (issue #104 / M1).
func skipQuoteOrComment(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch {
	case s[i] == '"' || s[i] == '`' || s[i] == '\'':
		q := s[i]
		for j := i + 1; j < len(s); j++ {
			if s[j] != q {
				continue
			}
			if j+1 < len(s) && s[j+1] == q { // doubled: an escaped quote
				j++
				continue
			}
			return j + 1
		}
		return len(s)
	case s[i] == '[':
		if j := strings.IndexByte(s[i:], ']'); j >= 0 {
			return i + j + 1
		}
		return len(s)
	case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
		if j := strings.IndexAny(s[i:], "\r\n"); j >= 0 {
			return i + j + 1
		}
		return len(s)
	case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
		if j := strings.Index(s[i+2:], "*/"); j >= 0 {
			return i + 2 + j + 2
		}
		return len(s)
	}
	return i
}

// stripSQLComments replaces every -- line comment and /* ... */ block
// comment in s with a single space, leaving quoted strings/identifiers
// (which may legitimately contain "--" or "/*") untouched. The
// column-body split and per-column COLLATE search run on the result, so a
// comment can't contribute a spurious leading identifier or let a real
// column's COLLATE clause be attributed to the wrong name (issue #104).
func stripSQLComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		j := skipQuoteOrComment(s, i)
		if j == i {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch s[i] {
		case '"', '`', '\'', '[':
			b.WriteString(s[i:j]) // quoted span — keep verbatim
		default:
			b.WriteByte(' ') // comment span — drop, preserving a boundary
		}
		i = j
	}
	return b.String()
}

// matchingParen returns the index of the ')' matching the '(' at open, or
// -1 if createSQL[open:] never balances back to depth 0 (malformed input —
// callers fall back to treating the rest of the string as the body).
// Parens inside a quoted string/identifier or a comment are skipped (issue
// #104).
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		if j := skipQuoteOrComment(s, i); j != i {
			i = j
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// splitTopLevelCommas splits s on commas that appear at parenthesis depth
// 0 and outside any quoted string/identifier or comment — so a comma
// inside a nested `CHECK (a, b)` expression, a quoted name like `"a, b"`,
// or a `-- ...` / `/* ... */` comment doesn't produce a spurious split.
// Quoting and comments are handled by skipQuoteOrComment (issue #104).
func splitTopLevelCommas(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); {
		if j := skipQuoteOrComment(s, i); j != i {
			i = j
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
		i++
	}
	parts = append(parts, s[start:])
	return parts
}

// leadingIdentifier reads the identifier at the start of s (after any
// leading whitespace, which the caller is expected to have already
// trimmed), handling all four SQL identifier-quoting styles, and returns
// it along with the remainder of s after it. ok is false if s is empty or
// doesn't start with something identifier-shaped.
func leadingIdentifier(s string) (name, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	switch s[0] {
	case '"', '`', '\'':
		q := s[0]
		// SQLite spells an embedded quote in a quoted identifier by
		// doubling it — `"foo""bar"` is the identifier foo"bar. Scan for
		// the closing quote past any doubled pair, then un-escape (issue
		// #113 / L4: stopping at the first inner quote returned a
		// truncated name and left `"..." ` in the remainder, which
		// columnListOpenParen then mis-parsed).
		for j := 1; j < len(s); j++ {
			if s[j] != q {
				continue
			}
			if j+1 < len(s) && s[j+1] == q { // doubled: an escaped quote
				j++
				continue
			}
			// j is the closing quote. j == 1 means "" — an empty quoted
			// identifier, not a valid name (issue #70).
			if j == 1 {
				return "", "", false
			}
			return strings.ReplaceAll(s[1:j], string([]byte{q, q}), string(q)), s[j+1:], true
		}
		// No closing quote.
		return "", "", false
	case '[':
		end := strings.IndexByte(s, ']')
		// end < 0: no closing bracket. end == 1: "[]" — empty. Neither is
		// a valid name (issue #70).
		if end <= 1 {
			return "", "", false
		}
		return s[1:end], s[end+1:], true
	default:
		i := 0
		for i < len(s) && !isIdentBoundary(s[i]) {
			i++
		}
		if i == 0 {
			return "", "", false
		}
		return s[:i], s[i:], true
	}
}

func isIdentBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '(', ')', ',':
		return true
	}
	return false
}
