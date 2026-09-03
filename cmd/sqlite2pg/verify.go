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
// actually work correctly", meant to be run after every `sqlite2pg load`,
// not just as a one-off audit.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pgURL := fs.String("pg", "", "Postgres server URL, e.g. postgres://user@localhost:5432/?sslmode=disable (required; no database name — the database to verify is read from <config>.state.json, the one `sqlite2pg load` provisioned)")
	outPath := fs.String("out", "", "path to write the verification report (default: print to stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: sqlite2pg verify --pg url [--out report] <source.db> <config.migration.yaml>")
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

	// The database to connect to is whatever `sqlite2pg load` (or `sqlite2pg
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
		return fmt.Errorf("verify requires having run `sqlite2pg load` (or `sqlite2pg load --resume`) against %s first — no database is recorded in %s", configPath, statePath)
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

	reportOut := io.Writer(os.Stdout)
	var reportFile *os.File
	var ew *errWriter
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("creating report file %s: %w", *outPath, err)
		}
		// Latch the first write error so a full disk (or any I/O failure)
		// while writing the --out file doesn't leave a truncated report
		// with a zero exit code (issue #136). Only for --out; the stdout
		// path is left byte-identical to before.
		reportFile = f
		ew = &errWriter{w: f}
		reportOut = ew
	}

	summary, err := verifyLoadedTables(ctx, sourceDB, conn, cfg, os.Stdout, reportOut)
	if err != nil {
		if reportFile != nil {
			reportFile.Close()
		}
		return err
	}

	var reportErr error
	if reportFile != nil {
		closeErr := reportFile.Close()
		switch {
		case ew.err != nil:
			reportErr = fmt.Errorf("writing report to %s: %w", *outPath, ew.err)
		case closeErr != nil:
			reportErr = fmt.Errorf("writing report to %s: %w", *outPath, closeErr)
		}
	}

	lines, err := verifyOutcome(summary, *outPath, reportErr)
	for _, l := range lines {
		fmt.Println(l)
	}
	return err
}

// verifyOutcome turns the verification summary plus any --out report write
// error into runVerify's result: the success lines to print (nil on
// failure) and the error to return. The verification verdict always wins
// — a report-file write failure must never shadow "your data is wrong"
// (issue #144) — and "report written to" prints only on full success,
// after the verdict is known.
func verifyOutcome(summary verifySummary, outPath string, reportErr error) ([]string, error) {
	if !summary.passed() {
		msg := fmt.Sprintf("verification FAILED: %d table(s) with row-count mismatches, %d value mismatch(es) across %d table(s) checked",
			summary.rowCountFailures, summary.totalMismatches, summary.tablesChecked)
		if reportErr != nil {
			return nil, fmt.Errorf("%s; also failed writing the report: %w", msg, reportErr)
		}
		return nil, errors.New(msg)
	}
	if reportErr != nil {
		return nil, reportErr
	}
	var lines []string
	if outPath != "" {
		lines = append(lines, fmt.Sprintf("report written to %s", outPath))
	}
	lines = append(lines, fmt.Sprintf("verification passed: %d table(s) checked, %d row(s) compared, 0 mismatches",
		summary.tablesChecked, summary.totalRowsCompared))
	return lines, nil
}

// verifyLoadedTables runs pipeline.VerifyTable for every included table in
// cfg, comparing sourceDB against conn, prints a "verifying <table>..."
// progress line per table to progressOut as it goes, and writes the full
// report (via writeVerifyReport) to reportOut. It's the single verification
// engine both the standalone `sqlite2pg verify` command and the automatic
// post-load verification path (`run`/`load --verify`, see
// postload_verify.go) call — so the two can never independently drift in
// what they check or how they report it, the same "two similar paths
// silently disagree" failure mode this project has already hit more than
// once (issues #40/#41, and the sortKeyFor/valuesMatch numeric-comparison
// saga during this very feature's own development).
func verifyLoadedTables(ctx context.Context, sourceDB *sql.DB, conn *pgx.Conn, cfg *config.MigrationConfig, progressOut io.Writer, reportOut io.Writer) (verifySummary, error) {
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

	// The identifier `sqlite2pg load` actually created this table under
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
		fmt.Fprintf(progressOut, "verifying %s...\n", name)
		result, err := pipeline.VerifyTable(ctx, sourceDB, conn, name, pgTableNames[name], tc)
		if err != nil {
			return verifySummary{}, fmt.Errorf("verifying %s: %w", name, err)
		}
		results = append(results, result)
	}

	summary := writeVerifyReport(reportOut, results, skipped)
	summary.tablesChecked = len(results)
	return summary, nil
}

// verifySummary is writeVerifyReport's aggregate across every table it
// wrote a section for — what runVerify needs to decide the process's exit
// code without re-deriving it from the per-table results a second time.
type verifySummary struct {
	rowCountFailures  int
	totalMismatches   int
	totalRowsCompared int
	tablesChecked     int
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

// errWriter wraps an io.Writer, dropping later writes once one fails and
// keeping the first error. writeVerifyReport does dozens of unchecked
// fmt.Fprint* calls; this catches an I/O failure on the --out file so the
// caller can report it instead of silently writing a truncated report.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err == nil && n < len(p) {
		// A short write with no error violates the io.Writer contract;
		// treat it as truncation rather than pass it up for fmt to
		// convert to io.ErrShortWrite (which writeVerifyReport drops).
		err = io.ErrShortWrite
	}
	e.err = err
	return n, err
}
