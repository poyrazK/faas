package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCostControlMonthCloseCannotRefreshAnOldStorageEstimate(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "audit-stale-storage@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	observed := start.Add(10 * 24 * time.Hour)
	estimate := state.FinancialObjectStorageEstimate{AccountID: acct.ID, PeriodStart: start, ObservedAt: observed, UsageThrough: observed, Fresh: true,
		Usage:   api.ObjectStorageUsage{StoredByteHours: 100 * (1 << 30), RequestCount: 2000000, EgressBytes: 1 << 30},
		Pricing: api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 2300, RequestsMillicentsPerMillion: 400, EgressMillicentsPerGiB: 9000}}
	if err := store.PutFinancialObjectStorageEstimate(t.Context(), estimate); err != nil {
		t.Fatal(err)
	}
	storage := func(now time.Time) api.FinancialMeterCosts {
		t.Helper()
		report, err := ReadFinancialCosts(t.Context(), store, acct.ID, start, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range report.Meters {
			if m.Meter == "object_storage" {
				return m
			}
		}
		t.Fatal("fixture has no object-storage meter")
		return api.FinancialMeterCosts{}
	}
	before := storage(end.Add(-time.Minute))
	if before.Coverage.Fresh {
		t.Fatalf("fixture not stale before closing: %+v", before.Coverage)
	}
	after := storage(end.Add(time.Minute))
	if after.Coverage.Fresh {
		t.Errorf("month closing revived the same old object-storage estimate as fresh: observed=%v usage_through=%v period_end=%v; coverage complete=%v reported=%d/%d minutes reasons=%v", estimate.ObservedAt, estimate.UsageThrough, end, after.Coverage.Complete, after.Coverage.CompleteMinutes, after.Coverage.ExpectedMinutes, after.Coverage.Reasons)
	}
}
