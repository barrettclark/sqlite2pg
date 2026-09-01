package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/jackc/pgx/v5"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/pipeline"
)

// runVerify streams every row and every included column from both the
// original SQLite source and its Postgres copy and confirms they agree —
// exhaustively, not a spot check. It's the tool's answer to "did the load
// actually work correctly", meant to be run after every `migrate load`,
// not just as a one-off audit.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pgURL := fs.String("pg", "", "Postgres server URL, e.g. postgres://user@localhost:5432/?sslmode=disable (required; no database name — the database to verify is read from <config>.state.json, the one `migrate load` provisioned)")
	outPath := fs.String("out", "", "path to write the verification report (default: print to stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: migrate verify --pg url [--out report] <source.db> <config.migration.yaml>")
	}
	if *pgURL == "" {
		return errors.New("--pg is required")
	}
	sourcePath := fs.Arg(0)
	configPath := fs.Arg(1)

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := ddl.ValidateTableConfigs(cfg); err != nil {
		return err
	}

	// The database to connect to is whatever `migrate load` (or `migrate
	// load --resume`) actually provisioned for this exact config — never
	// re-derived from the source filename, since that would only
	// coincidentally point at the right database (issue #19's same
	// reasoning applies here as it does to --resume).
	statePath := configPath + ".state.json"
	st, err := readState(statePath)
	if err != nil {
		return err
	}
	if st.Database == "" {
		return fmt.Errorf("verify requires having run `migrate load` (or `migrate load --resume`) against %s first — no database is recorded in %s", configPath, statePath)
	}

	connCfg, err := connConfigForDatabase(*pgURL, st.Database)
	if err != nil {
		return err
	}

	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return fmt.Errorf("connecting to Postgres database %s: %w", st.Database, err)
	}
	defer conn.Close(ctx)

	sourceDB, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", sourcePath, err)
	}
	defer sourceDB.Close()

	// Sorted for deterministic output, same convention executeLoad and
	// printDryRunDDL already follow (cfg.Tables is a Go map).
	var tableNames []string
	for name, tc := range cfg.Tables {
		if !tc.Include {
			continue
		}
		tableNames = append(tableNames, name)
	}
	sort.Strings(tableNames)

	// The identifier `migrate load` actually created this table under
	// (see ddl.PostgresTableNames/issue #44) — computed the same way over
	// the same full cfg, so a table that was disambiguated at load time is
	// looked up by the name that really exists rather than its raw source
	// name.
	pgTableNames := ddl.PostgresTableNames(cfg)

	var results []pipeline.TableVerifyResult
	var skipped []string
	for _, name := range tableNames {
		tc := cfg.Tables[name]
		// A table with zero included columns has nothing to verify —
		// same skip logic executeLoad applies before COPY (issue #30).
		if len(ddl.IncludedColumns(tc)) == 0 {
			skipped = append(skipped, name)
			continue
		}
		fmt.Printf("verifying %s...\n", name)
		result, err := pipeline.VerifyTable(ctx, sourceDB, conn, name, pgTableNames[name], tc)
		if err != nil {
			return fmt.Errorf("verifying %s: %w", name, err)
		}
		results = append(results, result)
	}

	out := io.Writer(os.Stdout)
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("creating report file %s: %w", *outPath, err)
		}
		defer f.Close()
		out = f
	}

	summary := writeVerifyReport(out, results, skipped)
	if *outPath != "" {
		fmt.Printf("report written to %s\n", *outPath)
	}

	if !summary.passed() {
		return fmt.Errorf("verification FAILED: %d table(s) with row-count mismatches, %d value mismatch(es) across %d table(s) checked",
			summary.rowCountFailures, summary.totalMismatches, len(results))
	}
	fmt.Printf("verification passed: %d table(s) checked, %d row(s) compared, 0 mismatches\n", len(results), summary.totalRowsCompared)
	return nil
}

// verifySummary is writeVerifyReport's aggregate across every table it
// wrote a section for — what runVerify needs to decide the process's exit
// code without re-deriving it from the per-table results a second time.
type verifySummary struct {
	rowCountFailures  int
	totalMismatches   int
	totalRowsCompared int
}

