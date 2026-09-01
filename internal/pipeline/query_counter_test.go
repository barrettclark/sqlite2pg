package pipeline

import (
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	msqlite "modernc.org/sqlite"
)

// queryCounter wraps modernc.org/sqlite to count how many times a plain,
// unfiltered, unordered full-table scan (exactly the shape
// sqlitereader.StreamTable emits: `SELECT ... FROM "table"`, no WHERE, no
// ORDER BY) is issued against a given table. This is the direct evidence
// issue #55's fix requires: a table with several auto-approving,
// transform-bearing columns must trigger exactly one such scan, not one
// per column — a test that only checked the final decided config could
// pass whether or not the batching actually happened, so this counts the
// actual queries sent to SQLite instead.
type queryCounter struct {
	mu     sync.Mutex
	counts map[string]int // table name -> number of matching full-table-scan queries
}

func newQueryCounter() *queryCounter {
	return &queryCounter{counts: map[string]int{}}
}

// fullTableScanPattern matches StreamTable's exact query shape for a
// quoted table name: SELECT <cols> FROM "table" with nothing after it
// (distinguishing it from SampleRows'/SampleColumn's `ORDER BY RANDOM()
// LIMIT ?` and SampleNonNullColumn's `WHERE ... ORDER BY RANDOM()`).
var fullTableScanPattern = regexp.MustCompile(`(?is)^SELECT .+ FROM "([^"]+)"\s*$`)

func (qc *queryCounter) record(query string) {
	m := fullTableScanPattern.FindStringSubmatch(strings.TrimSpace(query))
	if m == nil {
		return
	}
	qc.mu.Lock()
	defer qc.mu.Unlock()
	qc.counts[m[1]]++
}

func (qc *queryCounter) countFor(table string) int {
	qc.mu.Lock()
	defer qc.mu.Unlock()
	return qc.counts[table]
}

// countingDriver is registered exactly once for the whole test binary
// (sql.Register panics on a duplicate name), but each test needs its own
// independent *queryCounter — so it keeps a registry keyed by dsn (each
// test's t.TempDir() path is unique), populated just before that test's
// sql.Open call.
type countingDriver struct {
	inner *msqlite.Driver

	mu       sync.Mutex
	counters map[string]*queryCounter
}

func (d *countingDriver) registerCounter(dsn string, qc *queryCounter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counters == nil {
		d.counters = map[string]*queryCounter{}
	}
	d.counters[dsn] = qc
}

func (d *countingDriver) counterFor(dsn string) *queryCounter {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.counters[dsn]
}

func (d *countingDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: c, qc: d.counterFor(name)}, nil
}

// countingConn wraps a driver.Conn, intercepting only Prepare — embedding
// the driver.Conn interface (rather than the concrete modernc type) means
// no other optional interface (QueryerContext, etc.) is promoted, so
// database/sql always routes every query through Prepare, where it's
// recorded, then straight through to the real, unwrapped driver.Stmt.
type countingConn struct {
	driver.Conn
	qc *queryCounter
}

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	c.qc.record(query)
	return c.Conn.Prepare(query)
}

var (
	registerCountingDriverOnce sync.Once
	sharedCountingDriver       = &countingDriver{inner: &msqlite.Driver{}}
)

// openCountingTestDB is openTestDB, but returns a *sql.DB whose queries are
// also tallied by the returned *queryCounter.
func openCountingTestDB(t *testing.T, ddl string) (*sql.DB, string, *queryCounter) {
	t.Helper()
	qc := newQueryCounter()
	registerCountingDriverOnce.Do(func() {
		sql.Register("sqlite-counting", sharedCountingDriver)
	})

	path := filepath.Join(t.TempDir(), "test.db")
	sharedCountingDriver.registerCounter(path, qc)
	db, err := sql.Open("sqlite-counting", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// countingDriver wraps a bare *msqlite.Driver{}, whose zero value has
	// none of the process-wide registrations (functions, hooks) the
	// package-registered "sqlite" driver carries — but this codebase's
	// SQL is plain standard SQL/DDL that needs none of those.
	db.SetMaxOpenConns(1) // one physical conn -> deterministic query recording, no cross-conn ordering surprises
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("exec ddl: %v", err)
	}
	// The counting connection above records DDL/DML prepares too, but
	// fullTableScanPattern only matches the exact StreamTable shape, so
	// CREATE/INSERT statements are silently ignored — no reset needed.
	return db, path, qc
}
