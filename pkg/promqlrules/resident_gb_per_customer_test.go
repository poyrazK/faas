package promqlrules_test

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// FaasResidentGbPerCustomerHigh warns when a plan runs above its own
// financial-model residency by api.ResidentGBPerCustomerWarnFactor. A flat
// threshold for every plan fired for a week on Scale customers well inside
// their plan (ADR-20261117070932).
func TestResidentGBPerCustomerAlertUsesThePlanModel(t *testing.T) {
	factor := regexp.MustCompile(`max by \(plan\) \(meterd_resident_gb_per_customer\)\s*>\s*([0-9.]+) \* max by \(plan\) \(meterd_resident_gb_per_customer_planned\)`)
	for _, r := range loadShippedRules(t) {
		if r.Alert != "FaasResidentGbPerCustomerHigh" {
			continue
		}
		m := factor.FindStringSubmatch(r.Expr)
		if m == nil {
			t.Fatalf("FaasResidentGbPerCustomerHigh expr %q does not compare the plan average with its planned value", r.Expr)
		}
		got, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			t.Fatalf("factor %q: %v", m[1], err)
		}
		if got != api.ResidentGBPerCustomerWarnFactor {
			t.Fatalf("FaasResidentGbPerCustomerHigh factor = %v, want api.ResidentGBPerCustomerWarnFactor %v", got, api.ResidentGBPerCustomerWarnFactor)
		}
		return
	}
	t.Fatal("FaasResidentGbPerCustomerHigh not found in the shipped rules")
}
