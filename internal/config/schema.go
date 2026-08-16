// Package config defines and persists MigrationConfig, the reviewed
// mapping file that both the web wizard and `migrate load` read and write.
// It is the single source of truth for a reproducible run.
package config

import "time"

// CurrentConfigVersion is written into every newly-generated config and
// checked by Load to detect configs from an older schema.
const CurrentConfigVersion = 1

// MigrationConfig is the persisted, versioned mapping from a SQLite source
// to a Postgres target.
type MigrationConfig struct {
	ConfigVersion int                    `yaml:"config_version"`
	Source        SourceInfo             `yaml:"source"`
	GeneratedAt   time.Time              `yaml:"generated_at,omitempty"`
	ToolVersion   string                 `yaml:"tool_version,omitempty"`
	Tables        map[string]TableConfig `yaml:"tables"`
}

// SourceInfo identifies the SQLite source file this config was generated
// from, including a content hash used for schema-drift detection.
type SourceInfo struct {
	Path         string `yaml:"path"`
	SQLiteSHA256 string `yaml:"sqlite_sha256"`
	Kind         string `yaml:"kind"` // "sqlite" | "esri_geodatabase"
}

// TableConfig is one table's inclusion decision and column mappings.
type TableConfig struct {
	Include bool                    `yaml:"include"`
	Columns map[string]ColumnConfig `yaml:"columns"`

	// ColumnOrder preserves the source table's declared column order, since
	// Columns is a map and Go/YAML give no ordering guarantee. DDL
	// generation depends on this for a deterministic CREATE TABLE.
	ColumnOrder []string `yaml:"column_order,omitempty"`
}

// ColumnConfig is one column's resolved type decision, together with the
// audit trail of how it was decided.
type ColumnConfig struct {
	DeclaredType string     `yaml:"declared_type"`
	TargetType   string     `yaml:"target_type"`
	Transform    string     `yaml:"transform,omitempty"`
	Confidence   float64    `yaml:"confidence"`
	Source       string     `yaml:"source"` // "heuristic:<name>" | "human_override" | "llm:<model>"
	Rationale    string     `yaml:"rationale,omitempty"`
	Reviewed     bool       `yaml:"reviewed"`
	ReviewedBy   string     `yaml:"reviewed_by,omitempty"`
	ReviewedAt   *time.Time `yaml:"reviewed_at,omitempty"`

	// OriginalSuggestion preserves the tool's original guess when a human
	// (or future LLM resolver) overrides it, so the diff between "what the
	// tool guessed" and "what shipped" is never lost.
	OriginalSuggestion *Suggestion `yaml:"original_suggestion,omitempty"`
}

// Suggestion is a preserved prior decision, kept for audit purposes after
// being overridden.
type Suggestion struct {
	TargetType string  `yaml:"target_type"`
	Confidence float64 `yaml:"confidence"`
	Source     string  `yaml:"source"`
}
