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

	open := strings.IndexByte(createSQL, '(')
	if open < 0 {
		return result
	}
	close := matchingParen(createSQL, open)
	if close < 0 {
		close = len(createSQL)
	}
	body := createSQL[open+1 : close]

	for _, part := range splitTopLevelCommas(body) {
		name, rest, ok := leadingIdentifier(strings.TrimSpace(part))
		if !ok {
			continue
		}
		if m := collateClauseRe.FindStringSubmatch(rest); m != nil {
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

// matchingParen returns the index of the ')' matching the '(' at open, or
// -1 if createSQL[open:] never balances back to depth 0 (malformed input —
// callers fall back to treating the rest of the string as the body).
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevelCommas splits s on commas that appear at parenthesis depth
// 0 and outside any quoted string/identifier — so a comma inside a nested
// `CHECK (a, b)` expression or a quoted name like `"a, b"` doesn't produce
// a spurious split. Quote handling covers all four identifier/string
// quoting styles SQLite accepts: "...", `...`, [...], and '...'.
func splitTopLevelCommas(s string) []string {
	var parts []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '`', '\'':
			quote = c
		case '[':
			quote = ']'
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
		end := strings.IndexByte(s[1:], q)
		// end < 0: no closing quote. end == 0: closing quote immediately,
		// i.e. an empty quoted identifier ("", ``, '') — not a valid name
		// (issue #70). Both mean "not an identifier here".
		if end <= 0 {
			return "", "", false
		}
		return s[1 : 1+end], s[1+end+1:], true
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
