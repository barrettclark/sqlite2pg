package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeriveDatabaseName(t *testing.T) {
	fixedNow := time.Date(2026, 8, 16, 19, 45, 8, 0, time.UTC)

	cases := []struct {
		name       string
		sourcePath string
		want       string
	}{
		{
			name:       "simple filename gets sanitized and timestamped",
			sourcePath: "/Users/barrettclark/Desktop/neh-grants.db",
			want:       "neh_grants_20260816_194508",
		},
		{
			name:       "mixed case and spaces are lowercased and underscored",
			sourcePath: "My Cool DB.sqlite",
			want:       "my_cool_db_20260816_194508",
		},
		{
			name:       "sqlite3 extension is stripped",
			sourcePath: "fixtures/sample.sqlite3",
			want:       "sample_20260816_194508",
		},
		{
			name:       "geodatabase extension is stripped",
			sourcePath: "SchoolSites2425.geodatabase",
			want:       "schoolsites2425_20260816_194508",
		},
		{
			name:       "only the final extension is stripped, internal dots become underscores",
			sourcePath: "archive.v2.db",
			want:       "archive_v2_20260816_194508",
		},
		{
			name:       "a name starting with a digit gets a safe prefix",
			sourcePath: "123data.db",
			want:       "db_123data_20260816_194508",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := deriveDatabaseName(c.sourcePath, fixedNow)
			if got != c.want {
				t.Errorf("deriveDatabaseName(%q, ...) = %q, want %q", c.sourcePath, got, c.want)
			}
		})
	}
}

func TestDeriveDatabaseName_TruncatesLongNamesToFitPostgresIdentifierLimit(t *testing.T) {
	fixedNow := time.Date(2026, 8, 16, 19, 45, 8, 0, time.UTC)
	longName := strings.Repeat("a", 100) + ".db"

	got := deriveDatabaseName(longName, fixedNow)

	if len(got) > 63 {
		t.Errorf("deriveDatabaseName produced a %d-char name, want <= 63: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "_20260816_194508") {
		t.Errorf("expected the timestamp suffix to survive truncation, got %q", got)
	}
}

// TestConnectForLoad_ResumeReconnectsToRecordedDatabase is the regression
// test for issue #19: --resume must reconnect to the database a prior run
// recorded in the state file, never provision a fresh one. This never
// touches a real Postgres server — the resume path only builds a
// ConnConfig from the recorded name, it doesn't connect or create anything.
func TestConnectForLoad_ResumeReconnectsToRecordedDatabase(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "cfg.migration.yaml.state.json")
	if err := writeState(statePath, loadState{Database: "chinook_20260830_120000", Completed: []string{"albums", "artists"}}); err != nil {
		t.Fatalf("writeState: %v", err)
	}

	connCfg, err := connectForLoad(context.Background(), "postgres://localhost:5432/?sslmode=disable", "chinook.db", true, statePath)
	if err != nil {
		t.Fatalf("connectForLoad: %v", err)
	}
	if connCfg.Database != "chinook_20260830_120000" {
		t.Errorf("expected --resume to reconnect to the recorded database, got %q", connCfg.Database)
	}
}

// TestConnectForLoad_ResumeWithoutRecordedDatabaseErrors covers --resume
// passed with no prior state to resume from (missing state file, or one
// that never got far enough to record a database) — it must refuse rather
// than silently provisioning a new database out from under a resume.
func TestConnectForLoad_ResumeWithoutRecordedDatabaseErrors(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nonexistent.state.json")

	_, err := connectForLoad(context.Background(), "postgres://localhost:5432/?sslmode=disable", "chinook.db", true, statePath)
	if err == nil {
		t.Fatal("expected an error when --resume has no recorded database to reconnect to")
	}
	if !strings.Contains(err.Error(), "--resume") {
		t.Errorf("expected the error to mention --resume, got %q", err.Error())
	}
}
