package ddl

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

// TestDisambiguateNamesReserving_HashSuffixAlsoAvoidsReserved covers the
// Copilot PR #75 follow-up: reserving a name must keep the DISAMBIGUATED
// (hash-suffixed) output out of the reserved set too, not just gate the
// singleton pass-through. Here both the raw truncated name and the salt-0
// hash output are reserved, so the function must re-salt to a third value.
func TestDisambiguateNamesReserving_HashSuffixAlsoAvoidsReserved(t *testing.T) {
	display := "idx_x"
	identity := "fk-identity-a"

	sum := sha1.Sum([]byte(identity))
	saltZero := display + "_" + hex.EncodeToString(sum[:])[:identifierHashLen]

	reserved := map[string]bool{
		display:  true, // forces the disambiguation branch for the singleton
		saltZero: true, // and the first hash output is claimed too
	}

	got := disambiguateNamesReserving([]string{display}, []string{identity}, reserved)
	if len(got) != 1 {
		t.Fatalf("expected 1 name, got %d", len(got))
	}
	if reserved[got[0]] {
		t.Errorf("disambiguateNamesReserving returned %q, which is in the reserved set", got[0])
	}
	if len(got[0]) > maxIdentifierLen {
		t.Errorf("output %q is %d bytes, over the %d limit", got[0], len(got[0]), maxIdentifierLen)
	}

	// Deterministic: same inputs -> same output.
	again := disambiguateNamesReserving([]string{display}, []string{identity}, reserved)
	if again[0] != got[0] {
		t.Errorf("not deterministic: %q vs %q", got[0], again[0])
	}
}

// TestPostgresTableNames_DisambiguatesTablesCollidingAfter63ByteTruncation
// reproduces issue #44's exact failure scenario: two source tables whose
// names are identical in their first 63 bytes but differ after that must
// not collide once Postgres truncates them to NAMEDATALEN, or the second
// CREATE TABLE fails with "relation ... already exists" — the identical
// hazard issue #21 fixed for columns, one level up at the table level.
func TestPostgresTableNames_DisambiguatesTablesCollidingAfter63ByteTruncation(t *testing.T) {
	base := strings.Repeat("b", 63) // 63 identical bytes
	table1 := base + "1"            // 64 bytes
	table2 := base + "2"            // 64 bytes, identical to table1 up to byte 63

	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			table1: {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
			table2: {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
		},
	}

	names := PostgresTableNames(cfg)

	if len(names) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(names), names)
	}
	id1, id2 := names[table1], names[table2]
	if id1 == "" || id2 == "" {
		t.Fatalf("expected an entry for both source tables, got %v", names)
	}
	if len(id1) > maxIdentifierLen || len(id2) > maxIdentifierLen {
		t.Errorf("expected both identifiers within %d bytes, got %d and %d", maxIdentifierLen, len(id1), len(id2))
	}
	if id1 == id2 {
		t.Fatalf("expected distinct Postgres identifiers for %q and %q (identical in their first 63 bytes), got the same identifier %q for both — Postgres would reject the second CREATE TABLE as a duplicate relation", table1, table2, id1)
	}
}

// TestPostgresTableNames_LeavesNonCollidingNamesUnchanged confirms ordinary
// table names — the overwhelming majority case — pass through verbatim,
// same as PostgresColumnNames does for columns within the 63-byte limit.
func TestPostgresTableNames_LeavesNonCollidingNamesUnchanged(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"users": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
			"orders": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
			"excluded_table": {
				Include:     false,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
		},
	}

	names := PostgresTableNames(cfg)

	if names["users"] != "users" || names["orders"] != "orders" {
		t.Errorf("expected non-colliding table names to pass through unchanged, got %v", names)
	}
	if _, ok := names["excluded_table"]; ok {
		t.Errorf("expected an excluded table to be omitted entirely, got an entry for it: %v", names)
	}
}

// TestGenerateCreateTable_UsingDisambiguatedTableName confirms that
// GenerateCreateTable, given the identifier PostgresTableNames resolved for
// a colliding table, actually emits that resolved identifier in the CREATE
// TABLE statement — the end-to-end shape callers (cmd/migrate/main.go) rely
// on: PostgresTableNames only computes the mapping, GenerateCreateTable is
// what has to honor it.
func TestGenerateCreateTable_UsingDisambiguatedTableName(t *testing.T) {
	base := strings.Repeat("b", 63)
	table1 := base + "1"
	table2 := base + "2"

	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id"},
		Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
	}
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{table1: tc, table2: tc},
	}

	names := PostgresTableNames(cfg)

	stmt1, err := GenerateCreateTable(names[table1], tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stmt2, err := GenerateCreateTable(names[table2], tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(stmt1, quoteIdent(names[table2])) || stmt1 == stmt2 {
		t.Fatalf("expected two distinct CREATE TABLE statements, got:\n%s\nand:\n%s", stmt1, stmt2)
	}
	if !strings.Contains(stmt1, quoteIdent(names[table1])) {
		t.Errorf("expected stmt1 to name %q, got:\n%s", names[table1], stmt1)
	}
	if !strings.Contains(stmt2, quoteIdent(names[table2])) {
		t.Errorf("expected stmt2 to name %q, got:\n%s", names[table2], stmt2)
	}
}
