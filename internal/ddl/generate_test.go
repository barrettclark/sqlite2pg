package ddl

import (
	"errors"
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

	ddl, err := GenerateCreateTable("bikes", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

func TestGenerateCreateTable_EmitsArrayTargetTypeVerbatim(t *testing.T) {
	// uuid[] (issue #12, the uuid_list heuristic) is the first array
	// target type this tool supports — GenerateCreateTable has no
	// allowlist of TargetType strings, so it should just pass the
	// bracketed type straight through into the column definition like
	// any other type.
	tc := config.TableConfig{
		Include:     true,
		ColumnOrder: []string{"id", "composer_ids"},
		Columns: map[string]config.ColumnConfig{
			"id":           {TargetType: "integer", PrimaryKeySeq: 1},
			"composer_ids": {TargetType: "uuid[]"},
		},
	}

	ddl, err := GenerateCreateTable("albums", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(ddl, `"composer_ids" uuid[]`) {
		t.Errorf("expected composer_ids uuid[] column definition, got:\n%s", ddl)
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

	ddl, err := GenerateCreateTable("customers", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(ddl, `"first_name" varchar(45)`) {
		t.Errorf("expected first_name varchar(45) column definition, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_EmitsNotNullForSourceNotNullColumns(t *testing.T) {
	// Issue #34: a source `NOT NULL` column must produce a Postgres
	// column also declared NOT NULL, matching source constraints instead
	// of silently dropping them.
	tc := config.TableConfig{
		ColumnOrder: []string{"id", "email", "nickname"},
		Columns: map[string]config.ColumnConfig{
			"id":       {TargetType: "integer", NotNull: true},
			"email":    {TargetType: "text", NotNull: true},
			"nickname": {TargetType: "text"},
		},
	}

	ddl, err := GenerateCreateTable("users", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(ddl, `"email" text NOT NULL`) {
		t.Errorf("expected email column declared NOT NULL, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, `"id" integer NOT NULL`) {
		t.Errorf("expected id column declared NOT NULL, got:\n%s", ddl)
	}
	if strings.Contains(ddl, `"nickname" text NOT NULL`) {
		t.Errorf("expected nickname column to stay nullable, got:\n%s", ddl)
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

	ddl, err := GenerateCreateTable("bikes", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	ddl, err := GenerateCreateTable("playlist_track", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	ddl, err := GenerateCreateTable("t", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(ddl, `PRIMARY KEY ("a", "b")`) {
		t.Errorf("expected primary key ordered by seq (a, b), not column order (b, a), got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_NoPrimaryKeyClauseWhenNoColumnIsAPrimaryKey(t *testing.T) {
	tc := config.TableConfig{
		ColumnOrder: []string{"a"},
		Columns:     map[string]config.ColumnConfig{"a": {TargetType: "integer"}},
	}

	ddl, err := GenerateCreateTable("t", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	ddlText, err := GenerateCreateTable("products", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	ddl, err := GenerateCreateTable("counties", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

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

	ddl, err := GenerateCreateTable("SchoolSites2425", tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(ddl, "SHAPE") {
		t.Errorf("expected __drop__ column to be excluded from DDL, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "OBJECTID") {
		t.Errorf("expected OBJECTID to be included, got:\n%s", ddl)
	}
}

func TestGenerateCreateTable_ErrorsWhenEveryColumnIsDropped(t *testing.T) {
	// An Esri table whose only column is a geometryblob mapped to
	// __drop__ ends up with zero included columns. GenerateCreateTable
	// must not emit `CREATE TABLE "t" (\n\n);` — Postgres rejects that as
	// a syntax error (issue #30) — it must signal the caller instead.
	tc := config.TableConfig{
		ColumnOrder: []string{"SHAPE"},
		Columns: map[string]config.ColumnConfig{
			"SHAPE": {TargetType: "__drop__"},
		},
	}

	stmt, err := GenerateCreateTable("geometry_only", tc)

	if err == nil {
		t.Fatalf("expected an error, got DDL:\n%s", stmt)
	}
	if stmt != "" {
		t.Errorf("expected no DDL on error, got:\n%s", stmt)
	}
	if !errors.Is(err, ErrNoIncludedColumns) {
		t.Errorf("expected ErrNoIncludedColumns, got: %v", err)
	}
	if errors.Is(err, ErrMissingColumnOrder) {
		t.Errorf("an intentionally all-dropped table is not a missing-column_order config bug, got: %v", err)
	}
}

func TestGenerateCreateTable_ErrorsWhenColumnOrderEmptyButColumnsPopulated(t *testing.T) {
	// column_order is `omitempty` in the YAML schema, so a hand-trimmed
	// config can lose the key entirely while Columns still has entries —
	// IncludedColumns then returns nil just like the legitimate
	// all-dropped case above, but this one is almost certainly a config
	// bug, not an intentionally column-less table, so it must be
	// distinguishable and reported differently.
	tc := config.TableConfig{
		Columns: map[string]config.ColumnConfig{
			"bike_id": {TargetType: "integer"},
		},
	}

	stmt, err := GenerateCreateTable("bikes", tc)

	if err == nil {
		t.Fatalf("expected an error, got DDL:\n%s", stmt)
	}
	if stmt != "" {
		t.Errorf("expected no DDL on error, got:\n%s", stmt)
	}
	if !errors.Is(err, ErrMissingColumnOrder) {
		t.Errorf("expected ErrMissingColumnOrder, got: %v", err)
	}
}

func TestGenerateCreateTable_ErrorsWhenNoColumnsAtAll(t *testing.T) {
	// A table config with neither Columns nor ColumnOrder populated (e.g.
	// a table with genuinely zero source columns) is the same "nothing
	// to create" case as everything being dropped, not the
	// missing-column_order config bug — there's nothing indicating
	// column_order was ever meant to be populated.
	tc := config.TableConfig{}

	_, err := GenerateCreateTable("empty", tc)

	if !errors.Is(err, ErrNoIncludedColumns) {
		t.Errorf("expected ErrNoIncludedColumns, got: %v", err)
	}
	if errors.Is(err, ErrMissingColumnOrder) {
		t.Errorf("expected not to be classified as a missing-column_order bug, got: %v", err)
	}
}
