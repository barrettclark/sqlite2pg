package pipeline

import (
	"database/sql"
	"fmt"

	"sqlite2pg/internal/config"
	"sqlite2pg/internal/ddl"
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

// pendingColumnDecision carries a column's tentative decision (issue #55's
// phase 1: every sample-based heuristic already ran, resolver.Decide
// already picked a winner) across to phase 2 once its
// about-to-auto-approve transform has been checked against the full
// table — everything finalizeColumnDecision needs to reproduce
// decideColumn's original single-pass logic exactly, just fed from a
// batched verification result instead of an inline per-column scan.
type pendingColumnDecision struct {
	table    string
	col      sqlitereader.ColumnInfo
	samples  []profiler.Value
	findings []profiler.Finding

	best        profiler.Finding
	needsReview bool
	reason      string

	// verifySpec is what phase 2 needs to check this column against the
	// full table — its Column field is also the key results are looked
	// up by once verifyTransformsAgainstFullTable returns.
	verifySpec columnVerifySpec
}

// decideColumnTentative computes decideColumn's decision from samples
// alone — every heuristic, resolver.Decide, the drop-sentinel skip (issue
// #45), the zero-findings fallback path, everything decideColumn did
// before issue #55 split it — except the full-table verification itself,
// which requires I/O decideColumnTentative deliberately never performs.
//
// A column whose decision is already fully settled from samples alone
// (no heuristic had an opinion, the decision already needs review, it's a
// drop-sentinel, or it simply has no transform to verify) returns a
// finished (config.ColumnConfig, *resolver.UnresolvedCase) and a nil
// *pendingColumnDecision — there's nothing left for phase 2 to do. A
// column that would otherwise auto-approve with a transform attached
// returns a nil config/UnresolvedCase and a non-nil *pendingColumnDecision
// instead — the caller must run verifyTransformsAgainstFullTable and pass
// the result to finalizeColumnDecision before this column's decision is
// usable.
func decideColumnTentative(table string, col sqlitereader.ColumnInfo, samples []profiler.Value, threshold float64, extraFindings ...profiler.Finding) (config.ColumnConfig, *resolver.UnresolvedCase, *pendingColumnDecision) {
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
			NotNull:       col.NotNull,
		}, uc, nil
	}

	best, needsReview := resolver.Decide(findings, threshold)
	reason := fmt.Sprintf("confidence %.2f below auto-approve threshold %.2f, or heuristics disagreed", best.Confidence, threshold)

	// Issue #45: a column resolving to the drop sentinel (e.g. Esri
	// geometryblob via esri_typename_mapping) has nothing to verify —
	// copywriter.Transform("drop_column", ...) unconditionally errors by
	// design, since a dropped column has nothing to convert into. Running
	// the full-table check against it would always "find a violation" on
	// row 1, streaming the entire table for an answer already known
	// statically from the transform name.
	if !needsReview && best.TransformExpr != "" && best.SuggestedType != ddl.DropSentinel {
		return config.ColumnConfig{}, nil, &pendingColumnDecision{
			table:       table,
			col:         col,
			samples:     samples,
			findings:    findings,
			best:        best,
			needsReview: needsReview,
			reason:      reason,
			verifySpec: columnVerifySpec{
				Column:     col.Name,
				Transform:  best.TransformExpr,
				TargetType: best.SuggestedType,
				RejectNull: col.PrimaryKeySeq > 0 || col.NotNull,
			},
		}
	}

	cc := buildColumnConfig(col, best, needsReview)
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

// finalizeColumnDecision applies a full-table verification result to a
// pendingColumnDecision, reproducing decideColumn's original
// post-verification logic exactly: a confirmed decision passes through
// unchanged; a contradicted one keeps its suggested type/transform visible
// (so a human sees exactly what the sample suggested) but with its
// confidence dropped below threshold, its rationale replaced, and an
// UnresolvedCase produced — the same way any other below-threshold finding
// is surfaced.
func finalizeColumnDecision(p *pendingColumnDecision, vr verifyResult) (config.ColumnConfig, *resolver.UnresolvedCase) {
	best := p.best
	needsReview := p.needsReview
	reason := p.reason

	if !vr.OK {
		needsReview = true
		reason = fmt.Sprintf("sample looked like %s (heuristic:%s, confidence %.2f), but a full-table check found a value it can't convert: %q",
			best.SuggestedType, best.Heuristic, best.Confidence, vr.BadValue)
		best.Rationale = fmt.Sprintf("%s — but a full-table check found a value the transform can't convert: %q", best.Rationale, vr.BadValue)
		best.Confidence = fullTableViolationConfidence
	}

	cc := buildColumnConfig(p.col, best, needsReview)
	if !needsReview {
		return cc, nil
	}
	return cc, &resolver.UnresolvedCase{
		Table:        p.table,
		Column:       p.col.Name,
		DeclaredType: p.col.DeclaredType,
		Samples:      p.samples,
		Findings:     p.findings,
		Reason:       reason,
	}
}

// buildColumnConfig builds the config.ColumnConfig shared by both the
// samples-only path (decideColumnTentative) and the post-verification path
// (finalizeColumnDecision) — everything but the winning finding and
// needsReview verdict is the same either way.
func buildColumnConfig(col sqlitereader.ColumnInfo, best profiler.Finding, needsReview bool) config.ColumnConfig {
	return config.ColumnConfig{
		DeclaredType:  col.DeclaredType,
		TargetType:    best.SuggestedType,
		Transform:     best.TransformExpr,
		Confidence:    best.Confidence,
		Source:        "heuristic:" + best.Heuristic,
		Rationale:     best.Rationale,
		Reviewed:      false,
		NeedsReview:   needsReview,
		PrimaryKeySeq: col.PrimaryKeySeq,
		NotNull:       col.NotNull,
	}
}

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
//
// This single-column form runs decideColumnTentative followed immediately
// by its own one-column verifyTransformsAgainstFullTable call and
// finalizeColumnDecision (issue #55 split the original single-pass
// function into these pieces so ProfileDatabase's per-table loop could
// batch every column's verification into one shared full-table scan
// instead of one scan per column) — kept for callers, and tests, that
// only need to decide one column at a time.
func decideColumn(db *sql.DB, table string, col sqlitereader.ColumnInfo, samples []profiler.Value, threshold float64, extraFindings ...profiler.Finding) (config.ColumnConfig, *resolver.UnresolvedCase, error) {
	cc, uc, pending := decideColumnTentative(table, col, samples, threshold, extraFindings...)
	if pending == nil {
		return cc, uc, nil
	}

	results, err := verifyTransformsAgainstFullTable(db, table, []columnVerifySpec{pending.verifySpec})
	if err != nil {
		return config.ColumnConfig{}, nil, err
	}
	cc, uc = finalizeColumnDecision(pending, results[pending.verifySpec.Column])
	return cc, uc, nil
}
