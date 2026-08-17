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
