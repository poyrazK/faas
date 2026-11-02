package meter_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261031211500 — meterd evaluates an organization's budgets against the
// usage recorded under the organization, and alerts its billing contact.
func TestFinancialBudgetMonitorEvaluatesOrganizations(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	// A MemStore organization created on Free owns its resources, and keeps
	// them on a paid plan.
	org, err := store.CreateOrg(ctx, state.Org{Slug: "monitored-" + uuid.NewString()[:8], Name: "Monitored Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgBillingEmail(ctx, org.ID, "billing@monitored.example"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	price := state.FinancialPriceSnapshot{AccountID: org.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EffectiveFrom: start, Plan: api.PlanPro, DeliveryMode: "live", Price: financial.Price{Version: "org-budget-v1", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1}}
	if _, err := store.PutFinancialPriceSnapshot(ctx, price); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-time.Minute).Truncate(time.Minute)
	if err := store.AppendUsage(ctx, org.ID, uuid.NewString(), uuid.NewString(), at, 90, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
		t.Fatal(err)
	}
	// Gross usage: the organization's Pro activation grants an allowance that
	// would absorb this little usage on a net basis.
	spec := financial.BudgetSpec{Name: "Company compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 100, NotifyMillicents: []int64{80}, Mode: "monitored", Action: "notify", ResumeRule: "next_period", Enabled: true}
	p, err := store.CreateFinancialBudget(ctx, org.ID, uuid.NewString(), "account:member", spec)
	if err != nil {
		t.Fatal(err)
	}
	// The evaluation clock must not precede the sampling window's observation.
	now = time.Now().UTC()
	sender := &fakeMailer{}
	loop := meter.NewLoop(store, nil, nil, nil, nil, sender, nil, nil, nil, func() time.Time { return now }, nil, nil, nil)
	if err := loop.RunFinancialBudgetsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := loop.RunFinancialBudgetDeliveriesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sender.count() != 1 || sender.sent[0].To[0] != "billing@monitored.example" {
		runtime, err := store.FinancialBudgetRuntime(ctx, org.ID, p.ID, p.Revision, start)
		if err == nil && runtime.Evaluation != nil {
			t.Fatalf("organization alert = %+v, want one mail to the billing contact (evaluation %+v)", sender.sent, *runtime.Evaluation)
		}
		t.Fatalf("organization alert = %+v, want one mail to the billing contact (runtime %+v, %v)", sender.sent, runtime, err)
	}
}
