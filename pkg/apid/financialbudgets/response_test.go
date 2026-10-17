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

func TestFinancialBudgetResponseStaleMonitoring(t *testing.T) {
	s := state.NewMemStore()
	a, err := s.CreateAccount(t.Context(), "monitor-health@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "editor", financial.BudgetSpec{Name: "Usage", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 100, Mode: "monitored", Action: "notify", ResumeRule: "next_period", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(time.Hour)
	first, err := financialbudgets.Response(t.Context(), s, p, now)
	if err != nil || first.Status != "active" || !first.NotificationReady || first.EnforcementReady || first.Runtime.Evaluation != nil {
		t.Fatalf("initial monitoring: %+v, %v", first, err)
	}
	known := int64(90)
	e := financial.BudgetEvaluation{PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, KnownMillicents: &known, Fresh: true, CoverageComplete: true, Reasons: []string{}}
	if err := s.RecordFinancialBudgetEvaluation(t.Context(), a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	stale, err := financialbudgets.Response(t.Context(), s, p, now.Add(api.FinancialEvidenceFreshness+time.Second))
	if err != nil || stale.Runtime.Evaluation.Fresh {
		t.Fatalf("stale monitor still fresh: %+v, %v", stale, err)
	}
	fresh, err := financialbudgets.Response(t.Context(), s, p, now)
	if err != nil || !fresh.Runtime.Evaluation.Fresh {
		t.Fatalf("response mutated stored evidence: %+v, %v", fresh, err)
	}
}
