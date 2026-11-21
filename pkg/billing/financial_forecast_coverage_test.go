package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// Cost-control review 2026-11-19: one lost sampling minute used to stop the
// month's forecasts and projected-overrun alerts. A month sampled at least
// 99% with the last hour whole is now projectable.
func TestProjectableCoverage(t *testing.T) {
	const expected = 10_000
	for _, tc := range []struct {
		name                                 string
		complete, retainedWhole, recentWhole bool
		minutes                              int64
		want                                 bool
	}{
		{"complete", true, true, true, expected, true},
		{"one lost minute", false, true, true, expected - 1, true},
		{"exactly 99%", false, true, true, expected * 99 / 100, true},
		{"below 99%", false, true, true, expected*99/100 - 1, false},
		{"the last hour has a gap", false, true, false, expected - 1, false},
		{"retention started inside the month", false, false, true, expected - 1, false},
		{"more minutes than elapsed", false, true, true, expected + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := projectableCoverage(tc.complete, tc.retainedWhole, tc.recentWhole, tc.minutes, expected); got != tc.want {
				t.Fatalf("projectable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBudgetEvaluationProjectsAProjectableMonth(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	report := api.FinancialCostsResponse{PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), AsOf: start.Add(48 * time.Hour),
		Meters: []api.FinancialMeterCosts{{Meter: "compute",
			Coverage: api.FinancialMeterCoverage{Complete: false, Projectable: true, Fresh: true, Reasons: []string{"missing_sampling_windows"}},
			Accrued:  financial.ContractCosts{Meter: "compute"}}}}
	spec := financial.BudgetSpec{Name: "month", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"},
		Basis: "gross_usage", LimitMillicents: 1000, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	got, err := EvaluateFinancialBudget(report, spec, start.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.CoverageComplete || !got.Projectable || got.ProjectionReason == "incomplete_evidence" {
		t.Fatalf("evaluation = %+v; want projectable though incomplete", got)
	}
	report.Meters[0].Coverage.UnpricedQuantity = 1
	if got, _ := EvaluateFinancialBudget(report, spec, start.Add(48*time.Hour)); got.Projectable || got.ProjectionReason != "incomplete_evidence" {
		t.Fatalf("unpriced usage evaluation = %+v; want no projection", got)
	}
}
