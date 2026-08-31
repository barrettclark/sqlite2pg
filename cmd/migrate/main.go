// Command migrate replaces pgloader for SQLite -> Postgres migrations: it
// profiles a source database, lets a human review ambiguous type
// decisions in a terminal review UI, and loads the result into Postgres via
// the COPY protocol.
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
	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/pipeline"
	_ "sqlite2pg/internal/profiler/heuristics"
	"sqlite2pg/internal/resolver"
	"sqlite2pg/internal/review"
	"sqlite2pg/internal/sqlitereader"
	"sqlite2pg/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: migrate <profile|review|load|verify|resolve> ...")
	}

	switch args[0] {
	case "run":
		return runRun(args[1:])
	case "profile":
		return runProfile(args[1:])
	case "review":
		return runReview(args[1:])
	case "load":
		return runLoad(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "resolve":
		return runResolve(args[1:])
	default:
		return fmt.Errorf("unknown command %q (expected run, profile, review, load, verify, or resolve)", args[0])
	}
}

// --- run -------------------------------------------------------------------

// runRun is the single-shot path: profile the source, open the review TUI,
// and — only if the human finishes the review rather than cancelling it —
// load the result into Postgres. This is `profile` + `review` + `load`
// collapsed into one command for the common case where a human is sitting
// at the terminal watching it happen, as opposed to the scriptable
// three-command flow (profile now, review later, load in CI, etc.).
func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	pgURL := fs.String("pg", "", "Postgres server URL, e.g. postgres://user@localhost:5432/?sslmode=disable (required; no database name — a fresh one is created per run)")
	sampleSize := fs.Int("sample-size", 500, "rows to sample per column")
	threshold := fs.Float64("threshold", 0.9, "confidence below which a column is highlighted as needing review")
	keepConfig := fs.Bool("keep-config", false, "keep the generated <source>.migration.yaml after the run instead of deleting it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate run --pg url [--sample-size N] [--threshold F] <source.db>")
	}
	if *pgURL == "" {
		return errors.New("--pg is required (use `migrate profile` + `migrate review` separately if you don't have a target yet)")
	}
	sourcePath := fs.Arg(0)

	db, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", sourcePath, err)
	}
	defer db.Close()

	result, err := pipeline.ProfileDatabase(db, sourcePath, *sampleSize, *threshold)
	if err != nil {
		return err
	}

	configPath := sourcePath + ".migration.yaml"
	if err := config.Save(result.Config, configPath); err != nil {
		return err
	}
	fmt.Printf("profiled %s: %d table(s), %d column(s) need review\n", sourcePath, len(result.Config.Tables), len(result.Unresolved))
	warnSkippedTables(result.SkippedTables)
	warnFilteredSystemTables(result.FilteredSystemTables)

	st, err := review.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	fmt.Println("opening review — f: finish & import, c: cancel")
	if err := tui.Run(context.Background(), st); err != nil {
		return err
	}

	switch st.Outcome() {
	case review.OutcomeCancelled:
		fmt.Println("cancelled — nothing was imported")
		return nil
	case review.OutcomeConfirmed:
		// fall through to load below
	default:
		return errors.New("review session ended without a decision")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := ddl.ValidateTableConfigs(cfg); err != nil {
		return err
	}

	statePath := configPath + ".state.json"
	connCfg, err := connectForLoad(context.Background(), *pgURL, sourcePath, false, statePath)
	if err != nil {
		return err
	}

	loadErr := executeLoad(cfg, connCfg, false, statePath)
	return cleanupConfigAfterLoad(loadErr, configPath, *keepConfig)
}

// cleanupConfigAfterLoad decides what becomes of a `run`-generated config
// once the load step has run its course (issue #38). The config is only
// ever removed after a load that actually succeeded — on any load error it
// is left in place, independent of --keep-config, so a user who hits a
// failure without having anticipated --keep-config up front can still
// inspect what was decided or retry via `migrate load --resume` against the
// surviving config and its state file. loadErr, when non-nil, is returned
// unchanged so callers still see the real failure.
func cleanupConfigAfterLoad(loadErr error, configPath string, keepConfig bool) error {
	if loadErr != nil {
		return loadErr
	}
	if keepConfig {
		return nil
	}
	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing generated config %s: %w", configPath, err)
	}
	return nil
}

// --- profile ---------------------------------------------------------------

