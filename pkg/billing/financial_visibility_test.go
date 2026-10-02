package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type visibilityTestStore struct {
	*state.MemStore
	retained, timeNow time.Time
}

func (s *visibilityTestStore) FinancialEvidenceCoverage(context.Context) (time.Time, error) {
	return s.retained, nil
}
func (s *visibilityTestStore) FinancialSamplingCoverage(ctx context.Context, start, end time.Time) (state.FinancialSamplingCoverage, error) {
	c, err := s.MemStore.FinancialSamplingCoverage(ctx, start, end)
	c.ObservedAt = s.timeNow.Add(-time.Minute)
	return c, err
}

// adr: 431 — forecasts require complete coverage and retained prices.
func TestFinancialVisibility(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &visibilityTestStore{MemStore: state.NewMemStore(), retained: start, timeNow: start.Add(24 * time.Hour)}
	a, err := store.CreateAccount(t.Context(), "financial-view@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	p := state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EffectiveFrom: start, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "hobby-v1", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: 50 * api.SecondsPerGBHour}}
	if _, err := store.PutFinancialPriceSnapshot(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := store.AppendUsage(t.Context(), a.ID, "app", string(rune('a'+i)), start.Add(time.Duration(i)*time.Minute), 125*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	for at := start; at.Before(store.timeNow); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadFinancialCosts(t.Context(), store, a.ID, start, store.timeNow)
	if err != nil {
		t.Fatal(err)
	}
	compute := got.Meters[0]
	if !compute.Coverage.Complete || got.KnownUsageMillicents != 200_000 || !compute.Forecast.Available || *compute.Forecast.ProjectedNetMillicents != 7_700_000 {
		t.Fatalf("priced forecast: %+v", got)
	}
	if got.Meters[1].Forecast.Available || got.InvoiceReconciliation != "not_reconciled" || len(got.MissingBillComponents) == 0 {
		t.Fatalf("unknown bill coverage presented as exact: %+v", got)
	}
	store.retained = start.Add(time.Minute)
	got, err = ReadFinancialCosts(t.Context(), store, a.ID, start, store.timeNow)
	if err != nil || got.Meters[0].Coverage.Complete || got.Meters[0].Forecast.Available || got.Meters[0].Forecast.Reason != "incomplete_coverage" {
		t.Fatalf("retention gap ignored: %+v, %v", got, err)
	}
	store.retained = start
	store.timeNow = store.timeNow.Add(time.Minute)
	got, err = ReadFinancialCosts(t.Context(), store, a.ID, start, store.timeNow)
	if err != nil || got.Meters[0].Coverage.Complete || got.Meters[0].Forecast.Available {
		t.Fatalf("missing sample treated as zero: %+v, %v", got, err)
	}
	other, err := store.CreateAccount(t.Context(), "other-financial-view@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	got, err = ReadFinancialCosts(t.Context(), store, other.ID, start, store.timeNow)
	if err != nil || got.KnownUsageMillicents != 0 || got.Meters[0].Coverage.Complete || got.Meters[0].Forecast.Available {
		t.Fatalf("missing prices or tenant isolation ignored: %+v, %v", got, err)
	}
}
