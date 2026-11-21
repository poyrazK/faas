package promqlrules_test

import (
	"strings"
	"testing"
)

// TestGCPOpsAgentMissingScopedToGCE pins FaasGCPOpsAgentMissing to compute
// nodes whose node-exporter DMI vendor is Google. Bare-metal hosts (the
// OVHcloud fleet since 2026-11-20) run no GCP Ops Agent, and an unscoped
// expression fired on every one of them. The promtool fixture in
// testdata/public_beta_release.test.yml exercises both sides; this keeps the
// scope from being dropped in a later rewrite of the expression.
func TestGCPOpsAgentMissingScopedToGCE(t *testing.T) {
	var found bool
	for _, r := range loadShippedRules(t) {
		if r.Alert != "FaasGCPOpsAgentMissing" {
			continue
		}
		found = true
		expr := strings.Join(strings.Fields(r.Expr), " ")
		for _, want := range []string{
			`node_dmi_info{job="node-compute",system_vendor="Google"}`,
			`and on(instance)`,
		} {
			if !strings.Contains(expr, want) {
				t.Errorf("%s: FaasGCPOpsAgentMissing expr %q lacks %q; the alert must stay scoped to GCE hosts", r.File, expr, want)
			}
		}
	}
	if !found {
		t.Fatal("FaasGCPOpsAgentMissing not found in the shipped rule files")
	}
}