func runProfile(args []string) error {
	fs := flag.NewFlagSet("profile", flag.ContinueOnError)
	out := fs.String("out", "", "path to write the draft migration config (default: <source>.migration.yaml)")
	sampleSize := fs.Int("sample-size", 500, "rows to sample per column")
	threshold := fs.Float64("threshold", 0.9, "confidence required to auto-approve a column")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate profile [--out path] [--sample-size N] [--threshold F] <source.db>")
	}
	sourcePath := fs.Arg(0)
	if *out == "" {
		*out = sourcePath + ".migration.yaml"
	}

	db, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", sourcePath, err)
	}
	defer db.Close()

	result, err := pipeline.ProfileDatabase(db, sourcePath, *sampleSize, *threshold)
	if err != nil {
		return err
	}
	if err := config.Save(result.Config, *out); err != nil {
		return err
	}
	fmt.Printf("wrote draft config to %s (%d table(s))\n", *out, len(result.Config.Tables))
	warnSkippedTables(result.SkippedTables)
	warnFilteredSystemTables(result.FilteredSystemTables)

	if len(result.Unresolved) > 0 {
		reportPath := *out + ".unresolved_report.yaml"
		fr := resolver.FileResolver{ReportPath: reportPath}
		_, resolveErr := fr.Resolve(context.Background(), result.Unresolved)
		fmt.Printf("%d column(s) need review — run `migrate review %s` or edit %s\n", len(result.Unresolved), *out, *out)
		return resolveErr
	}
	return nil
}

// warnSkippedTables prints a stderr warning for every table ReadSchema
// deliberately skipped (issue #29: a table backed by an unsupported SQLite
// virtual table module) — the generated config has no entry for these at
// all, so without this warning the only sign anything was skipped is a
// missing table discovered after the fact in Postgres.
func warnSkippedTables(skipped []sqlitereader.SkippedTable) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %d table(s) skipped (unsupported SQLite virtual table module) and left out of the config:\n", len(skipped))
	for _, st := range skipped {
		fmt.Fprintf(os.Stderr, "  - %s: %s\n", st.Name, st.Reason)
	}
}

// warnFilteredSystemTables prints a stderr warning for every table
// FilterSystemTables excluded as an Esri GDB_* or Spatialite st_* system
// table (issue #35) — the generated config has no entry for these at all,
// so without this warning the exclusion is invisible.
func warnFilteredSystemTables(filtered []sqlitereader.TableInfo) {
	if len(filtered) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: %d table(s) filtered out as Esri/Spatialite system tables and left out of the config:\n", len(filtered))
	for _, t := range filtered {
		fmt.Fprintf(os.Stderr, "  - %s\n", t.Name)
	}
}

// --- review ------------------------------------------------------------

func runReview(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	threshold := fs.Float64("threshold", 0.9, "confidence below which a column is highlighted as needing review")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate review [--threshold F] <config.migration.yaml>")
	}
	configPath := fs.Arg(0)

	st, err := review.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	return tui.Run(context.Background(), st)
}

// --- load ----------------------------------------------------------------

func runLoad(args []string) error {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	pgURL := fs.String("pg", "", "Postgres server URL, e.g. postgres://user@localhost:5432/?sslmode=disable (no database name — a fresh one is created per run)")
	dryRun := fs.Bool("dry-run", false, "print generated DDL without connecting to Postgres")
	force := fs.Bool("force", false, "proceed even with unreviewed columns above the confidence gate")
	resume := fs.Bool("resume", false, "skip tables already completed in a prior run, per <config>.state.json")
	threshold := fs.Float64("threshold", 0.9, "confidence gate enforced unless --force")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate load [--pg url] [--dry-run] [--force] <config.migration.yaml>")
	}
	configPath := fs.Arg(0)

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := ddl.ValidateTableConfigs(cfg); err != nil {
		return err
	}

	if !*force {
		if drifted, err := config.DetectDrift(cfg); err != nil {
			return err
		} else if drifted {
			return fmt.Errorf("source file %s has changed since this config was generated; re-run `migrate profile` or pass --force", cfg.Source.Path)
		}
		for tableName, tc := range cfg.Tables {
			for colName, col := range tc.Columns {
				// col.NeedsReview persists resolver.Decide's
				// disagreement-tie verdict (issue #20): a contested
				// decision can leave Confidence at the winning finding's
				// original value, above threshold, so Confidence alone
				// isn't a reliable gate here.
				if !col.Reviewed && col.Confidence < *threshold {
					return fmt.Errorf("%s.%s is unreviewed (confidence %.2f < %.2f); run `migrate review` or pass --force", tableName, colName, col.Confidence, *threshold)
				}
				if !col.Reviewed && col.NeedsReview {
					return fmt.Errorf("%s.%s is unreviewed (heuristics disagreed); run `migrate review` or pass --force", tableName, colName)
				}
			}
		}
	}

	if *dryRun {
		printDryRunDDL(os.Stdout, cfg)
		return nil
	}

	if *pgURL == "" {
		return errors.New("--pg is required unless --dry-run is set")
	}

	statePath := configPath + ".state.json"
	connCfg, err := connectForLoad(context.Background(), *pgURL, cfg.Source.Path, *resume, statePath)
	if err != nil {
		return err
	}

	return executeLoad(cfg, connCfg, *resume, statePath)
}

