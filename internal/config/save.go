package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Save writes cfg to path as YAML.
func Save(cfg *MigrationConfig, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing config to %s: %w", path, err)
	}
	return nil
}
