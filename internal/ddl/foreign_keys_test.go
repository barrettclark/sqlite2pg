package ddl

import (
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

func TestGenerateForeignKeyConstraints_EmitsAlterTableForAValidForeignKey(t *testing.T) {
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
					{Columns: []string{"ArtistId"}, RefTable: "artists", RefColumns: []string{"ArtistId"}, OnDelete: "CASCADE"},
				},
			},
		},
	}

	statements, skipped := GenerateForeignKeyConstraints(cfg)

	if len(skipped) != 0 {
		t.Fatalf("expected no skipped foreign keys, got %v", skipped)
	}
	if len(statements) != 1 {
		t.Fatalf("expected 1 ALTER TABLE statement, got %d: %v", len(statements), statements)
	}
	stmt := statements[0]
	if !strings.Contains(stmt, `ALTER TABLE "albums"`) {
		t.Errorf("expected ALTER TABLE albums, got %q", stmt)
	}
	if !strings.Contains(stmt, `FOREIGN KEY ("ArtistId")`) {
		t.Errorf("expected FOREIGN KEY (ArtistId), got %q", stmt)
	}
	if !strings.Contains(stmt, `REFERENCES "artists" ("ArtistId")`) {
		t.Errorf("expected REFERENCES artists (ArtistId), got %q", stmt)
	}
	if !strings.Contains(stmt, "ON DELETE CASCADE") {
		t.Errorf("expected ON DELETE CASCADE, got %q", stmt)
	}
}

func TestGenerateForeignKeyConstraints_EmitsCompositeForeignKey(t *testing.T) {
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

	statements, skipped := GenerateForeignKeyConstraints(cfg)

	if len(skipped) != 0 {
		t.Fatalf("expected no skipped foreign keys, got %v", skipped)
	}
	if len(statements) != 1 {
		t.Fatalf("expected 1 statement, got %d: %v", len(statements), statements)
	}
	if !strings.Contains(statements[0], `FOREIGN KEY ("x", "y")`) || !strings.Contains(statements[0], `REFERENCES "parents" ("a", "b")`) {
		t.Errorf("expected a composite foreign key clause, got %q", statements[0])
	}
}

func TestGenerateForeignKeyConstraints_SkipsForeignKeyReferencingAnExcludedTable(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"artists": {Include: false, ColumnOrder: []string{"ArtistId"}, Columns: map[string]config.ColumnConfig{"ArtistId": {TargetType: "integer"}}},
			"albums": {
				Include:     true,
				ColumnOrder: []string{"ArtistId"},
				Columns:     map[string]config.ColumnConfig{"ArtistId": {TargetType: "integer"}},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"ArtistId"}, RefTable: "artists", RefColumns: []string{"ArtistId"}},
				},
			},
		},
	}

	statements, skipped := GenerateForeignKeyConstraints(cfg)

	if len(statements) != 0 {
		t.Errorf("expected no statements, got %v", statements)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %v", skipped)
	}
}

func TestGenerateForeignKeyConstraints_SkipsForeignKeyReferencingADroppedColumn(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"parents": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "__drop__"}},
			},
			"children": {
				Include:     true,
				ColumnOrder: []string{"parent_id"},
				Columns:     map[string]config.ColumnConfig{"parent_id": {TargetType: "integer"}},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"parent_id"}, RefTable: "parents", RefColumns: []string{"id"}},
				},
			},
		},
	}

	statements, skipped := GenerateForeignKeyConstraints(cfg)

	if len(statements) != 0 {
		t.Errorf("expected no statements, got %v", statements)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %v", skipped)
	}
}

func TestGenerateForeignKeyConstraints_SkipsForeignKeyOnADroppedLocalColumn(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"parents": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
			"children": {
				Include:     true,
				ColumnOrder: []string{"parent_id"},
				Columns:     map[string]config.ColumnConfig{"parent_id": {TargetType: "__drop__"}},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"parent_id"}, RefTable: "parents", RefColumns: []string{"id"}},
				},
			},
		},
	}

	statements, skipped := GenerateForeignKeyConstraints(cfg)

	if len(statements) != 0 {
		t.Errorf("expected no statements, got %v", statements)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped foreign key, got %v", skipped)
	}
}

func TestGenerateForeignKeyConstraints_TruncatesAndDisambiguatesLongConstraintNames(t *testing.T) {
	// A long table name plus a composite key's joined column names easily
	// exceeds Postgres's 63-byte NAMEDATALEN limit. Two different
	// composite foreign keys on the same long-named table, whose "fk_" +
	// table + "_" + columns names would truncate to the same 63-byte
	// prefix, must not collapse into the same constraint name (issue #36)
	// — Postgres would silently truncate both to the same identifier and
	// the second ALTER TABLE ... ADD CONSTRAINT would fail with
	// "constraint ... already exists".
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

	statements, skipped := GenerateForeignKeyConstraints(cfg)

	if len(skipped) != 0 {
		t.Fatalf("expected no skipped foreign keys, got %v", skipped)
	}
	if len(statements) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(statements), statements)
	}

	names := make(map[string]bool, len(statements))
	for _, stmt := range statements {
		start := strings.Index(stmt, "ADD CONSTRAINT \"") + len("ADD CONSTRAINT \"")
		end := strings.Index(stmt[start:], "\"")
		name := stmt[start : start+end]

		if len(name) > 63 {
			t.Errorf("constraint name %q is %d bytes, exceeds Postgres's 63-byte NAMEDATALEN limit", name, len(name))
		}
		if names[name] {
			t.Errorf("constraint name %q was generated for more than one foreign key; Postgres would reject the second ADD CONSTRAINT as a duplicate", name)
		}
		names[name] = true
	}
}

func TestGenerateForeignKeyConstraints_OmitsNoActionClauses(t *testing.T) {
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"parents": {
				Include:     true,
				ColumnOrder: []string{"id"},
				Columns:     map[string]config.ColumnConfig{"id": {TargetType: "integer"}},
			},
			"children": {
				Include:     true,
				ColumnOrder: []string{"parent_id"},
				Columns:     map[string]config.ColumnConfig{"parent_id": {TargetType: "integer"}},
				ForeignKeys: []config.ForeignKey{
					{Columns: []string{"parent_id"}, RefTable: "parents", RefColumns: []string{"id"}, OnDelete: "NO ACTION", OnUpdate: "NO ACTION"},
				},
			},
		},
	}

	statements, _ := GenerateForeignKeyConstraints(cfg)
	if len(statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(statements))
	}
	if strings.Contains(statements[0], "NO ACTION") {
		t.Errorf("expected NO ACTION clauses to be omitted (Postgres's own default), got %q", statements[0])
	}
}
