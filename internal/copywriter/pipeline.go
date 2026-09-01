package copywriter

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/sqlitereader"
)

// errClosed is StreamTable's callback's way of unwinding early once Close
// has been called — it's never surfaced to a caller (Close doesn't record
// it, and by the time it would matter TableSource has already been
// abandoned), it only exists to make sqlitereader.StreamTable return so its
// deferred rows.Close() runs and the SQLite cursor is released.
var errClosed = errors.New("table source closed")

// TableSource streams one table's rows from SQLite, applying each column's
// configured Transform, and satisfies pgx's CopyFromSource interface
// (Next() bool, Values() ([]any, error), Err() error) — the pull side of a
// producer/consumer pipeline that never buffers a full table in memory.
type TableSource struct {
	rowsCh   chan []any
	errCh    chan error
	done     chan struct{}
	doneOnce sync.Once
	current  []any
	err      error
	onRow    func()
}

// NewTableSource starts streaming table in the background. Columns marked
// __drop__ in tc are excluded from every row.
func NewTableSource(db *sql.DB, table string, tc config.TableConfig) *TableSource {
	columns := ddl.IncludedColumns(tc)
	ts := &TableSource{
		rowsCh: make(chan []any, 100),
		errCh:  make(chan error, 1),
		done:   make(chan struct{}),
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
			select {
			case ts.rowsCh <- transformed:
				return nil
			case <-ts.done:
				// The consumer has abandoned this source (e.g. pgx gave
				// up mid-COPY after a server-side failure). Unwind out of
				// StreamTable instead of blocking here forever, so its
				// deferred rows.Close() runs and the SQLite cursor and
				// this goroutine are released.
				return errClosed
			}
		})
		if !errors.Is(err, errClosed) {
			ts.errCh <- err
		}
	}()

	return ts
}

// Close signals the producer goroutine to stop and release its SQLite
// cursor. Callers that abandon a TableSource before draining it via Next()
// — as LoadTable does whenever pgx's CopyFrom returns early — must call
// Close so the goroutine doesn't leak parked on a full rowsCh. Close is a
// no-op from Next's point of view: it never itself sets Err(). Close is
// safe to call more than once — a second call is a no-op.
func (ts *TableSource) Close() {
	ts.doneOnce.Do(func() {
		close(ts.done)
	})
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
