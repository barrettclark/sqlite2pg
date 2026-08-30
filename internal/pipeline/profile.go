// Package pipeline wires together sqlitereader, profiler, and resolver into
// the orchestration behind `migrate profile`: read schema, sample and
// profile every column, resolve or flag each one, and produce a draft
// config.MigrationConfig plus any resolver.UnresolvedCases.
package pipeline

import (
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/resolver"
	"sqlite2pg/internal/sqlitereader"
)

// ProfileResult is the outcome of profiling a whole database.
type ProfileResult struct {
	Config     *config.MigrationConfig
	Unresolved []resolver.UnresolvedCase
}

// ProfileDatabase reads db's schema, samples up to sampleSize rows per
// column, runs every registered heuristic, and resolves each column via
// resolver.Decide against threshold. Columns no heuristic has an opinion on
// fall back to a declared-type passthrough mapping. The returned config is
// always a complete draft (every column gets a best-guess decision); columns
// that need human review are both marked reviewed: false and included in
// Unresolved.
func ProfileDatabase(db *sql.DB, sourcePath string, sampleSize int, threshold float64) (*ProfileResult, error) {
	tables, err := sqlitereader.ReadSchema(db)
	if err != nil {
		return nil, fmt.Errorf("reading schema: %w", err)
	}

	kind := "sqlite"
	if sqlitereader.IsEsriGeodatabase(tables) {
		kind = "esri_geodatabase"
	}
	tables = sqlitereader.FilterSystemTables(tables)

	sourceHash, err := config.HashFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("hashing source: %w", err)
	}

	cfg := &config.MigrationConfig{
		ConfigVersion: config.CurrentConfigVersion,
		Source: config.SourceInfo{
			Path:         sourcePath,
			SQLiteSHA256: sourceHash,
			Kind:         kind,
		},
		ToolVersion: "0.1.0",
		Tables:      map[string]config.TableConfig{},
	}

	var unresolved []resolver.UnresolvedCase

	suggestedFKs, err := inferForeignKeys(db, tables)
	if err != nil {
		return nil, fmt.Errorf("inferring foreign keys: %w", err)
	}

	for _, table := range tables {
		tc := config.TableConfig{
			Include:              true,
			Columns:              map[string]config.ColumnConfig{},
			ForeignKeys:          convertForeignKeys(table.ForeignKeys),
			SuggestedForeignKeys: suggestedFKs[table.Name],
		}

		columnNames := make([]string, len(table.Columns))
		for i, col := range table.Columns {
			columnNames[i] = col.Name
		}
		// One random-order scan samples every column in the table together,
		// instead of one random-order scan per column — both cheaper and,
		// since every column's sample comes from the same randomly-chosen
		// rows, exactly as representative per column as SampleColumn alone
		// would be.
		sampledRows, err := sqlitereader.SampleRows(db, table.Name, columnNames, sampleSize)
		if err != nil {
			return nil, fmt.Errorf("sampling %s: %w", table.Name, err)
		}
		columnSamples := transposeToColumns(sampledRows, len(columnNames))

		// A column that comes back entirely NULL in a full-size random
		// sample isn't necessarily entirely NULL in the table — it might
		// just be sparse enough that random chance missed every non-NULL
		// value (verified for real against a 99.5%-NULL column). A
		// heuristic that never sees a real value can't fire, so the
		// column silently falls through to the declared-type passthrough
		// instead of being flagged — no error, just a silently wrong
		// type. Rescue these with a second, targeted query for non-NULL
		// values only. If sampledRows came back shorter than sampleSize,
		// the table has no more rows than we already sampled — the
		// column really is all NULL, and there's nothing to rescue.
		if len(sampledRows) == sampleSize {
			for i, name := range columnNames {
				if hasNonNilValue(columnSamples[i]) {
					continue
				}
				rescued, err := sqlitereader.SampleNonNullColumn(db, table.Name, name, sampleSize)
				if err != nil {
					return nil, fmt.Errorf("rescuing sparse column %s.%s: %w", table.Name, name, err)
				}
				if len(rescued) > 0 {
					columnSamples[i] = rescued
				}
			}
		}

		varcharLens := varcharSuggestions(table.Columns)

		for i, col := range table.Columns {
			tc.ColumnOrder = append(tc.ColumnOrder, col.Name)
			samples := columnSamples[i]

			var extra []profiler.Finding
			if n, ok := varcharLens[col.Name]; ok {
				extra = append(extra, varcharFinding(n))
			}

			cc, uc, err := decideColumn(db, table.Name, col, samples, threshold, extra...)
			if err != nil {
				return nil, fmt.Errorf("deciding %s.%s: %w", table.Name, col.Name, err)
			}
			if uc != nil {
				unresolved = append(unresolved, *uc)
			}
			tc.Columns[col.Name] = cc
		}
		cfg.Tables[table.Name] = tc
	}

	return &ProfileResult{Config: cfg, Unresolved: unresolved}, nil
}

