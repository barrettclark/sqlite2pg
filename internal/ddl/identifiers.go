package ddl

import (
	"crypto/sha1"
	"encoding/hex"

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
	groups := make(map[string][]int, len(names))
	for i, name := range names {
		t := truncateBytes(name, maxIdentifierLen)
		groups[t] = append(groups[t], i)
	}

	result := make(map[string]string, len(names))
	for truncated, idxs := range groups {
		if len(idxs) == 1 {
			result[names[idxs[0]]] = truncated
			continue
		}
		for _, i := range idxs {
			result[names[i]] = disambiguateOne(names[i])
		}
	}
	return result
}

// disambiguateOne returns name's truncation-safe identifier plus a short
// hash suffix of the full original name.
func disambiguateOne(name string) string {
	sum := sha1.Sum([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:])[:identifierHashLen]
	base := truncateBytes(name, maxIdentifierLen-len(suffix))
	return base + suffix
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
