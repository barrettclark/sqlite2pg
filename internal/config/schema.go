// Package config defines and persists MigrationConfig, the reviewed
// mapping file that both the review UI and `migrate load` read and write.
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

	// SkippedTables are source tables ReadSchema deliberately left out of
	// Tables because they're backed by a SQLite virtual table module this
	// tool doesn't implement (issue #29) — e.g. a spatial index or
	// reference-system catalog from an FGDB/Spatialite export. Recorded here
	// so a human reviewing this config can see exactly what wasn't migrated
	// and why, rather than the omission being silent.
	SkippedTables []SkippedTable `yaml:"skipped_tables,omitempty"`
}

// SkippedTable is one source table ReadSchema couldn't read and skipped —
// see MigrationConfig.SkippedTables.
type SkippedTable struct {
	Name   string `yaml:"name"`
	Reason string `yaml:"reason"`
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

	// ForeignKeys are the source table's declared foreign key constraints,
	// carried forward as-is — this is preserved source truth, not an
	// inference, so it's applied automatically rather than requiring human
	// review the way an ambiguous type decision does.
	ForeignKeys []ForeignKey `yaml:"foreign_keys,omitempty"`

	// SuggestedForeignKeys are undeclared relationships inferred from
	// column-naming convention plus a full-column value-containment check
	// (issue #6) — unlike ForeignKeys, these are never applied
	// automatically. Promote one by hand into ForeignKeys above (or edit
	// it first) to accept it; delete the entry to reject it.
	SuggestedForeignKeys []SuggestedForeignKey `yaml:"suggested_foreign_keys,omitempty"`
}

// SuggestedForeignKey is one inferred-but-unconfirmed foreign key, carrying
// the rationale a human needs to judge it (see TableConfig.SuggestedForeignKeys).
type SuggestedForeignKey struct {
	ForeignKey `yaml:",inline"`
	Rationale  string `yaml:"rationale"`
}

// ForeignKey is one declared foreign key constraint, which may span
// multiple columns (a composite key) — Columns and RefColumns are aligned
// by position.
type ForeignKey struct {
	Columns    []string `yaml:"columns"`
	RefTable   string   `yaml:"ref_table"`
	RefColumns []string `yaml:"ref_columns"`
	OnDelete   string   `yaml:"on_delete,omitempty"`
	OnUpdate   string   `yaml:"on_update,omitempty"`
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

	// NeedsReview persists resolver.Decide's needsReview verdict
	// explicitly, independent of Confidence (issue #20): the
	// disagreement-tie case leaves Confidence at the winning finding's
	// original value (e.g. 0.95), which can sit above threshold even
	// though the decision was contested — so Confidence alone can't be
	// trusted to signal "review this" the way it can for the
	// below-threshold and full-table-violation cases (which also rewrite
	// Confidence to a sentinel below threshold). Both BuildReviewSummary
	// and `migrate load`'s gate must consult this in addition to
	// Confidence, not instead of it.
	NeedsReview bool `yaml:"needs_review,omitempty"`

	// PrimaryKeySeq is 0 if this column isn't part of the table's primary
	// key, or its 1-based position within it otherwise — mirrors
	// sqlitereader.ColumnInfo.PrimaryKeySeq, preserving a composite
	// primary key's declared column order into the generated DDL.
	PrimaryKeySeq int `yaml:"primary_key_seq,omitempty"`

	// NotNull mirrors sqlitereader.ColumnInfo.NotNull — preserved source
	// truth carried straight from SQLite's declared `NOT NULL`, emitted as
	// a NOT NULL constraint in the generated DDL rather than a heuristic
	// decision requiring review.
	NotNull bool `yaml:"not_null,omitempty"`

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
