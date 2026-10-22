package meter_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019193500 — a projected overrun emails the account once, beside
// threshold alerts, with the projection and the limit in exact money text.
func TestFinancialBudgetProjectionEmail(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	a, err := store.CreateAccount(ctx, "budget-projection@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "Monthly compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000000, NotifyMillicents: []int64{800000}, Mode: "monitored", Action: "notify", ResumeRule: "next_period", Enabled: true}
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(10 * 24 * time.Hour)
	known, projected := int64(400000), int64(1250000)
	e := financial.BudgetEvaluation{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), AsOf: now, EvaluatedAt: now, EvidenceThroughID: 1, KnownMillicents: &known, ProjectedMillicents: &projected, Fresh: true, CoverageComplete: true}
	if err := store.RecordFinancialBudgetEvaluation(ctx, a.ID, p.ID, p.Revision, e); err != nil {
		t.Fatal(err)
	}
	sender := &fakeMailer{}
	loop := meter.NewLoop(store, nil, nil, nil, nil, sender, nil, nil, nil, func() time.Time { return now }, nil, nil, nil)
	if err := loop.RunFinancialBudgetDeliveriesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 1 {
		t.Fatalf("sent %d emails, want the projection only", sender.count())
	}
	msg := sender.sent[0]
	if msg.To[0] != a.Email || !strings.Contains(msg.Subject, "projected to exceed") || !strings.Contains(msg.TextBody, "USD 12.50000") || !strings.Contains(msg.TextBody, "USD 10.00000 limit") || !strings.Contains(msg.TextBody, "not an invoice") {
		t.Fatalf("projection email = %+v", msg)
	}
	if err := loop.RunFinancialBudgetDeliveriesOnce(ctx); err != nil || sender.count() != 1 {
		t.Fatalf("projection resent: %d, %v", sender.count(), err)
	}
}
