// Package profiler samples SQLite column values and infers the Postgres
// type they should be migrated to, via a registry of independent,
// self-contained heuristics.
package profiler

// ColumnMeta describes a column as declared in the SQLite source schema.
type ColumnMeta struct {
	Table        string
	Name         string
	DeclaredType string
}

// Value is a single sampled cell value: nil, int64, float64, string, or []byte.
type Value = any

// Finding is one heuristic's opinion about a column's target type.
type Finding struct {
	SuggestedType string
	Confidence    float64 // 0.0-1.0
	Rationale     string
	TransformExpr string
	Heuristic     string // set by ProfileColumn to the name of the heuristic that produced this Finding
}

// Heuristic evaluates sampled column values and, optionally, suggests a
// target type. Implementations should be self-contained and independently
// testable; new cases are added by implementing a new Heuristic, not by
// editing existing ones.
type Heuristic interface {
	// Name is a stable identifier, e.g. "comma_formatted_number".
	Name() string
	// AppliesTo is a cheap pre-filter run before Evaluate.
	AppliesTo(meta ColumnMeta) bool
	// Evaluate returns a Finding and true if this heuristic has an opinion
	// about the column, or a zero Finding and false otherwise. meta is
	// passed alongside samples because some heuristics (e.g. Esri custom
	// type-name mapping) decide purely from declared type.
	Evaluate(meta ColumnMeta, samples []Value) (Finding, bool)
}

// Registry holds a set of Heuristics and runs them against a column.
type Registry struct {
	heuristics []Heuristic
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a Heuristic to the registry.
func (r *Registry) Register(h Heuristic) {
	r.heuristics = append(r.heuristics, h)
}

// ProfileColumn runs every applicable Heuristic against samples and returns
// the Findings from those that had an opinion. Multiple heuristics may fire
// on the same column; arbitrating between them is the resolver's job, not
// the registry's.
func (r *Registry) ProfileColumn(meta ColumnMeta, samples []Value) []Finding {
	var findings []Finding
	for _, h := range r.heuristics {
		if !h.AppliesTo(meta) {
			continue
		}
		f, ok := h.Evaluate(meta, samples)
		if !ok {
			continue
		}
		f.Heuristic = h.Name()
		findings = append(findings, f)
	}
	return findings
}

// Default is the package-level registry that heuristics self-register into
// via init().
var Default = NewRegistry()

// Register adds a Heuristic to the Default registry.
func Register(h Heuristic) {
	Default.Register(h)
}
