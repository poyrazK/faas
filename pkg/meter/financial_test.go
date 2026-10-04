package meter

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 530 — snapshots follow activations, including returning to an old plan.
func TestFinancialPricingHistory(t *testing.T) {
	store := state.NewMemStore()
	a, err := store.CreateAccount(t.Context(), "financial-pricing@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	l := &Loop{store: store}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, step := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanHobby, start}, {api.PlanHobby, start.Add(time.Minute)}, {api.PlanPro, start.Add(2 * time.Minute)}, {api.PlanHobby, start.Add(3 * time.Minute)}} {
		if err := store.UpdateAccountPlan(t.Context(), a.ID, step.plan); err != nil {
			t.Fatal(err)
		}
		if err := l.recordFinancialPricing(t.Context(), step.at); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendUsage(t.Context(), a.ID, "app", "instance", step.at, 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	prices, err := store.ListFinancialPriceSnapshots(t.Context(), a.ID, start)
	if err != nil || len(prices) != 6 {
		t.Fatalf("price activations: %+v, %v", prices, err)
	}
	head, err := store.FinancialEvidenceHead(t.Context(), a.ID, start, start.AddDate(0, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListFinancialUsageEvidence(t.Context(), a.ID, start, start.AddDate(0, 1, 0), 0, head, 10)
	if err != nil || len(rows) != 4 {
		t.Fatalf("priced evidence: %+v, %v", rows, err)
	}
	if rows[0].PriceVersion == "" || rows[0].PriceVersion != rows[1].PriceVersion || rows[2].PriceVersion == rows[0].PriceVersion || rows[3].PriceVersion == rows[0].PriceVersion || rows[3].PriceVersion == rows[2].PriceVersion {
		t.Fatalf("lost historical price activation: %+v", rows)
	}
	if err := l.recordFinancialPricing(t.Context(), start.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	prices, err = store.ListFinancialPriceSnapshots(t.Context(), a.ID, start.AddDate(0, 1, 0))
	if err != nil || len(prices) != 2 {
		t.Fatalf("UTC period reset: %+v, %v", prices, err)
	}
}
