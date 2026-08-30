package ddl

import (
	"crypto/sha1"
	"encoding/hex"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
)

// maxIdentifierLen is Postgres's identifier length limit (NAMEDATALEN=64,
// minus 1 for the trailing null byte the server reserves) — the same
// constant cmd/migrate/provision.go applies to generated database names
// (see maxDatabaseNameLen there).
const maxIdentifierLen = 63

// identifierHashLen is how many hex characters of a name's sha1 to keep
// when disambiguating a truncation collision — enough that two different
// full names re-colliding by hash as well is vanishingly unlikely, without
// eating much of the readable prefix.
const identifierHashLen = 8

// PostgresColumnNames maps every one of tc's included columns (see
// IncludedColumns) to the identifier that must actually appear in
// generated DDL and in the COPY protocol's target column list. Postgres
// truncates any identifier over maxIdentifierLen bytes at the wire/parser
// level (NAMEDATALEN), so two source columns whose names are identical in
// their first 63 bytes but differ after that — e.g. two very long,
// near-duplicate names — collide the moment Postgres truncates them,
// producing "column ... specified more than once" (issue #21). Names
// already within the limit are returned unchanged; only within-table
// collisions after truncation get a short hash suffix, applied
// deterministically so the same source config always maps to the same
// identifiers across separate `profile`/`review`/`load` runs.
func PostgresColumnNames(tc config.TableConfig) map[string]string {
	return disambiguateIdentifiers(IncludedColumns(tc))
}

// disambiguateIdentifiers maps each name in names (in declared order) to a
// Postgres-safe identifier. Every name is first truncated to
// maxIdentifierLen bytes (a no-op if it's already within the limit). Any
// group of names that truncate to the same identifier is then
// disambiguated: each member gets maxIdentifierLen bytes of its own
// original name, minus room for "_" plus an identifierHashLen-character
// hex hash of that full original name, appended as a suffix — so the
// group's members stay both unique and stable across runs, since the
// truncation-plus-hash is a pure function of each name alone.
func disambiguateIdentifiers(names []string) map[string]string {
	disambiguated := disambiguateNames(names, names)

	result := make(map[string]string, len(names))
	for i, name := range names {
		result[name] = disambiguated[i]
	}
	return result
}

// disambiguateNames is disambiguateIdentifiers' index-parallel counterpart,
// for callers that can't key a result map by displayNames alone because two
// different entries may legitimately share the same display name (e.g. two
// foreign keys on the same local columns that reference different tables —
// see foreignKeyConstraintNames in foreign_keys.go). Every collision, from
// truncation or from a shared display name, is disambiguated using the
// corresponding entry in identities — a value guaranteed unique per entry —
// so the hash suffix still distinguishes them even when their display names
// are identical.
func disambiguateNames(displayNames, identities []string) []string {
	groups := make(map[string][]int, len(displayNames))
	for i, name := range displayNames {
		t := truncateBytes(name, maxIdentifierLen)
		groups[t] = append(groups[t], i)
	}

	result := make([]string, len(displayNames))
	for truncated, idxs := range groups {
		if len(idxs) == 1 {
			result[idxs[0]] = truncated
			continue
		}
		for _, i := range idxs {
			result[i] = disambiguateOne(displayNames[i], identities[i])
		}
	}
	return result
}

// disambiguateOne returns display's truncation-safe identifier plus a short
// hash suffix of identity — the value that actually distinguishes this
// entry from the others it collided with (usually identity == display, but
// see disambiguateNames).
func disambiguateOne(display, identity string) string {
	sum := sha1.Sum([]byte(identity))
	suffix := "_" + hex.EncodeToString(sum[:])[:identifierHashLen]
	base := truncateBytes(display, maxIdentifierLen-len(suffix))
	return base + suffix
}

// quoteIdent double-quotes name as a SQL identifier, doubling any embedded
// double quotes per SQL's identifier-quoting rule (e.g. `a"b` becomes
// `"a""b"`). This must produce exactly what the COPY path's
// pgx.Identifier.Sanitize() produces for the same name (see
// internal/copywriter/load.go) — DDL and COPY have to agree on what
// identifier they're naming, or Postgres accepts the CREATE TABLE but COPY
// (or vice versa) fails or silently targets a different column (issue #26).
// Go's fmt.Sprintf("%q", name) must never be used for a SQL identifier: it
// backslash-escapes like a Go string literal, which is not valid SQL and
// disagrees with Sanitize on any name containing a quote or backslash.
func quoteIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

// truncateBytes returns s truncated to at most max bytes. Postgres itself
// truncates identifiers byte-wise (not rune-wise), so this matches that
// behavior rather than trying to avoid splitting a multi-byte UTF-8
// sequence — the same tradeoff deriveDatabaseName in
// cmd/migrate/provision.go accepts.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
