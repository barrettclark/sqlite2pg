package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// HashFile returns the hex-encoded SHA-256 hash of a file's contents,
// used to detect when a source SQLite file has changed since a config was
// generated from it.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DetectDrift reports whether cfg.Source.Path's current content hash
// differs from the hash recorded when the config was generated. When
// drift is detected, callers should route new/changed columns back through
// review rather than proceeding with a stale mapping.
func DetectDrift(cfg *MigrationConfig) (bool, error) {
	current, err := HashFile(cfg.Source.Path)
	if err != nil {
		return false, err
	}
	return current != cfg.Source.SQLiteSHA256, nil
}
