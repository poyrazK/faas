package state

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"testing"
	"time"
)

// A strict net compute commitment must cover the same supported usage the
// provider bills, including the allowance rules after a mid-month downgrade.
func TestCostControlStrictFundingBoundsBilledComputeAfterPlanChange(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	change := month.Add(15 * 24 * time.Hour)
	through := change.Add(24 * time.Hour)
	prices := []FinancialPriceSnapshot{}
	for _, term := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanPro, month}, {api.PlanHobby, change}} {
		prices = append(prices, FinancialPriceSnapshot{PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: term.at, Plan: term.plan, DeliveryMode: "live", Price: financial.Price{Version: string(term.plan), Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(term.plan)}})
	}
	quantity := int64(700) * api.SecondsPerGBHour
	grant, err := computeAllowance(prices, month, through)
	if err != nil {
		t.Fatal(err)
	}
	book := financial.ComputeBook{AllowanceGranted: grant, QuantityCommitted: quantity}
	if err := computeAccountReprice(&book); err != nil {
		t.Fatal(err)
	}
	billed, err := MonthBilledComputeMBSeconds(api.PlanHobby, prices, through, quantity, func(time.Time) (int64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	billedMoney, err := financial.ComputeOverageBound(billed, 0, api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("strict allowance=%d GB-h; billed allowance=%d GB-h; strict commitment=%d millicents; provider billable=%d millicents", grant/api.SecondsPerGBHour, MonthComputeIncludedMBSeconds(api.PlanHobby, prices, through)/api.SecondsPerGBHour, book.Committed, billedMoney)
	if billedMoney > book.Committed {
		t.Errorf("strict compute commitment does not cover the provider's billable compute: billed=%d, funded=%d", billedMoney, book.Committed)
	}
}

func TestCostControlCanDisableBudgetAfterAppDeletion(t *testing.T) {
	store := NewMemStore()
	account, err := store.CreateAccount(t.Context(), "audit-budget-deleted@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "audit-deleted-budget-target", RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "audit", Scope: financial.BudgetScope{Kind: "app", ID: app.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1000, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	policy, err := store.CreateFinancialBudget(t.Context(), account.ID, uuid.NewString(), "audit", spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	spec.Enabled = false
	if _, err := store.UpdateFinancialBudget(t.Context(), account.ID, policy.ID, policy.Revision, "audit", spec); err != nil {
		t.Errorf("cannot disable a saved budget after its app is deleted: %v", err)
	}
}