// printDryRunDDL writes the CREATE TABLE statements, foreign key
// constraints, and foreign key indexes generated from cfg to w. Table names
// are sorted first — cfg.Tables is a Go map, and ranging over it directly
// (as this used to do) produces a randomized table order on every run,
// making a diff between two dry runs show spurious churn even when nothing
// meaningful changed (issue #32). executeLoad sorts for the same reason.
func printDryRunDDL(w io.Writer, cfg *config.MigrationConfig) {
	tableNames := make([]string, 0, len(cfg.Tables))
	for tableName := range cfg.Tables {
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames)

	// The identifier CREATE TABLE must actually emit for each table (see
	// ddl.PostgresTableNames/issue #44) — computed once here so every
	// generator below (CREATE TABLE, foreign keys, foreign key indexes)
	// agrees on the same disambiguated name for the same table.
	pgTableNames := ddl.PostgresTableNames(cfg)

	for _, tableName := range tableNames {
		tc := cfg.Tables[tableName]
		if !tc.Include {
			continue
		}
		stmt, err := ddl.GenerateCreateTable(pgTableNames[tableName], tc)
		if err != nil {
			// ValidateTableConfigs above already rejected the
			// config-bug case (ErrMissingColumnOrder), so what's
			// left here is a legitimately all-dropped table
			// (issue #30) — skip it with a warning instead of
			// printing invalid SQL.
			fmt.Fprintf(os.Stderr, "skipping table %s: %v\n", tableName, err)
			continue
		}
		fmt.Fprint(w, stmt)
	}
	statements, skipped := ddl.GenerateForeignKeyConstraints(cfg)
	for _, stmt := range statements {
		fmt.Fprintln(w, stmt)
	}
	for _, reason := range skipped {
		fmt.Fprintf(os.Stderr, "skipping foreign key: %s\n", reason)
	}
	for _, stmt := range ddl.GenerateForeignKeyIndexes(cfg) {
		fmt.Fprintln(w, stmt)
	}
}

