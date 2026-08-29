// Package pipeline wires together sqlitereader, profiler, and resolver into
// the orchestration behind `migrate profile`: read schema, sample and
// profile every column, resolve or flag each one, and produce a draft
// config.MigrationConfig plus any resolver.UnresolvedCases.
package pipeline

import (
	"database/sql"
	"fmt"
	"math"
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

	for _, table := range tables {
		tc := config.TableConfig{
			Include:     true,
			Columns:     map[string]config.ColumnConfig{},
			ForeignKeys: convertForeignKeys(table.ForeignKeys),
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

		for i, col := range table.Columns {
			tc.ColumnOrder = append(tc.ColumnOrder, col.Name)
			meta := profiler.ColumnMeta{Table: table.Name, Name: col.Name, DeclaredType: col.DeclaredType}
			samples := columnSamples[i]
			findings := profiler.Default.ProfileColumn(meta, samples)

			var cc config.ColumnConfig
			if len(findings) == 0 {
				cc = config.ColumnConfig{
					DeclaredType:  col.DeclaredType,
					TargetType:    fallbackTypeFor(col.DeclaredType, samples),
					Confidence:    0.99,
					Source:        "heuristic:default_passthrough",
					Rationale:     "no heuristic had an opinion; passed through via SQLite type affinity",
					Reviewed:      false,
					PrimaryKeySeq: col.PrimaryKeySeq,
				}
			} else {
				best, needsReview := resolver.Decide(findings, threshold)
				cc = config.ColumnConfig{
					DeclaredType:  col.DeclaredType,
					TargetType:    best.SuggestedType,
					Transform:     best.TransformExpr,
					Confidence:    best.Confidence,
					Source:        "heuristic:" + best.Heuristic,
					Rationale:     best.Rationale,
					Reviewed:      false,
					PrimaryKeySeq: col.PrimaryKeySeq,
				}
				if needsReview {
					reason := fmt.Sprintf("confidence %.2f below auto-approve threshold %.2f, or heuristics disagreed", best.Confidence, threshold)
					unresolved = append(unresolved, resolver.UnresolvedCase{
						Table:        table.Name,
						Column:       col.Name,
						DeclaredType: col.DeclaredType,
						Samples:      samples,
						Findings:     findings,
						Reason:       reason,
					})
				}
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
