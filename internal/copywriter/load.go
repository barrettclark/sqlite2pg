package copywriter

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
)

// compile-time assertion that TableSource satisfies pgx.CopyFromSource.
var _ pgx.CopyFromSource = (*TableSource)(nil)

// LoadTable streams table's rows into Postgres via the COPY protocol and
// returns the number of rows written. Verified against a real Postgres
// instance in the Tier 3 integration suite (build tag "integration"), not
// here — this function has no logic of its own beyond delegating to pgx.
func LoadTable(ctx context.Context, conn *pgx.Conn, dbTable string, tc config.TableConfig, src *TableSource) (int64, error) {
	columns := ddl.IncludedColumns(tc)
	n, err := conn.CopyFrom(ctx, pgx.Identifier{dbTable}, columns, src)
	if err != nil {
		return n, fmt.Errorf("copying into %s: %w", dbTable, err)
	}
	return n, nil
}