// executeLoad connects to Postgres and, for every included table, creates
// it and streams its rows via COPY. Shared by `load` and the load step of
// the single-shot `run` command.
func executeLoad(cfg *config.MigrationConfig, connCfg *pgx.ConnConfig, resume bool, statePath string) error {
	sourceDB, err := sql.Open("sqlite", cfg.Source.Path)
	if err != nil {
		return fmt.Errorf("opening source %s: %w", cfg.Source.Path, err)
	}
	defer sourceDB.Close()

	ctx := context.Background()
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	completed := map[string]bool{}
	if resume {
		completed, err = loadCompletedTables(statePath)
		if err != nil {
			return err
		}
	}

	// Count every table this run will actually load up front, so the
	// progress bar has a real grand total from its very first draw
	// instead of growing as tables are discovered. Tables already
	// completed by a prior --resume run are excluded, matching what the
	// loop below will actually touch. Sorted for deterministic load
	// order — cfg.Tables is a Go map, and this replaces what was
	// previously an unordered range directly over it.
	var tableNames []string
	for tableName, tc := range cfg.Tables {
		if !tc.Include {
			continue
		}
		// A table with zero included columns (issue #30 — e.g. an Esri
		// table whose only column is a geometryblob mapped to
		// __drop__) can't be created at all, so it's excluded here
		// rather than left to fail later — both from the DDL/COPY loop
		// below and from the row count that sizes the progress bar.
		// ValidateTableConfigs already rejected the distinct
		// config-bug case (columns present but column_order missing),
		// so anything left here is a legitimate all-dropped table.
		if len(ddl.IncludedColumns(tc)) == 0 {
			fmt.Fprintf(os.Stderr, "skipping table %s: %v\n", tableName, ddl.ErrNoIncludedColumns)
			continue
		}
		if resume && completed[tableName] {
			fmt.Printf("%s: skipping (already completed)\n", tableName)
			continue
		}
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames)

	// The identifier CREATE TABLE and COPY must actually target for each
	// table (see ddl.PostgresTableNames/issue #44) — computed once here
	// (schema-wide, over the full config, not just the tables this run
	// will touch) so DDL, COPY, and the foreign key/index step below all
	// agree on the same disambiguated name for the same table. tableName
	// (the source name) is still what's used for progress reporting, the
	// state file, and error messages below — those stay human-readable
	// and are unaffected by truncation since source table names are never
	// themselves ambiguous, only their Postgres-truncated form can be.
	pgTableNames := ddl.PostgresTableNames(cfg)

	var totalRows int64
	for _, tableName := range tableNames {
		n, err := sqlitereader.CountRows(sourceDB, tableName)
		if err != nil {
			return fmt.Errorf("counting rows in %s: %w", tableName, err)
		}
		totalRows += int64(n)
	}
	progress := newProgressReporter(totalRows)

	for _, tableName := range tableNames {
		tc := cfg.Tables[tableName]
		pgTable := pgTableNames[tableName]
		stmt, err := ddl.GenerateCreateTable(pgTable, tc)
		if err != nil {
			return fmt.Errorf("generating DDL for %s: %w", tableName, err)
		}
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("creating table %s: %w", tableName, err)
		}
		progress.startTable(tableName)
		src := copywriter.NewTableSource(sourceDB, tableName, tc).OnRow(progress.row)
		n, err := copywriter.LoadTable(ctx, conn, pgTable, tc, src)
		if err != nil {
			progress.abort()
			return err
		}
		if err := markTableCompleted(statePath, tableName); err != nil {
			return err
		}
		progress.finishTable(tableName, n)
	}

	// Foreign keys are added only now, after every table exists and is
	// fully loaded — never interleaved with CREATE TABLE/COPY above, so
	// table creation and data loading never need to be ordered by FK
	// dependency. Guarded by the state file's FKsApplied flag so this step
	// is itself idempotent across separate --resume invocations: without
	// it, a --resume run that finds every table already completed but
	// hasn't yet recorded FKsApplied would either skip foreign keys
	// entirely (if this guard were "resume implies FKs done") or, worse,
	// try to re-add constraints Postgres already has and fail.
	st, err := readState(statePath)
	if err != nil {
		return err
	}
	if !st.FKsApplied {
		statements, skipped := ddl.GenerateForeignKeyConstraints(cfg)
		for _, reason := range skipped {
			fmt.Printf("skipping foreign key: %s\n", reason)
		}
		for _, stmt := range statements {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("adding foreign key: %w", err)
			}
		}

		// Postgres doesn't auto-index foreign keys the way some other
		// databases do, and an index on every FK column is
		// well-established best practice with no real downside — added
		// right after the constraints themselves, once every FK is known
		// to be valid.
		for _, stmt := range ddl.GenerateForeignKeyIndexes(cfg) {
			if _, err := conn.Exec(ctx, stmt); err != nil {
				return fmt.Errorf("adding foreign key index: %w", err)
			}
		}
		if err := markForeignKeysApplied(statePath); err != nil {
			return err
		}
	} else {
		fmt.Println("foreign keys already applied in a prior run — skipping")
	}

	// The state file is deliberately left in place even after a fully
	// successful load, rather than removed: it's the durable record of
	// which database this config's data actually landed in (issue #19),
	// and `migrate verify` (which has no other way to know which database
	// to check) reads it back out for exactly that reason. A --resume
	// against an already-fully-loaded config is safe to run again — every
	// table is skipped via Completed and the FK step above is skipped via
	// FKsApplied — so there's no correctness reason to remove it once
	// nothing is left to resume, only the (deliberately declined) tidiness
	// of an unused file lying around.
	return nil
}

// --- resolve ---------------------------------------------------------------

func runResolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	apply := fs.String("apply", "", "resolutions YAML file to merge into the config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *apply == "" {
		return errors.New("usage: migrate resolve --apply resolutions.yaml <config.migration.yaml>")
	}
	configPath := fs.Arg(0)

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	resolutions, err := loadResolutions(*apply)
	if err != nil {
		return err
	}

	for key, res := range resolutions {
		table, column, err := splitKey(key)
		if err != nil {
			return err
		}
		tc, ok := cfg.Tables[table]
		if !ok {
			return fmt.Errorf("resolutions file references unknown table %q", table)
		}
		col, ok := tc.Columns[column]
		if !ok {
			return fmt.Errorf("resolutions file references unknown column %s.%s", table, column)
		}
		col.OriginalSuggestion = &config.Suggestion{TargetType: col.TargetType, Confidence: col.Confidence, Source: col.Source}
		col.TargetType = res.Type
		col.Transform = res.Transform
		col.Rationale = res.Rationale
		col.Confidence = res.Confidence
		col.Source = res.Source
		col.Reviewed = true
		tc.Columns[column] = col
		cfg.Tables[table] = tc
	}

	return config.Save(cfg, configPath)
}
