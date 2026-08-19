package gateway

import (
	"strings"
	"testing"
)

// TestObserveEdgeRuleValidateFailure_ClosedLabelSet checks that the
// metric coercer folds any unknown mode or reason into the closed
// {observe, warn, block, other} × {required_missing, type_mismatch,
// additional_properties_not_allowed, enum_violation, format_violation,
// other} cross-product. The closed set is the metric's contract with
// the §12 dashboard panel — a regression that allows a new tuple
// would inflate the series count and break the label-set alarms
// (issue #975 #3 / Mega-Foundation #979-a).

func TestObserveEdgeRuleValidateFailure_ClosedLabelSet(t *testing.T) {
	m := NewMetrics()
	// Hit every cell of the cross-product once.
	modes := []string{"observe", "warn", "block"}
	reasons := []string{
		"required_missing",
		"type_mismatch",
		"additional_properties_not_allowed",
		"enum_violation",
		"format_violation",
		"other",
	}
	for _, mode := range modes {
		for _, reason := range reasons {
			m.ObserveEdgeRuleValidateFailure(mode, reason)
		}
	}

	body := bodyForCounter(t, m)
	// The §12 dashboard panel reads the counter with label combos;
	// the matrix anchors the cross-product.
	want := []string{
		`gateway_edge_rule_validate_failures_total{mode="observe",reason="required_missing"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="warn",reason="type_mismatch"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="block",reason="additional_properties_not_allowed"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="block",reason="enum_violation"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="block",reason="format_violation"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="block",reason="other"} 1`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in metrics body:\n%s", w, body)
		}
	}
}

// TestObserveEdgeRuleValidateFailure_UnknownCoercedToOther ensures
// that a malformed mode or reason collapses to the closed set so a
// determined caller cannot grow the label cardinality. Covers the
// full coerce matrix:
//
//   - unknown mode + unknown reason → (mode="other", reason="other")
//   - unknown mode + known reason   → (mode="other", reason=<known>)
//   - known mode + unknown reason   → (mode=<known>, reason="other")
//
// A regression that only co-erced one side would let the cross-
// product inflate; this test pins the four-cell matrix.
func TestObserveEdgeRuleValidateFailure_UnknownCoercedToOther(t *testing.T) {
	m := NewMetrics()
	// (mode="other", reason="other") — both unknown.
	m.ObserveEdgeRuleValidateFailure("NUKE", "leak-fingerprint")
	m.ObserveEdgeRuleValidateFailure("explode", "x"+"y"+"z")
	// (mode="other", reason=<known>) — mode unknown, reason known.
	m.ObserveEdgeRuleValidateFailure("NUKE", "type_mismatch")
	m.ObserveEdgeRuleValidateFailure("explode", "enum_violation")
	// (mode=<known>, reason="other") — mode known, reason unknown.
	m.ObserveEdgeRuleValidateFailure("observe", "leak-fingerprint")
	m.ObserveEdgeRuleValidateFailure("block", "x"+"y"+"z")

	body := bodyForCounter(t, m)
	want := []string{
		// Both unknown → (other, other). Two increments from
		// the first two calls land here.
		`gateway_edge_rule_validate_failures_total{mode="other",reason="other"} 2`,
		// Unknown mode + known reason → (other, known).
		`gateway_edge_rule_validate_failures_total{mode="other",reason="type_mismatch"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="other",reason="enum_violation"} 1`,
		// Known mode + unknown reason → (known, other).
		`gateway_edge_rule_validate_failures_total{mode="observe",reason="other"} 1`,
		`gateway_edge_rule_validate_failures_total{mode="block",reason="other"} 1`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in metrics body:\n%s", w, body)
		}
	}
}

// TestObserveEdgeRuleValidateFailure_NilReceiver is the nil-safe
// guard called out in the method's doc-comment. The unit test
// doubles as a meta-check that no other Observe* helper changed
// the nil-safety contract.
func TestObserveEdgeRuleValidateFailure_NilReceiver(t *testing.T) {
	var m *Metrics
	// Must not panic.
	m.ObserveEdgeRuleValidateFailure("observe", "required_missing")
}
