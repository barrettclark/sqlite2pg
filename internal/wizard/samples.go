package wizard

import (
	"database/sql"
	"fmt"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/sqlitereader"
)

// sampleGridData re-reads the source SQLite file directly to build a
// data-preview grid — synchronized rows (not independently-sampled
// columns) plus each table's real total row count — the way many
// CSV/DB import wizards (e.g. DB Browser for SQLite) preview actual data
// rather than just a type name. Best-effort: if the source file can't be
// opened (moved, deleted, permissions), returns an empty GridData rather
// than failing — the preview is a display nicety, not something review
// correctness depends on.
func sampleGridData(cfg *config.MigrationConfig, limit int) GridData {
	grid := GridData{}

	db, err := sql.Open("sqlite", cfg.Source.Path)
	if err != nil {
		return grid
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return grid
	}

	for tableName, tc := range cfg.Tables {
		if !tc.Include {
			continue
		}
		columns := ddl.IncludedColumns(tc)
		if len(columns) == 0 {
			continue
		}

		rawRows, err := sqlitereader.SampleRows(db, tableName, columns, limit)
		if err != nil {
			continue
		}
		rows := make([][]string, len(rawRows))
		for i, raw := range rawRows {
			formatted := make([]string, len(raw))
			for j, v := range raw {
				formatted[j] = formatSampleValue(v)
			}
			rows[i] = formatted
		}

		rowCount, err := sqlitereader.CountRows(db, tableName)
		if err != nil {
			rowCount = len(rows)
		}

		grid[tableName] = TablePreview{Rows: rows, RowCount: rowCount}
	}

	return grid
}

// formatSampleValue renders a raw sampled value for display, truncating
// long text/binary values so the review grid stays scannable.
func formatSampleValue(v any) string {
	const maxLen = 40
	switch val := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(val))
	}
	s := fmt.Sprintf("%v", v)
	if len(s) > maxLen {
		return s[:maxLen] + "…"
	}
	return s
}
