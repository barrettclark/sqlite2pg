package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/term"

	"sqlite2pg/internal/config"
)

// verifyMode is `run`/`load`'s decision about whether to automatically run
// `migrate verify`-equivalent verification once a load has succeeded — set
// from the --verify/--noverify flags, or left to an interactive prompt when
// neither is passed.
type verifyMode int

const (
	// verifyPrompt is the default: neither --verify nor --noverify was
	// passed, so ask interactively via stdin once the load has finished.
	verifyPrompt verifyMode = iota
	// verifyAlways means --verify was passed: run verification
	// unconditionally, no prompt.
	verifyAlways
	// verifyNever means --noverify was passed: skip verification
	// unconditionally, no prompt.
	verifyNever
)

// resolveVerifyMode turns `run`/`load`'s --verify and --noverify flags into
// a verifyMode. Passing both is rejected as an unambiguous usage error
// rather than silently preferring one — there's no sensible "both" meaning
// for two flags that are each other's opposite.
func resolveVerifyMode(verify, noverify bool) (verifyMode, error) {
	if verify && noverify {
		return verifyPrompt, errors.New("--verify and --noverify are mutually exclusive")
	}
	if verify {
		return verifyAlways, nil
	}
	if noverify {
		return verifyNever, nil
	}
	return verifyPrompt, nil
}

// determineVerify decides whether post-load verification should actually
// run, given mode. For verifyAlways/verifyNever this is immediate and never
// touches in — only verifyPrompt reads a line from in (a plain stdin
// prompt, not another TUI dialog, since this decision point is reached
// after `run`'s review TUI has already exited back to plain terminal
// output). Only an explicit "y"/"yes" (case-insensitive, surrounding
// whitespace ignored) counts as yes; anything else — including empty input
// from a bare Enter — means no. This defaults to NOT verifying on a bare
// Enter deliberately: verification re-streams every row of the just-loaded
// data and can be time-consuming for a large import, so the safe default
// for an unconsidered answer is to skip it rather than silently commit the
// user to a potentially long extra pass.
func determineVerify(mode verifyMode, in io.Reader, out io.Writer) bool {
	switch mode {
	case verifyAlways:
		return true
	case verifyNever:
		return false
	}

	// A CI/automation environment often leaves stdin connected to an open
	// pipe that's never written to and never closed — reading from it
	// below would block forever. When in is a real *os.File and it's not
	// attached to a terminal (the same term.IsTerminal check progress.go
	// already uses for stdout), skip the prompt entirely and default to
	// false (no verify), the same answer a bare Enter at an interactive
	// prompt would produce. When in is NOT an *os.File (e.g. a
	// bytes.Reader/strings.Reader test double), this check doesn't apply
	// and the read-based behavior below proceeds unchanged.
	if f, ok := in.(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		return false
	}

	fmt.Fprint(out, "Run migrate verify now? [y/N]: ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// runPostLoadVerify is the inline verification path for `run --verify`/
// `load --verify` (and the interactive prompt both fall back to when
// neither --verify nor --noverify is passed). It runs directly against cfg
// and connCfg — the very same in-memory config.MigrationConfig and Postgres
// connection info the load that just succeeded already used — and
// deliberately never re-reads configPath or statePath from disk. That
// matters specifically for `run`: its cleanup step (cleanupConfigAfterLoad,
// issue #38/#52) deletes both files right after a successful load unless
// --keep-config is passed, and this function is always called before that
// cleanup runs, so it must not depend on either file still existing.
//
// A verification mismatch found here is a different, more serious finding
// than a load failure would have been: the import already succeeded and the
// data is already sitting in Postgres, so this returns a non-zero-signaling
// error (mirroring standalone `migrate verify`'s own exit-code convention)
// without implying the load itself failed or should be rolled back — the
// message printed makes the LOAD-succeeded/VERIFICATION-failed distinction
// explicit for exactly that reason.
func runPostLoadVerify(ctx context.Context, cfg *config.MigrationConfig, connCfg *pgx.ConnConfig, mode verifyMode, in io.Reader, out io.Writer) error {
	if !determineVerify(mode, in, out) {
		return nil
	}

	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		return fmt.Errorf("connecting to Postgres for post-load verification: %w", err)
	}
	defer conn.Close(ctx)

	sourceDB, err := sql.Open("sqlite", cfg.Source.Path)
	if err != nil {
		return fmt.Errorf("opening %s for post-load verification: %w", cfg.Source.Path, err)
	}
	defer sourceDB.Close()

	fmt.Fprintln(out, "running post-load verification...")
	summary, err := verifyLoadedTables(ctx, sourceDB, conn, cfg, out, out)
	if err != nil {
		return fmt.Errorf("post-load verification: %w", err)
	}

	if !summary.passed() {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "*** LOAD SUCCEEDED, BUT POST-LOAD VERIFICATION FOUND A PROBLEM ***")
		fmt.Fprintln(out, "The data is already in Postgres; this reports a data-integrity finding in it, not a failed import.")
		return fmt.Errorf("post-load verification FAILED: %d table(s) with row-count mismatches, %d value mismatch(es) across %d table(s) checked",
			summary.rowCountFailures, summary.totalMismatches, summary.tablesChecked)
	}
	fmt.Fprintf(out, "post-load verification passed: %d table(s) checked, %d row(s) compared, 0 mismatches\n", summary.tablesChecked, summary.totalRowsCompared)
	return nil
}
