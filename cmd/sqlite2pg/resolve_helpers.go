package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"sqlite2pg/internal/resolver"
)

// loadResolutions reads a resolutions file keyed by "table.column", the
// format a human (or Claude Code session) fills in after `sqlite2pg profile`
// writes an unresolved_report.yaml and exits non-zero pointing at it.
func loadResolutions(path string) (map[string]resolver.Resolution, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading resolutions %s: %w", path, err)
	}
	var resolutions map[string]resolver.Resolution
	if err := yaml.Unmarshal(data, &resolutions); err != nil {
		return nil, fmt.Errorf("parsing resolutions %s: %w", path, err)
	}
	return resolutions, nil
}

func splitKey(key string) (table, column string, err error) {
	i := strings.LastIndex(key, ".")
	if i < 0 {
		return "", "", fmt.Errorf("resolutions key %q is not in \"table.column\" form", key)
	}
	return key[:i], key[i+1:], nil
}
