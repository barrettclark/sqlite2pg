package main

import (
	"strings"
	"testing"
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