// transposeToColumns turns rows (each a slice of numCols values, one row
// per sampled record) into numCols column-major slices — what
// profiler.ProfileColumn expects for a single column, given what
// sqlitereader.SampleRows returns for a whole table. An empty rows (a table
// with no rows at all) yields numCols empty-but-non-nil slices, so every
// column still gets a (trivial) heuristic pass rather than being skipped.
func transposeToColumns(rows [][]profiler.Value, numCols int) [][]profiler.Value {
	columns := make([][]profiler.Value, numCols)
	for i := range columns {
		columns[i] = make([]profiler.Value, 0, len(rows))
	}
	for _, row := range rows {
		for i, v := range row {
			columns[i] = append(columns[i], v)
		}
	}
	return columns
}

// hasNonNilValue reports whether samples contains at least one non-NULL
// value.
func hasNonNilValue(samples []profiler.Value) bool {
	for _, v := range samples {
		if v != nil {
			return true
		}
	}
	return false
}

// convertForeignKeys copies sqlitereader's declared foreign keys into the
// config's shape unchanged — this is preserved source truth, not something
// a heuristic decides, so there's no confidence/rationale to attach.
func convertForeignKeys(fks []sqlitereader.ForeignKeyInfo) []config.ForeignKey {
	if len(fks) == 0 {
		return nil
	}
	converted := make([]config.ForeignKey, len(fks))
	for i, fk := range fks {
		converted[i] = config.ForeignKey{
			Columns:    fk.Columns,
			RefTable:   fk.RefTable,
			RefColumns: fk.RefColumns,
			OnDelete:   fk.OnDelete,
			OnUpdate:   fk.OnUpdate,
		}
	}
	return converted
}

// fallbackTypeFor maps a column with no heuristic opinion to a Postgres
// type, preferring the actual runtime type of its sampled values over the
// declared type name. This matters because a Go SQLite driver's scanned
// value type does not always match what the declared type name implies:
// modernc.org/sqlite returns float64 for a NUMERIC(10,2)-declared column
// (chinook.db's invoice_items.UnitPrice) and time.Time for a
// DATETIME-declared column (chinook.db's employees.BirthDate) — trusting
// the declared type name for either would produce a target type that
// can't actually hold the value pgx receives at COPY time. Only when no
// non-NULL sample is available (an empty table, or an all-NULL column) does
// this fall back to SQLite's declared-type affinity rules
// (https://www.sqlite.org/datatype3.html#determination_of_column_affinity),
// defaulting unmatched declared types to "text" since it's the one target
// that can never fail to hold an unknown value.
func fallbackTypeFor(declared string, samples []profiler.Value) string {
	var sawInt, sawFloat, sawTime, sawBytes, sawString, sawOutOfInt4Range bool
	for _, v := range samples {
		switch n := v.(type) {
		case int64:
			sawInt = true
			if n < math.MinInt32 || n > math.MaxInt32 {
				sawOutOfInt4Range = true
			}
		case int:
			sawInt = true
		case float64, float32:
			sawFloat = true
		case time.Time:
			sawTime = true
		case []byte:
			sawBytes = true
		case string:
			sawString = true
		}
	}

	switch {
	// SQLite's dynamic typing lets a single NUMERIC/DECIMAL-declared
	// column store some rows as INTEGER storage class and others as REAL
	// (e.g. 18 alongside 21.35) — if any sample was fractional, the whole
	// column needs double precision, not just the rows that happened to be
	// whole numbers.
	case sawFloat:
		return "double precision"
	case sawInt && sawOutOfInt4Range:
		// SQLite INTEGER holds the full 8-byte int64 range; Postgres
		// "integer" is only 4 bytes. A value outside int4 range (found via
		// dogfooding against sample-types.sqlite: -9007199254740992) fails
		// at COPY time with "integer" but fits "bigint".
		return "bigint"
	case sawInt:
		return "integer"
	case sawTime:
		return "timestamptz"
	case sawBytes:
		return "bytea"
	case sawString:
		return "text"
	default:
		return fallbackTypeFromDeclared(declared)
	}
}

