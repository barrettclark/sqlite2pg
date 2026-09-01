package pipeline

import (
	"testing"

	"sqlite2pg/internal/config"
)

// TestPrimaryKeyOrderingIsSafe_FalseWhenPKColumnHasTransform covers issue
// #60: verifyTableOrdered assumes both sides walk rows in the same order
// when it ORDER BYs the primary key. That only holds when the Postgres-side
// PK value is the same value SQLite ordered by. When the PK column carries
// a transform (a TEXT PK of digit strings mapped to bigint via
// numeric_text_to_integer, a TEXT PK of UUIDs mapped to uuid via
// uuid_format), SQLite orders the original text and Postgres orders the
// converted value — genuinely different orders — and the positional
// comparison mass-false-fails. primaryKeyOrderingIsSafe must return false
// so VerifyTable degrades to the order-independent path.
func TestPrimaryKeyOrderingIsSafe_FalseWhenPKColumnHasTransform(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE items (id TEXT PRIMARY KEY, label TEXT);`)
	tc := config.TableConfig{
		Include: true,
		Columns: map[string]config.ColumnConfig{
			"id":    {TargetType: "bigint", Transform: "numeric_text_to_integer", PrimaryKeySeq: 1, Reviewed: true},
			"label": {TargetType: "text", Reviewed: true},
		},
	}

	safe, err := primaryKeyOrderingIsSafe(db, "items", []string{"id"}, tc)
	if err != nil {
		t.Fatalf("primaryKeyOrderingIsSafe: %v", err)
	}
	if safe {
		t.Error("primaryKeyOrderingIsSafe = true for a PK column with a numeric_text_to_integer transform; want false")
	}
}

// TestPrimaryKeyOrderingIsSafe_TrueForPlainPKNoTransform confirms the
// common case is unaffected — a plain INTEGER/TEXT primary key with no
// transform still uses the cheaper, exact PK-ordered comparison.
func TestPrimaryKeyOrderingIsSafe_TrueForPlainPKNoTransform(t *testing.T) {
	db, _ := openTestDB(t, `CREATE TABLE items (id INTEGER PRIMARY KEY, label TEXT);`)
	tc := config.TableConfig{
		Include: true,
		Columns: map[string]config.ColumnConfig{
			"id":    {TargetType: "bigint", PrimaryKeySeq: 1, Reviewed: true},
			"label": {TargetType: "text", Reviewed: true},
		},
	}

	safe, err := primaryKeyOrderingIsSafe(db, "items", []string{"id"}, tc)
	if err != nil {
		t.Fatalf("primaryKeyOrderingIsSafe: %v", err)
	}
	if !safe {
		t.Error("primaryKeyOrderingIsSafe = false for a plain INTEGER primary key with no transform; want true")
	}
}
