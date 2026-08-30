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
	// If CopyFrom returns early — success or a mid-COPY failure — signal
	// src's producer goroutine to stop rather than leaving it parked
	// forever on a full rowsCh (issue #28).
	defer src.Close()

	columns := ddl.IncludedColumns(tc)
	ids := ddl.PostgresColumnNames(tc)
	pgColumns := make([]string, len(columns))
	for i, name := range columns {
		pgColumns[i] = ids[name]
	}
	n, err := conn.CopyFrom(ctx, pgx.Identifier{dbTable}, pgColumns, src)
	if err != nil {
		return n, fmt.Errorf("copying into %s: %w", dbTable, err)
	}
	return n, nil
}
