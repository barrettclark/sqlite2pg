package ddl

import (
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
