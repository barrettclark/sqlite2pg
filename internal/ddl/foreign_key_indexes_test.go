package ddl

import (
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

func TestGenerateForeignKeyIndexes_EmitsCreateIndexForAValidForeignKey(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"artists": {
				Include:     true,
				ColumnOrder: []string{"ArtistId"},
				Columns:     map[string]config.ColumnConfig{"ArtistId": {TargetType: "integer"}},
			},
			"albums": {
				Include:     true,
				ColumnOrder: []string{"AlbumId", "ArtistId"},
				Columns: map[string]config.ColumnConfig{
					"AlbumId":  {TargetType: "integer"},
					"ArtistId": {TargetType: "integer"},
				},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"ArtistId"}, RefTable: "artists", RefColumns: []string{"ArtistId"}},
				},
			},
		},
	}

	statements := GenerateForeignKeyIndexes(cfg)

	if len(statements) != 1 {
		t.Fatalf("expected 1 CREATE INDEX statement, got %d: %v", len(statements), statements)
	}
	stmt := statements[0]
	if !strings.Contains(stmt, `CREATE INDEX "idx_albums_ArtistId" ON "albums" ("ArtistId")`) {
		t.Errorf("expected a CREATE INDEX on albums.ArtistId, got %q", stmt)
	}
}

func TestGenerateForeignKeyIndexes_EmitsCompositeIndexForACompositeForeignKey(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"parents": {
				Include:     true,
				ColumnOrder: []string{"a", "b"},
				Columns: map[string]config.ColumnConfig{
					"a": {TargetType: "integer"},
					"b": {TargetType: "integer"},
				},
			},
			"children": {
				Include:     true,
				ColumnOrder: []string{"x", "y"},
				Columns: map[string]config.ColumnConfig{
					"x": {TargetType: "integer"},
					"y": {TargetType: "integer"},
				},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"x", "y"}, RefTable: "parents", RefColumns: []string{"a", "b"}},
				},
			},
		},
	}

	statements := GenerateForeignKeyIndexes(cfg)

	if len(statements) != 1 {
		t.Fatalf("expected 1 statement, got %d: %v", len(statements), statements)
	}
	if !strings.Contains(statements[0], `ON "children" ("x", "y")`) {
		t.Errorf("expected a composite index on children (x, y), got %q", statements[0])
	}
}

func TestGenerateForeignKeyIndexes_TruncatesAndDisambiguatesLongIndexNames(t *testing.T) {
	// Same collision hazard as the constraint-name case (issue #36):
	// "idx_" + a long table name + joined composite-key column names can
	// exceed Postgres's 63-byte NAMEDATALEN limit, and two different
	// foreign keys on the same table can truncate to the same index name.
	longTable := "a_very_long_table_name_that_pushes_generated_identifiers_over_the_limit"
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"parents": {
				Include:     true,
				ColumnOrder: []string{"column_one", "column_two"},
				Columns: map[string]config.ColumnConfig{
					"column_one": {TargetType: "integer"},
					"column_two": {TargetType: "integer"},
				},
			},
			"other_parents": {
				Include:     true,
				ColumnOrder: []string{"column_one", "column_two"},
				Columns: map[string]config.ColumnConfig{
					"column_one": {TargetType: "integer"},
					"column_two": {TargetType: "integer"},
				},
			},
			longTable: {
				Include:     true,
				ColumnOrder: []string{"column_one", "column_two", "column_three", "column_four"},
				Columns: map[string]config.ColumnConfig{
					"column_one":   {TargetType: "integer"},
					"column_two":   {TargetType: "integer"},
					"column_three": {TargetType: "integer"},
					"column_four":  {TargetType: "integer"},
				},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"column_one", "column_two"}, RefTable: "parents", RefColumns: []string{"column_one", "column_two"}},
					{Columns: []string{"column_three", "column_four"}, RefTable: "other_parents", RefColumns: []string{"column_one", "column_two"}},
				},
			},
		},
	}

	statements := GenerateForeignKeyIndexes(cfg)

	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(statements), statements)
	}

	names := make(map[string]bool, len(statements))
	for _, stmt := range statements {
		start := strings.Index(stmt, "CREATE INDEX \"") + len("CREATE INDEX \"")
		end := strings.Index(stmt[start:], "\"")
		name := stmt[start : start+end]

		if len(name) > 63 {
			t.Errorf("index name %q is %d bytes, exceeds Postgres's 63-byte NAMEDATALEN limit", name, len(name))
		}
		if names[name] {
			t.Errorf("index name %q was generated for more than one foreign key; Postgres would reject the second CREATE INDEX as a duplicate", name)
		}
		names[name] = true
	}
}

func TestGenerateForeignKeyIndexes_DisambiguatesAcrossDifferentTables(t *testing.T) {
	// Issue #43: a Postgres index is schema-scoped, not table-scoped like a
	// constraint, so disambiguation has to span every table's foreign keys
	// together — not run per-table the way foreignKeyConstraintNames
	// correctly does. Two tables whose names differ only after the 63-byte
	// NAMEDATALEN cutoff, each with a one-column FK to a shared parent,
	// independently generate the identical truncated index name unless
	// disambiguation sees both tables at once.
	base := strings.Repeat("a", 60)
	table1 := base + "x"
	table2 := base + "y"

	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"parents": {
				Include:     true,
				ColumnOrder: []string{"p"},
				Columns:     map[string]config.ColumnConfig{"p": {TargetType: "integer"}},
			},
			table1: {
				Include:     true,
				ColumnOrder: []string{"p"},
				Columns:     map[string]config.ColumnConfig{"p": {TargetType: "integer"}},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"p"}, RefTable: "parents", RefColumns: []string{"p"}},
				},
			},
			table2: {
				Include:     true,
				ColumnOrder: []string{"p"},
				Columns:     map[string]config.ColumnConfig{"p": {TargetType: "integer"}},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"p"}, RefTable: "parents", RefColumns: []string{"p"}},
				},
			},
		},
	}

	statements := GenerateForeignKeyIndexes(cfg)

	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(statements), statements)
	}

	names := make(map[string]bool, len(statements))
	for _, stmt := range statements {
		start := strings.Index(stmt, "CREATE INDEX \"") + len("CREATE INDEX \"")
		end := strings.Index(stmt[start:], "\"")
		name := stmt[start : start+end]

		if len(name) > 63 {
			t.Errorf("index name %q is %d bytes, exceeds Postgres's 63-byte NAMEDATALEN limit", name, len(name))
		}
		if names[name] {
			t.Fatalf("index name %q was generated for foreign keys on two different tables; Postgres would reject the second CREATE INDEX as a duplicate", name)
		}
		names[name] = true
	}
}

func TestGenerateForeignKeyIndexes_SkipsAnInvalidForeignKey(t *testing.T) {
	// Same shape as GenerateForeignKeyConstraints' skip tests: a foreign
	// key referencing an excluded table never becomes a real constraint,
	// so it shouldn't get a recommended index either.
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"artists": {Include: false},
			"albums": {
				Include:     true,
				ColumnOrder: []string{"AlbumId", "ArtistId"},
				Columns: map[string]config.ColumnConfig{
					"AlbumId":  {TargetType: "integer"},
					"ArtistId": {TargetType: "integer"},
				},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"ArtistId"}, RefTable: "artists", RefColumns: []string{"ArtistId"}},
				},
			},
		},
	}

	statements := GenerateForeignKeyIndexes(cfg)

	if len(statements) != 0 {
		t.Fatalf("expected no index statements for an invalid foreign key, got %v", statements)
	}
}
