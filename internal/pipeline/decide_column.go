package pipeline

import (
	"database/sql"
	"fmt"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/profiler"
	"sqlite2pg/internal/resolver"
	"sqlite2pg/internal/sqlitereader"
)

// fullTableViolationConfidence is the confidence stamped onto a decision
// a full-table check contradicts — deliberately below any sane
// --threshold, so both BuildReviewSummary's NeedsReview flag and `migrate
// load`'s own confidence gate treat it as needing review through the same
// mechanism every other low-confidence column already uses, with no new
// field required.
const fullTableViolationConfidence = 0.4

// decideColumn resolves one column's target-type decision from its
// samples, then — only for a decision that would otherwise auto-approve
// with a transform attached — verifies that decision against the column's
// entire table before trusting it (issue #13: a sample-based heuristic can
// look entirely clean while a rare real-world exception well outside the
// sample would crash COPY). A confirmed decision is returned unchanged; a
// contradicted one keeps its suggested type/transform visible (so a human
// sees exactly what the sample suggested) but with its confidence dropped
// below threshold and its rationale replaced, the same way any other
// below-threshold finding is surfaced.
//
// Returns the column's config, an UnresolvedCase (nil if none), and an
// error only for a genuine I/O/query failure — never for a found
// violation, which is reported via the returned UnresolvedCase instead.
func decideColumn(db *sql.DB, table string, col sqlitereader.ColumnInfo, samples []profiler.Value, threshold float64, extraFindings ...profiler.Finding) (config.ColumnConfig, *resolver.UnresolvedCase, error) {
	meta := profiler.ColumnMeta{Table: table, Name: col.Name, DeclaredType: col.DeclaredType}
	findings := append(profiler.Default.ProfileColumn(meta, samples), extraFindings...)

	if len(findings) == 0 {
		target := fallbackTypeFor(col.DeclaredType, samples)
		confidence := 0.99
		rationale := "no heuristic had an opinion; passed through via SQLite type affinity"
		var uc *resolver.UnresolvedCase
		if bad, found := fallbackSampleMismatch(target, samples); found {
			// Issue #16: SQLite's dynamic typing let this declared/sample
			// type majority be wrong for at least one row in hand already
			// — don't carry that false confidence to `load`, which would
			// crash encoding bad into target's binary format.
			confidence = fullTableViolationConfidence
			rationale = fmt.Sprintf("no heuristic had an opinion; declared type and sample majority suggested %s, but the sample itself contains a value that can't be stored as %s: %#v (SQLite's dynamic typing allows this even though the column is declared %s)", target, target, bad, col.DeclaredType)
			uc = &resolver.UnresolvedCase{
				Table:        table,
				Column:       col.Name,
				DeclaredType: col.DeclaredType,
				Samples:      samples,
				Findings:     findings,
				Reason:       rationale,
			}
		}
		return config.ColumnConfig{
			DeclaredType:  col.DeclaredType,
			TargetType:    target,
			Confidence:    confidence,
			Source:        "heuristic:default_passthrough",
			Rationale:     rationale,
			Reviewed:      false,
			NeedsReview:   uc != nil,
			PrimaryKeySeq: col.PrimaryKeySeq,
		}, uc, nil
	}

	best, needsReview := resolver.Decide(findings, threshold)
	reason := fmt.Sprintf("confidence %.2f below auto-approve threshold %.2f, or heuristics disagreed", best.Confidence, threshold)

	if !needsReview && best.TransformExpr != "" {
		ok, badValue, err := verifyTransformAgainstFullTable(db, table, col.Name, best.TransformExpr, best.SuggestedType)
		if err != nil {
			return config.ColumnConfig{}, nil, err
		}
		if !ok {
			needsReview = true
			reason = fmt.Sprintf("sample looked like %s (heuristic:%s, confidence %.2f), but a full-table check found a value it can't convert: %q",
				best.SuggestedType, best.Heuristic, best.Confidence, badValue)
			best.Rationale = fmt.Sprintf("%s — but a full-table check found a value the transform can't convert: %q", best.Rationale, badValue)
			best.Confidence = fullTableViolationConfidence
		}
	}

	cc := config.ColumnConfig{
		DeclaredType:  col.DeclaredType,
		TargetType:    best.SuggestedType,
		Transform:     best.TransformExpr,
		Confidence:    best.Confidence,
		Source:        "heuristic:" + best.Heuristic,
		Rationale:     best.Rationale,
		Reviewed:      false,
		NeedsReview:   needsReview,
		PrimaryKeySeq: col.PrimaryKeySeq,
	}
	if !needsReview {
		return cc, nil, nil
	}
	return cc, &resolver.UnresolvedCase{
		Table:        table,
		Column:       col.Name,
		DeclaredType: col.DeclaredType,
		Samples:      samples,
		Findings:     findings,
		Reason:       reason,
	}, nil
}
