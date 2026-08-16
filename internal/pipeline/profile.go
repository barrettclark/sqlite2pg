// Package pipeline wires together sqlitereader, profiler, and resolver into
// the orchestration behind `migrate profile`: read schema, sample and
// profile every column, resolve or flag each one, and produce a draft
// config.MigrationConfig plus any resolver.UnresolvedCases.
package pipeline

import (
	"database/sql"
	"fmt"
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
		tc := config.TableConfig{Include: true, Columns: map[string]config.ColumnConfig{}}
		for _, col := range table.Columns {
			tc.ColumnOrder = append(tc.ColumnOrder, col.Name)
			meta := profiler.ColumnMeta{Table: table.Name, Name: col.Name, DeclaredType: col.DeclaredType}
			samples, err := sqlitereader.SampleColumn(db, table.Name, col.Name, sampleSize)
			if err != nil {
				return nil, fmt.Errorf("sampling %s.%s: %w", table.Name, col.Name, err)
			}
			findings := profiler.Default.ProfileColumn(meta, samples)

			var cc config.ColumnConfig
			if len(findings) == 0 {
				cc = config.ColumnConfig{
					DeclaredType: col.DeclaredType,
					TargetType:   fallbackTypeFor(col.DeclaredType, samples),
					Confidence:   0.99,
					Source:       "heuristic:default_passthrough",
					Rationale:    "no heuristic had an opinion; passed through via SQLite type affinity",
					Reviewed:     false,
				}
			} else {
				best, needsReview := resolver.Decide(findings, threshold)
				cc = config.ColumnConfig{
					DeclaredType: col.DeclaredType,
					TargetType:   best.SuggestedType,
					Transform:    best.TransformExpr,
					Confidence:   best.Confidence,
					Source:       "heuristic:" + best.Heuristic,
					Rationale:    best.Rationale,
					Reviewed:     false,
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
	for _, v := range samples {
		switch v.(type) {
		case int64, int:
			return "integer"
		case float64, float32:
			return "double precision"
		case time.Time:
			return "timestamptz"
		case []byte:
			return "bytea"
		case string:
			return "text"
		}
	}
	return fallbackTypeFromDeclared(declared)
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
