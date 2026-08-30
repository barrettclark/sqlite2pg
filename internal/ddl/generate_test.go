package ddl

import (
	"regexp"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

func TestGenerateCreateTable_EmitsColumnsInDeclaredOrder(t *testing.T) {
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"bike_id", "last_reported", "is_installed"},
		Columns: map[string]config.ColumnConfig{
			"bike_id":       {TargetType: "integer"},
			"last_reported": {TargetType: "timestamptz"},
			"is_installed":  {TargetType: "boolean"},
		},
	}

	ddl := GenerateCreateTable("bikes", tc)

	idxID := strings.Index(ddl, "bike_id")
	idxLR := strings.Index(ddl, "last_reported")
	idxII := strings.Index(ddl, "is_installed")
	if !(idxID < idxLR && idxLR < idxII) {
		t.Errorf("expected columns in declared order, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, `CREATE TABLE "bikes"`) {
		t.Errorf("expected quoted table name in CREATE TABLE, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, `"last_reported" timestamptz`) {
		t.Errorf("expected last_reported timestamptz column definition, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_EmitsParameterizedVarcharTypeVerbatim(t *testing.T) {
	// varchar(N) suggestions (issue #7) are just another TargetType string
	// — no special-cased DDL handling needed, the same as any other type.
	tc := config.TableConfig{
		ColumnOrder: []string{"first_name"},
		Columns: map[string]config.ColumnConfig{
			"first_name": {TargetType: "varchar(45)"},
		},
	}

	ddl := GenerateCreateTable("customers", tc)

	if !strings.Contains(ddl, `"first_name" varchar(45)`) {
		t.Errorf("expected first_name varchar(45) column definition, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_EmitsInlinePrimaryKey(t *testing.T) {
	tc := config.TableConfig{
		ColumnOrder: []string{"station_id", "num_bikes_available"},
		Columns: map[string]config.ColumnConfig{
			"station_id":          {TargetType: "text", PrimaryKeySeq: 1},
			"num_bikes_available": {TargetType: "integer"},
		},
	}

	ddl := GenerateCreateTable("bikes", tc)

	if !strings.Contains(ddl, `PRIMARY KEY ("station_id")`) {
		t.Errorf("expected an inline PRIMARY KEY clause, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_EmitsCompositePrimaryKeyInDeclaredSeqOrder(t *testing.T) {
	tc := config.TableConfig{
		ColumnOrder: []string{"PlaylistId", "TrackId"},
		Columns: map[string]config.ColumnConfig{
			"PlaylistId": {TargetType: "integer", PrimaryKeySeq: 1},
			"TrackId":    {TargetType: "integer", PrimaryKeySeq: 2},
		},
	}

	ddl := GenerateCreateTable("playlist_track", tc)

	if !strings.Contains(ddl, `PRIMARY KEY ("PlaylistId", "TrackId")`) {
		t.Errorf("expected composite primary key in seq order, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_CompositePrimaryKeyRespectsSeqNotColumnOrder(t *testing.T) {
	// Declared column order and primary key seq order can differ — DDL
	// generation must follow seq, not ColumnOrder.
	tc := config.TableConfig{
		ColumnOrder: []string{"b", "a"},
		Columns: map[string]config.ColumnConfig{
			"a": {TargetType: "integer", PrimaryKeySeq: 1},
			"b": {TargetType: "integer", PrimaryKeySeq: 2},
		},
	}

	ddl := GenerateCreateTable("t", tc)

	if !strings.Contains(ddl, `PRIMARY KEY ("a", "b")`) {
		t.Errorf("expected primary key ordered by seq (a, b), not column order (b, a), got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_NoPrimaryKeyClauseWhenNoColumnIsAPrimaryKey(t *testing.T) {
	tc := config.TableConfig{
		ColumnOrder: []string{"a"},
		Columns:     map[string]config.ColumnConfig{"a": {TargetType: "integer"}},
	}

	ddl := GenerateCreateTable("t", tc)

	if strings.Contains(ddl, "PRIMARY KEY") {
		t.Errorf("expected no PRIMARY KEY clause, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_DisambiguatesColumnsCollidingAfter63ByteTruncation(t *testing.T) {
	// Postgres truncates identifiers to 63 bytes (NAMEDATALEN). Two source
	// columns identical in their first 63 bytes but differing after that
	// (issue #21, reproduced by the collision.db fixture) must not collide
	// in the generated DDL — CREATE TABLE would otherwise fail with
	// "column ... specified more than once" (SQLSTATE 42701).
	long := strings.Repeat("a", 60) + "_bbb" // 64 bytes
	colX := long + "x"                       // shares first 63 bytes with colY
	colY := long + "y"

	tc := config.TableConfig{
		ColumnOrder: []string{colX, colY},
		Columns: map[string]config.ColumnConfig{
			colX: {TargetType: "integer"},
			colY: {TargetType: "text"},
		},
	}

	ddlText := GenerateCreateTable("products", tc)

	quoted := regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(ddlText, -1)
	seen := map[string]bool{}
	for _, m := range quoted {
		ident := m[1]
		if ident == "products" {
			continue
		}
		if len(ident) > 63 {
			t.Errorf("emitted identifier %q exceeds Postgres's 63-byte limit", ident)
		}
		if seen[ident] {
			t.Fatalf("emitted duplicate identifier %q — colliding columns were not disambiguated, got:\n%s", ident, ddlText)
		}
		seen[ident] = true
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 distinct column identifiers, got %d, DDL:\n%s", len(seen), ddlText)
	}
}

func TestGenerateCreateTable_QuotesEmbeddedDoubleQuoteAsSQLIdentifier(t *testing.T) {
	// A column name containing an embedded double quote (issue #26) must be
	// quoted the way SQL identifier quoting requires — the inner quote
	// doubled — not the way Go's %q escapes it with a backslash, which
	// Postgres rejects as a syntax error at the first inner quote.
	tc := config.TableConfig{
		ColumnOrder: []string{`Total "Disability" Recipients`},
		Columns: map[string]config.ColumnConfig{
			`Total "Disability" Recipients`: {TargetType: "text"},
		},
	}

	ddl := GenerateCreateTable("counties", tc)

	if strings.Contains(ddl, `\"`) {
		t.Errorf("expected no backslash-escaped quotes (Go %%q style), got:\n%s", ddl)
	}
	if !strings.Contains(ddl, `"Total ""Disability"" Recipients" text`) {
		t.Errorf("expected doubled-quote SQL identifier quoting, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_ExcludesDroppedColumns(t *testing.T) {
	tc := config.TableConfig{
		ColumnOrder: []string{"OBJECTID", "SHAPE"},
		Columns: map[string]config.ColumnConfig{
			"OBJECTID": {TargetType: "integer"},
			"SHAPE":    {TargetType: "__drop__"},
		},
	}

	ddl := GenerateCreateTable("SchoolSites2425", tc)

	if strings.Contains(ddl, "SHAPE") {
		t.Errorf("expected __drop__ column to be excluded from DDL, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "OBJECTID") {
		t.Errorf("expected OBJECTID to be included, got:\n%s", ddl)
	}
}
