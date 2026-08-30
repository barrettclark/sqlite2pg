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
	"os"
	"sort"
	"time"

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
		return errors.New("usage: migrate <profile|review|load|resolve> ...")
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
	case "resolve":
		return runResolve(args[1:])
	default:
		return fmt.Errorf("unknown command %q (expected run, profile, review, load, or resolve)", args[0])
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
	if !*keepConfig {
		defer os.Remove(configPath)
	}
	fmt.Printf("profiled %s: %d table(s), %d column(s) need review\n", sourcePath, len(result.Config.Tables), len(result.Unresolved))

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

	dbName := deriveDatabaseName(sourcePath, time.Now())
	connCfg, err := provisionDatabase(context.Background(), *pgURL, dbName)
	if err != nil {
		return err
	}
	fmt.Printf("created database %s\n", dbName)

	return executeLoad(cfg, connCfg, false, configPath+".state.json")
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

	if len(result.Unresolved) > 0 {
		reportPath := *out + ".unresolved_report.yaml"
		fr := resolver.FileResolver{ReportPath: reportPath}
		_, resolveErr := fr.Resolve(context.Background(), result.Unresolved)
		fmt.Printf("%d column(s) need review — run `migrate review %s` or edit %s\n", len(result.Unresolved), *out, *out)
		return resolveErr
	}
	return nil
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

	if !*force {
		if drifted, err := config.DetectDrift(cfg); err != nil {
			return err
		} else if drifted {
			return fmt.Errorf("source file %s has changed since this config was generated; re-run `migrate profile` or pass --force", cfg.Source.Path)
		}
		for tableName, tc := range cfg.Tables {
			for colName, col := range tc.Columns {
				if !col.Reviewed && col.Confidence < *threshold {
					return fmt.Errorf("%s.%s is unreviewed (confidence %.2f < %.2f); run `migrate review` or pass --force", tableName, colName, col.Confidence, *threshold)
				}
			}
		}
	}

	if *dryRun {
		for tableName, tc := range cfg.Tables {
			if !tc.Include {
				continue
			}
			fmt.Print(ddl.GenerateCreateTable(tableName, tc))
		}
		statements, skipped := ddl.GenerateForeignKeyConstraints(cfg)
		for _, stmt := range statements {
			fmt.Println(stmt)
		}
		for _, reason := range skipped {
			fmt.Fprintf(os.Stderr, "skipping foreign key: %s\n", reason)
		}
		for _, stmt := range ddl.GenerateForeignKeyIndexes(cfg) {
			fmt.Println(stmt)
		}
		return nil
	}

	if *pgURL == "" {
		return errors.New("--pg is required unless --dry-run is set")
	}

	dbName := deriveDatabaseName(cfg.Source.Path, time.Now())
	connCfg, err := provisionDatabase(context.Background(), *pgURL, dbName)
	if err != nil {
		return err
	}
	fmt.Printf("created database %s\n", dbName)

	return executeLoad(cfg, connCfg, *resume, configPath+".state.json")
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
		if resume && completed[tableName] {
			fmt.Printf("%s: skipping (already completed)\n", tableName)
			continue
		}
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames)

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
		if _, err := conn.Exec(ctx, ddl.GenerateCreateTable(tableName, tc)); err != nil {
			return fmt.Errorf("creating table %s: %w", tableName, err)
		}
		progress.startTable(tableName)
		src := copywriter.NewTableSource(sourceDB, tableName, tc).OnRow(progress.row)
		n, err := copywriter.LoadTable(ctx, conn, tableName, tc, src)
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
	// dependency. If this fails, the state file is deliberately left in
	// place: a --resume retry will skip every already-loaded table (per
	// the completed-tables check above) and land right back here.
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
	// databases do, and an index on every FK column is well-established
	// best practice with no real downside — added right after the
	// constraints themselves, once every FK is known to be valid.
	for _, stmt := range ddl.GenerateForeignKeyIndexes(cfg) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("adding foreign key index: %w", err)
		}
	}

	// Every included table made it through, so nothing is left to resume —
	// the state file (and, for a `run`-generated config, the config file
	// itself, per --keep-config) has no further purpose.
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing state file %s: %w", statePath, err)
	}
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
