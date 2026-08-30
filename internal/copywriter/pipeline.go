package copywriter

import (
	"database/sql"
	"fmt"

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
	onRow   func()
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
					// A profiled decision is verified against the whole
					// table before being auto-approved (issue #13), so
					// this should be rare in practice — the residual case
					// is a column loaded via --force past a flagged,
					// below-threshold decision. Name the column and
					// suggest the fix rather than surfacing only the raw
					// transform error.
					return fmt.Errorf("%s.%s: %w (this column's type was chosen from a sample; consider re-profiling with a larger --sample-size, or run `migrate review` to override its type)", table, columns[i], err)
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

// OnRow registers fn to be called once per row Next() successfully pulls
// off the pipeline — after transform, right as pgx is about to receive it
// via Values(). Returns ts so it can be chained onto NewTableSource.
func (ts *TableSource) OnRow(fn func()) *TableSource {
	ts.onRow = fn
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
	if ts.onRow != nil {
		ts.onRow()
	}
	return true
}

func (ts *TableSource) Values() ([]any, error) {
	return ts.current, nil
}

func (ts *TableSource) Err() error {
	return ts.err
}
