// Package pipeline wires together sqlitereader, profiler, and resolver into
// the orchestration behind `migrate profile`: read schema, sample and
// profile every column, resolve or flag each one, and produce a draft
// config.MigrationConfig plus any resolver.UnresolvedCases.
package pipeline

import (
	"database/sql"
	"fmt"
	"strings"

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
					TargetType:   fallbackType(col.DeclaredType),
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

// fallbackType implements SQLite's own type-affinity rules
// (https://www.sqlite.org/datatype3.html#determination_of_column_affinity)
// to map a declared type with no heuristic opinion to a reasonable default
// Postgres type.
func fallbackType(declared string) string {
	d := strings.ToUpper(declared)
	switch {
	case strings.Contains(d, "INT"):
		return "integer"
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return "text"
	case strings.Contains(d, "BLOB"), d == "":
		return "bytea"
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return "double precision"
	default:
		return "numeric"
	}
}
