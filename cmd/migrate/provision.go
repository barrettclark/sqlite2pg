package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// maxDatabaseNameLen is Postgres's identifier length limit (NAMEDATALEN=64,
// minus 1 for the trailing null byte the server reserves).
const maxDatabaseNameLen = 63

var unsafeIdentChars = regexp.MustCompile(`[^a-z0-9_]+`)

// deriveDatabaseName turns a source file path into a database name unique
// to this run: the sanitized base filename plus a second-precision
// timestamp, so repeated loads of the same source file each land in their
// own database instead of colliding or silently appending.
func deriveDatabaseName(sourcePath string, now time.Time) string {
	base := filepath.Base(sourcePath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ToLower(base)
	base = unsafeIdentChars.ReplaceAllString(base, "_")
	base = strings.Trim(base, "_")
	if base == "" {
		base = "db"
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "db_" + base
	}

	suffix := "_" + now.Format("20060102_150405")
	if maxLen := maxDatabaseNameLen - len(suffix); len(base) > maxLen {
		base = strings.TrimRight(base[:maxLen], "_")
	}
	return base + suffix
}

// provisionDatabase creates a fresh database named dbName on the Postgres
// server identified by serverURL (which must carry no database name of its
// own — just host/port/credentials) and returns a ConnConfig pointed at it,
// ready for the actual load connection.
func provisionDatabase(ctx context.Context, serverURL, dbName string) (*pgx.ConnConfig, error) {
	maintCfg, err := pgx.ParseConfig(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parsing --pg url: %w", err)
	}
	maintCfg.Database = "postgres"

	conn, err := pgx.ConnectConfig(ctx, maintCfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to Postgres to create %s: %w", dbName, err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
		return nil, fmt.Errorf("creating database %s: %w", dbName, err)
	}

	targetCfg := maintCfg.Copy()
	targetCfg.Database = dbName
	return targetCfg, nil
}

// connConfigForDatabase builds a ConnConfig pointed at an existing database
// named dbName on the Postgres server identified by serverURL, without
// creating it. Used by --resume to reconnect to the database a prior
// partial run already provisioned, rather than creating a new one.
func connConfigForDatabase(serverURL, dbName string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parsing --pg url: %w", err)
	}
	cfg.Database = dbName
	return cfg, nil
}

// connectForLoad decides which database `migrate load` (and the load step
// of `migrate run`) should target and returns a ConnConfig for it.
//
// On a fresh run it derives a new timestamped name, provisions it, and
// records it in the state file at statePath so that a later --resume knows
// which database to come back to.
//
// On --resume it reads that recorded name back out of statePath and
// reconnects to it directly — it must never provision a new database, or
// every table the state file marks completed would silently be missing
// from wherever --resume actually lands (issue #19). If statePath carries
// no recorded database (e.g. --resume was passed on a run that never got
// far enough to provision one, or the state file is simply missing), it
// refuses rather than guessing.
func connectForLoad(ctx context.Context, serverURL, sourcePath string, resume bool, statePath string) (*pgx.ConnConfig, error) {
	if resume {
		st, err := readState(statePath)
		if err != nil {
			return nil, err
		}
		if st.Database == "" {
			return nil, fmt.Errorf("--resume requires a prior run's state file %s recording which database it loaded into; run without --resume to start a new load", statePath)
		}
		connCfg, err := connConfigForDatabase(serverURL, st.Database)
		if err != nil {
			return nil, err
		}
		fmt.Printf("resuming database %s\n", st.Database)
		return connCfg, nil
	}

	dbName := deriveDatabaseName(sourcePath, time.Now())
	connCfg, err := provisionDatabase(ctx, serverURL, dbName)
	if err != nil {
		return nil, err
	}
	fmt.Printf("created database %s\n", dbName)

	if err := writeState(statePath, loadState{Database: dbName}); err != nil {
		return nil, fmt.Errorf("recording provisioned database in state %s: %w", statePath, err)
	}
	return connCfg, nil
}
