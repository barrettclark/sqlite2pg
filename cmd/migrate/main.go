// Command migrate replaces pgloader for SQLite -> Postgres migrations: it
// profiles a source database, lets a human review ambiguous type
// decisions in a local web wizard, and loads the result into Postgres via
// the COPY protocol.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/jackc/pgx/v5"
	_ "modernc.org/sqlite"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/copywriter"
	"sqlite2pg/internal/ddl"
	"sqlite2pg/internal/pipeline"
	_ "sqlite2pg/internal/profiler/heuristics"
	"sqlite2pg/internal/resolver"
	"sqlite2pg/internal/wizard"
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
	case "profile":
		return runProfile(args[1:])
	case "review":
		return runReview(args[1:])
	case "load":
		return runLoad(args[1:])
	case "resolve":
		return runResolve(args[1:])
	default:
		return fmt.Errorf("unknown command %q (expected profile, review, load, or resolve)", args[0])
	}
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
	port := fs.Int("port", 0, "port to bind (0 = pick a free port)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: migrate review [--threshold F] <config.migration.yaml>")
	}
	configPath := fs.Arg(0)

	st, err := wizard.NewState(configPath, *threshold)
	if err != nil {
		return err
	}
	ln, err := wizard.Listen(*port)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	fmt.Printf("review server listening at %s (waiting for Finish Review)\n", url)
	openBrowser(url)

	return wizard.Run(context.Background(), ln, st)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// --- load ----------------------------------------------------------------

func runLoad(args []string) error {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	pgURL := fs.String("pg", "", "Postgres connection string, e.g. postgres://user@localhost/dbname")
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
		return nil
	}

	if *pgURL == "" {
		return errors.New("--pg is required unless --dry-run is set")
	}

	sourceDB, err := sql.Open("sqlite", cfg.Source.Path)
	if err != nil {
		return fmt.Errorf("opening source %s: %w", cfg.Source.Path, err)
	}
	defer sourceDB.Close()

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *pgURL)
	if err != nil {
		return fmt.Errorf("connecting to Postgres: %w", err)
	}
	defer conn.Close(ctx)

	statePath := configPath + ".state.json"
	completed := map[string]bool{}
	if *resume {
		completed, err = loadCompletedTables(statePath)
		if err != nil {
			return err
		}
	}

	for tableName, tc := range cfg.Tables {
		if !tc.Include {
			continue
		}
		if *resume && completed[tableName] {
			fmt.Printf("%s: skipping (already completed)\n", tableName)
			continue
		}
		if _, err := conn.Exec(ctx, ddl.GenerateCreateTable(tableName, tc)); err != nil {
			return fmt.Errorf("creating table %s: %w", tableName, err)
		}
		src := copywriter.NewTableSource(sourceDB, tableName, tc)
		n, err := copywriter.LoadTable(ctx, conn, tableName, tc, src)
		if err != nil {
			return err
		}
		if err := markTableCompleted(statePath, tableName); err != nil {
			return err
		}
		fmt.Printf("%s: loaded %d row(s)\n", tableName, n)
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
