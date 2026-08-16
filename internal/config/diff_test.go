package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashFile_IsStableForSameContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	h2, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if h1 != h2 {
		t.Errorf("expected stable hash, got %q then %q", h1, h2)
	}
}

func TestHashFile_DiffersForDifferentContent(t *testing.T) {
	pathA := filepath.Join(t.TempDir(), "a.db")
	pathB := filepath.Join(t.TempDir(), "b.db")
	os.WriteFile(pathA, []byte("hello"), 0o644)
	os.WriteFile(pathB, []byte("goodbye"), 0o644)

	hA, _ := HashFile(pathA)
	hB, _ := HashFile(pathB)
	if hA == hB {
		t.Error("expected different content to hash differently")
	}
}

func TestDetectDrift_FalseWhenSourceUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bikes.db")
	os.WriteFile(path, []byte("original content"), 0o644)
	hash, _ := HashFile(path)

	cfg := &MigrationConfig{Source: SourceInfo{Path: path, SQLiteSHA256: hash}}
	drifted, err := DetectDrift(cfg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}
	if drifted {
		t.Error("expected no drift when source file is unchanged")
	}
}

func TestDetectDrift_TrueWhenSourceChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bikes.db")
	os.WriteFile(path, []byte("original content"), 0o644)
	hash, _ := HashFile(path)

	os.WriteFile(path, []byte("modified content"), 0o644)

	cfg := &MigrationConfig{Source: SourceInfo{Path: path, SQLiteSHA256: hash}}
	drifted, err := DetectDrift(cfg)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}
	if !drifted {
		t.Error("expected drift to be detected when source file content changed")
	}
}
