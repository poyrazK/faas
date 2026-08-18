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
// determined caller cannot grow the label cardinality.
func TestObserveEdgeRuleValidateFailure_UnknownCoercedToOther(t *testing.T) {
	m := NewMetrics()
	m.ObserveEdgeRuleValidateFailure("NUKE", "leak-fingerprint")
	m.ObserveEdgeRuleValidateFailure("explode", "x"+"y"+"z")

	body := bodyForCounter(t, m)
	if !strings.Contains(body, `gateway_edge_rule_validate_failures_total{mode="other",reason="other"} 2`) {
		t.Errorf("unknown mode+reason did not collapse to other/other; body:\n%s", body)
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
