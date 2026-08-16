package copywriter

import (
	"database/sql"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/sqlitereader"
)

// TableSource streams one table's rows from SQLite, applying each column's
// configured Transform, and satisfies pgx's CopyFromSource interface
// (Next() bool, Values() ([]any, error), Err() error) — the pull side of a
// producer/consumer pipeline that never buffers a full table in memory.
type TableSource struct {
	rowsCh  chan []any
	errCh   chan error
	current []any
	err     error
}

// NewTableSource starts streaming table in the background. Columns marked
// __drop__ in tc are excluded from every row.
func NewTableSource(db *sql.DB, table string, tc config.TableConfig) *TableSource {
	columns := ddl.IncludedColumns(tc)
	ts := &TableSource{
		rowsCh: make(chan []any, 100),
		errCh:  make(chan error, 1),
	}

	go func() {
		defer close(ts.rowsCh)
		err := sqlitereader.StreamTable(db, table, columns, func(row []profiler.Value) error {
			transformed := make([]any, len(row))
			for i, v := range row {
				out, err := Transform(tc.Columns[columns[i]].Transform, v)
				if err != nil {
					return err
				}
				transformed[i] = out
			}
			ts.rowsCh <- transformed
			return nil
		})
		ts.errCh <- err
	}()

	return ts
}

func (ts *TableSource) Next() bool {
	row, ok := <-ts.rowsCh
	if !ok {
		select {
		case err := <-ts.errCh:
			ts.err = err
		default:
		}
		return false
	}
	ts.current = row
	return true
}

func (ts *TableSource) Values() ([]any, error) {
	return ts.current, nil
}

func (ts *TableSource) Err() error {
	return ts.err
}
