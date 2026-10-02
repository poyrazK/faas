package financial

import (
	"errors"
	"math"
	"testing"
	"time"
)

// adr: 431 — a scoped budget shares the account allowance and retains identity.
func TestBudgetAmountAndScope(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	price := Price{Version: "v1", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1, IncludedQuantity: 50}
	a := Attribution{AppID: "deleted-app", ProjectID: "project", EnvironmentID: "preview"}
	b := Attribution{AppID: "other-app"}
	cost, err := CostContracts("account", start, end, []Price{price}, []VersionedEvidence{
		{PriceVersion: "v1", Evidence: Evidence{AccountID: "account", SourceID: "a", Meter: "compute", Quantity: 100, Start: start, End: start.Add(time.Minute), Attribution: a}},
		{PriceVersion: "v1", Evidence: Evidence{AccountID: "account", SourceID: "b", Meter: "compute", Quantity: 100, Start: start, End: start.Add(time.Minute), Attribution: b}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := BudgetSpec{Name: "preview costs", Scope: BudgetScope{Kind: "app", ID: a.AppID}, Currency: "EUR", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 100, Mode: "monitored", Action: "stop_previews", ResumeRule: "manual"}
	for _, kind := range []string{"app", "project", "environment"} {
		p.Scope = BudgetScope{Kind: kind, ID: map[string]string{"app": a.AppID, "project": a.ProjectID, "environment": a.EnvironmentID}[kind]}
		amount, err := BudgetAmount(p, []ContractCosts{cost})
		if err != nil || amount != 75 {
			t.Fatalf("%s allocated net = %d, %v", kind, amount, err)
		}
	}
	p.Basis = "gross_usage"
	if amount, err := BudgetAmount(p, []ContractCosts{cost}); err != nil || amount != 100 {
		t.Fatalf("gross basis = %d, %v", amount, err)
	}
	p.Scope, p.Basis = BudgetScope{Kind: "account"}, "net_usage"
	if amount, err := BudgetAmount(p, []ContractCosts{cost}); err != nil || amount != 150 {
		t.Fatalf("shared account allowance = %d, %v", amount, err)
	}
	cost.Contracts[0].Allocations[0].NetMillicents = math.MaxInt64
	if _, err := BudgetAmount(p, []ContractCosts{cost}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("amount overflow: %v", err)
	}
}

// adr: 431 — strict guarantees cannot be inferred from a monitored threshold.
func TestBudgetValidation(t *testing.T) {
	valid := BudgetSpec{Name: "batch", Scope: BudgetScope{Kind: "job", ID: "job"}, Currency: "EUR", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, NotifyMillicents: []int64{500, 800}, Mode: "strict", Action: "suspend_background", DrainSeconds: 30, ResumeRule: "next_period"}
	if err := ValidateBudget(valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*BudgetSpec)
	}{
		{"scoped_net_strict", func(p *BudgetSpec) { p.Basis = "net_usage" }},
		{"selective_account_stop", func(p *BudgetSpec) { p.Scope = BudgetScope{Kind: "account"} }},
		{"strict_external_meter", func(p *BudgetSpec) { p.Meters = []string{"egress"} }},
		{"notify_strict", func(p *BudgetSpec) { p.Action = "notify" }},
		{"traffic_does_not_stop_compute", func(p *BudgetSpec) { p.Scope = BudgetScope{Kind: "account"}; p.Action = "reject_traffic" }},
		{"duplicate_meter", func(p *BudgetSpec) { p.Meters = []string{"compute", "compute"} }},
		{"duplicate_threshold", func(p *BudgetSpec) { p.NotifyMillicents = []int64{500, 500} }},
		{"threshold_above_limit", func(p *BudgetSpec) { p.NotifyMillicents = []int64{1001} }},
		{"negative_limit", func(p *BudgetSpec) { p.LimitMillicents = -1 }},
		{"missing_scope_identity", func(p *BudgetSpec) { p.Scope.ID = "" }},
		{"invalid_resume", func(p *BudgetSpec) { p.ResumeRule = "automatic" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			if err := ValidateBudget(p); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid contract accepted: %v", err)
			}
		})
	}
}
