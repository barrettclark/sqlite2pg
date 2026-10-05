package config

import (
	"fmt"
	"os"

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
		source := cfg.Source.Path
		if source == "" {
			source = "<source.db>"
		}
		return nil, fmt.Errorf("config %s has config_version %d, but this build of sqlite2pg understands version %d; re-run `sqlite2pg profile --out %s %s` to regenerate it. That overwrites the file and discards reviewed column decisions and overrides, so back it up first and re-review the output", path, cfg.ConfigVersion, CurrentConfigVersion, path, source)
	}
	return &cfg, nil
}