// varcharLengthPattern matches a declared VARCHAR(N) type exactly (SQLite
// happily accepts extra tokens on a declared type, but the specific
// "VARCHAR(N)" shape is what MySQL-origin schema exports, the case this
// feature targets, actually produce).
var varcharLengthPattern = regexp.MustCompile(`(?i)^\s*VARCHAR\s*\(\s*(\d+)\s*\)\s*$`)

// varcharLength parses a VARCHAR(N) declared type, reporting false for
// anything else (bare VARCHAR, TEXT, CHARACTER, or a non-matching shape).
func varcharLength(declared string) (int, bool) {
	m := varcharLengthPattern.FindStringSubmatch(declared)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// varcharSuggestions returns, for every VARCHAR(N)-declared column in
// columns, its N — but only when this table's VARCHAR columns don't all
// share the same N. A shared length across every VARCHAR column in a
// table is the hallmark of a mechanical export default (the same
// VARCHAR(8000) stamped on every text column regardless of content); a
// table whose VARCHAR lengths actually vary looks like a real,
// deliberately-sized schema (e.g. VARCHAR(45) alongside VARCHAR(100), a
// genuine MySQL-origin export) worth suggesting as varchar(N) rather than
// defaulting to text. SQLite never enforces these lengths either way, so
// this is a suggestion for human review (see varcharFinding), never
// auto-applied.
func varcharSuggestions(columns []sqlitereader.ColumnInfo) map[string]int {
	lengths := map[string]int{}
	distinct := map[int]bool{}
	for _, col := range columns {
		if n, ok := varcharLength(col.DeclaredType); ok {
			lengths[col.Name] = n
			distinct[n] = true
		}
	}
	if len(distinct) <= 1 {
		return nil
	}
	return lengths
}

// varcharFinding builds the Finding that gets a VARCHAR(N) column
// suggested as varchar(N) instead of text. Its confidence is deliberately
// held below any normal auto-approve threshold — this issue (#7) requires
// a human confirm the length looks real before it's carried into the
// target schema, since a wrong carried-over length risks a load failure
// text would never have.
func varcharFinding(n int) profiler.Finding {
	return profiler.Finding{
		SuggestedType: fmt.Sprintf("varchar(%d)", n),
		Confidence:    0.5,
		Rationale:     fmt.Sprintf("declared VARCHAR(%d), and this table's VARCHAR column lengths vary rather than sharing one blanket value — the length looks like a real constraint, but SQLite never enforced it, so confirm before keeping it", n),
		Heuristic:     "varchar_length_preservation",
	}
}

func fallbackTypeFromDeclared(declared string) string {
	d := strings.ToUpper(declared)
	switch {
	case strings.Contains(d, "INT"):
		return "integer"
	case strings.Contains(d, "BLOB"):
		return "bytea"
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return "double precision"
	default:
		return "text"
	}
}
