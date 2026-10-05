package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads and parses a MigrationConfig from path.
func Load(path string) (*MigrationConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg MigrationConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if cfg.ConfigVersion != CurrentConfigVersion {
		source := "'<your SQLite file>'"
		hint := ""
		if cfg.Source.Path != "" {
			source = shellQuote(cfg.Source.Path)
		} else {
			hint = " (replace <your SQLite file> with the path to your SQLite source)"
		}
		return nil, fmt.Errorf("config %s has config_version %d, but this build of sqlite2pg understands version %d; re-run `sqlite2pg profile --out %s %s` to regenerate it%s. From a terminal, profile prompts before overwriting; otherwise pass --force. run prompts the same way and takes --force too. Regeneration discards reviewed column decisions and overrides, and removes the load state, so back the file up first and re-review the output", path, cfg.ConfigVersion, CurrentConfigVersion, shellQuote(path), source, hint)
	}
	return &cfg, nil
}

// shellQuote single-quotes s for a POSIX shell, so a path with spaces or quotes
// copies correctly from the message.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
