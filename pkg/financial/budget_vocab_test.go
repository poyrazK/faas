package financial

import (
	"strings"
	"testing"
)

// Hunt 32 H32-60: `billing budgets create` with mode draft or action stop
// answered "budget mode" / "budget action" without the allowed values.
func TestBudgetValidationNamesAllowedValues(t *testing.T) {
	valid := BudgetSpec{Name: "batch", Scope: BudgetScope{Kind: "job", ID: "job"}, Currency: BillingCurrency, Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, Mode: "strict", Action: "suspend_background", DrainSeconds: 30, ResumeRule: "next_period"}
	for _, tc := range []struct {
		name   string
		change func(*BudgetSpec)
		want   string
	}{
		{"mode", func(p *BudgetSpec) { p.Mode = "draft" }, "budget mode must be one of monitored, strict"},
		{"action", func(p *BudgetSpec) { p.Action = "stop" }, "budget action must be one of notify, reject_traffic, suspend_background, stop_previews, suspend_workloads"},
		{"basis", func(p *BudgetSpec) { p.Basis = "list" }, "budget basis must be one of net_usage, gross_usage"},
		{"scope", func(p *BudgetSpec) { p.Scope.Kind = "org" }, "budget scope must be one of account, project, environment, app, job"},
		{"resume", func(p *BudgetSpec) { p.ResumeRule = "automatic" }, "budget resume rule must be one of manual, next_period"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			if err := ValidateBudget(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
