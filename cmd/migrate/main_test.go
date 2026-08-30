package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sqlite2pg/internal/config"
)

func TestRun_NoArgsReturnsUsageError(t *testing.T) {
	err := run(nil)
	if err == nil {
		t.Fatal("expected an error for no arguments")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected a usage message, got %q", err.Error())
	}
}

func TestRun_UnknownCommandReturnsError(t *testing.T) {
	err := run([]string{"frobnicate"})
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("expected the error to name the unknown command, got %q", err.Error())
	}
}

func TestRun_ProfileRequiresASourcePath(t *testing.T) {
	err := run([]string{"profile"})
	if err == nil {
		t.Fatal("expected an error when no source database is given to profile")
	}
}

func TestRun_LoadRequiresAConfigPath(t *testing.T) {
	err := run([]string{"load"})
	if err == nil {
		t.Fatal("expected an error when no config path is given to load")
	}
}

func TestRun_RunRequiresASourcePath(t *testing.T) {
	err := run([]string{"run", "--pg", "postgres://localhost/x"})
	if err == nil {
		t.Fatal("expected an error when no source database is given to run")
	}
}

func TestRun_RunRequiresThePgFlag(t *testing.T) {
	err := run([]string{"run", "nonexistent.db"})
	if err == nil {
		t.Fatal("expected an error when --pg is not given to run")
	}
	if !strings.Contains(err.Error(), "--pg") {
		t.Errorf("expected the error to mention --pg, got %q", err.Error())
	}
}

func TestRunResolve_OverridingTargetTypeClearsAStaleTransform(t *testing.T) {
	// Regression: applying a human resolution that changes a column from
	// boolean (transform int_to_bool) to integer must not leave the old
	// transform in place — otherwise the COPY pipeline would try to encode
	// a Go bool into an integer column and fail. Found via dogfooding
	// against chinook.db's invoice_items.Quantity.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test.migration.yaml")
	cfg := &config.MigrationConfig{
		Tables: map[string]config.TableConfig{
			"invoice_items": {
				ColumnOrder: []string{"Quantity"},
				Columns: map[string]config.ColumnConfig{
					"Quantity": {TargetType: "boolean", Transform: "int_to_bool", Confidence: 0.55, Source: "heuristic:boolean01"},
				},
			},
		},
	}
	if err := config.Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	resolutionsPath := filepath.Join(dir, "resolutions.yaml")
	resolutionsYAML := "invoice_items.Quantity:\n  type: integer\n  transform: \"\"\n  confidence: 0.95\n  source: human\n  rationale: clearly a count\n"
	if err := os.WriteFile(resolutionsPath, []byte(resolutionsYAML), 0o644); err != nil {
		t.Fatalf("writing resolutions: %v", err)
	}

	if err := run([]string{"resolve", "--apply", resolutionsPath, configPath}); err != nil {
		t.Fatalf("run resolve: %v", err)
	}

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	col := loaded.Tables["invoice_items"].Columns["Quantity"]
	if col.TargetType != "integer" {
		t.Errorf("expected target_type integer, got %q", col.TargetType)
	}
	if col.Transform != "" {
		t.Errorf("expected the stale int_to_bool transform to be cleared, got %q", col.Transform)
	}
}

func TestPrintDryRunDDL_OrdersTablesAlphabeticallyRegardlessOfMapIteration(t *testing.T) {
	// Regression (issue #32): cfg.Tables is a Go map, and ranging over it
	// directly randomizes CREATE TABLE order between runs, so `migrate
	// load --dry-run` on an unchanged config could produce a spuriously
	// different diff each time. printDryRunDDL must always sort table
	// names before iterating, the same way executeLoad already does.
	names := []string{"zebra", "middle", "apple", "banana", "yak"}
	tables := make(map[string]config.TableConfig, len(names))
	for _, name := range names {
		tables[name] = config.TableConfig{
			Include:     true,
			ColumnOrder: []string{"id"},
			Columns: map[string]config.ColumnConfig{
				"id": {TargetType: "integer"},
			},
		}
	}
	cfg := &config.MigrationConfig{Tables: tables}

	var buf bytes.Buffer
	printDryRunDDL(&buf, cfg)
	output := buf.String()

	want := append([]string(nil), names...)
	sort.Strings(want)

	var got []string
	for _, name := range names {
		got = append(got, name)
	}
	sort.Slice(got, func(i, j int) bool {
		return strings.Index(output, `CREATE TABLE "`+got[i]+`"`) < strings.Index(output, `CREATE TABLE "`+got[j]+`"`)
	})

	for i, name := range want {
		if got[i] != name {
			t.Fatalf("expected table order %v (sorted), got %v", want, got)
		}
	}
}
