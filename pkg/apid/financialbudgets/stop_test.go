package financialbudgets_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/financialbudgets"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFinancialBudgetManualStopAcrossMonths(t *testing.T) {
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "manual-stop@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", financial.BudgetSpec{Name: "Previews", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1, Mode: "monitored", Action: "stop_previews", ResumeRule: "manual", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now, amount := start.Add(time.Hour), int64(1)
	if err := s.RecordFinancialBudgetEvaluation(t.Context(), a.ID, p.ID, 1, financial.BudgetEvaluation{PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &amount, Fresh: true}); err != nil {
		t.Fatal(err)
	}
	out, err := financialbudgets.Response(t.Context(), s, p, start.AddDate(0, 1, 0))
	if err != nil || !out.EnforcementReady || out.Runtime.Evaluation != nil || len(out.Runtime.Stops) != 1 || len(out.Runtime.Stops[0].Targets) != 0 {
		t.Fatalf("manual decision disappeared at month reset: %+v %v", out, err)
	}
}
