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
