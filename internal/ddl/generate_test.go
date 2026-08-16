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