func (s verifySummary) passed() bool {
	return s.rowCountFailures == 0 && s.totalMismatches == 0
}

// writeVerifyReport writes a full verification report to w: an overall
// summary, then one section per table — its row-count check result, and
// (only if the full comparison ran) each mismatching column's count and
// capped example rows. It returns the aggregate verifySummary so the
// caller can decide pass/fail without re-scanning results.
func writeVerifyReport(w io.Writer, results []pipeline.TableVerifyResult, skipped []string) verifySummary {
	var summary verifySummary
	for _, r := range results {
		if r.RowCountMismatch {
			summary.rowCountFailures++
			continue
		}
		summary.totalRowsCompared += r.RowsCompared
		summary.totalMismatches += r.TotalMismatches()
	}

	fmt.Fprintln(w, "sqlite2pg verify report")
	fmt.Fprintln(w, "=======================")
	fmt.Fprintf(w, "tables checked:        %d\n", len(results))
	if len(skipped) > 0 {
		fmt.Fprintf(w, "tables skipped:        %d (no included columns — nothing to verify)\n", len(skipped))
	}
	fmt.Fprintf(w, "rows compared:         %d\n", summary.totalRowsCompared)
	fmt.Fprintf(w, "row-count mismatches:  %d\n", summary.rowCountFailures)
	fmt.Fprintf(w, "value mismatches:      %d\n", summary.totalMismatches)
	if summary.passed() {
		fmt.Fprintln(w, "result:                PASS")
	} else {
		fmt.Fprintln(w, "result:                FAIL")
	}
	fmt.Fprintln(w)

	for _, name := range skipped {
		fmt.Fprintf(w, "%s: skipped (no included columns)\n\n", name)
	}

	for _, r := range results {
		fmt.Fprintf(w, "%s:\n", r.Table)
		if r.RowCountMismatch {
			fmt.Fprintf(w, "  ROW COUNT MISMATCH: source has %d row(s), Postgres has %d row(s)\n", r.SourceRowCount, r.TargetRowCount)
			fmt.Fprintln(w, "  (full column comparison skipped)")
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "  row count OK: %d row(s)\n", r.SourceRowCount)
		if r.TotalMismatches() == 0 {
			fmt.Fprintf(w, "  PASS: all %d row(s) matched exactly\n", r.RowsCompared)
			fmt.Fprintln(w)
			continue
		}

		if !r.Ordered {
			fmt.Fprintln(w, "  NOTE: this table has no primary key, so rows were compared as an")
			fmt.Fprintln(w, "  unordered value comparison per column, not row-by-row — see below.")
		}

		var columns []string
		for col := range r.ColumnResults {
			columns = append(columns, col)
		}
		sort.Strings(columns)
		for _, col := range columns {
			cr := r.ColumnResults[col]
			fmt.Fprintf(w, "  MISMATCH %s.%s: %d of %d row(s) differ\n", r.Table, col, cr.MismatchCount, r.RowsCompared)
			for _, ex := range cr.Examples {
				if r.Ordered {
					fmt.Fprintf(w, "    row %d: source=%s expected=%s actual=%s\n",
						ex.RowIndex, formatVerifyValue(ex.Source), formatVerifyValue(ex.Expected), formatVerifyValue(ex.Actual))
				} else {
					fmt.Fprintf(w, "    sorted-comparison position %d (not a source row — no primary key to match rows by): expected=%s actual=%s\n",
						ex.RowIndex, formatVerifyValue(ex.Expected), formatVerifyValue(ex.Actual))
				}
			}
			if cr.MismatchCount > len(cr.Examples) {
				fmt.Fprintf(w, "    ... and %d more (showing first %d)\n", cr.MismatchCount-len(cr.Examples), len(cr.Examples))
			}
		}
		fmt.Fprintln(w)
	}

	return summary
}

// formatVerifyValue renders one source/expected/actual value for the
// report, with an explicit "NULL" for nil rather than the less readable
// "<nil>" fmt would otherwise produce.
func formatVerifyValue(v any) string {
	if v == nil {
		return "NULL"
	}
	return fmt.Sprintf("%v", v)
}
