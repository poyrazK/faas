package billing

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020160000 — apid's object-storage estimate becomes an
// account-level meter: priced like the usage endpoint, projected at the
// run rate, stale when apid stops observing, and budgetable on monitored
// account budgets only.
func TestFinancialObjectStorageMeter(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(10 * 24 * time.Hour)
	store := &visibilityTestStore{MemStore: state.NewMemStore(), retained: start, timeNow: now}
	a, err := store.CreateAccount(t.Context(), "object-budget@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	report := func() api.FinancialCostsResponse {
		t.Helper()
		got, err := ReadFinancialCosts(t.Context(), store, a.ID, start, now)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := report(); len(got.Meters) != 2 || got.Scope != "retained_compute_and_interface_egress" {
		t.Fatalf("object storage reported without an estimate: %+v", got)
	}

	const gib = int64(1) << 30
	e := state.FinancialObjectStorageEstimate{
		AccountID: a.ID, PeriodStart: start, ObservedAt: now.Add(-time.Minute), UsageThrough: start.Add(9 * 24 * time.Hour), Fresh: true,
		Usage:     api.ObjectStorageUsage{StoredByteHours: 365 * gib, RequestCount: 2_000_000, EgressBytes: 3 * gib},
		Pricing:   api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 2300, RequestsMillicentsPerMillion: 400, EgressMillicentsPerGiB: 9000},
		Allowance: api.ObjectStorageAllowance{StoredByteHours: 730 * gib, Requests: 1_000_000, EgressBytes: gib},
	}
	if err := store.PutFinancialObjectStorageEstimate(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	got := report()
	if len(got.Meters) != 3 || got.Scope != "retained_compute_interface_egress_and_object_storage" {
		t.Fatalf("object storage meter missing: %+v", got)
	}
	m := got.Meters[2]
	// Gross: storage 1150 + requests 800 + egress 27000. Net: the allowance
	// covers storage, half the requests and one GiB of egress.
	if m.Meter != financial.MeterObjectStorage || !m.Coverage.Complete || !m.Coverage.Fresh || m.Accrued.Contracts[0].GrossMillicents != 28950 || m.Accrued.NetMillicents != 18400 || got.KnownUsageMillicents != 18400 {
		t.Fatalf("object storage costs: %+v", m)
	}
	f := m.Forecast
	if !f.Available || f.ProjectedNetMillicents == nil || f.ProjectedGrossMillicents == nil || *f.ProjectedGrossMillicents < 28950*31/9-3 || *f.ProjectedGrossMillicents > 28950*31/9+3 || *f.ProjectedNetMillicents <= 18400 {
		t.Fatalf("object storage forecast: %+v", f)
	}

	spec := financial.BudgetSpec{Name: "storage", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{financial.MeterObjectStorage}, Basis: "net_usage", LimitMillicents: 50000, NotifyMillicents: []int64{}, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	ev, err := EvaluateFinancialBudget(got, spec, now)
	if err != nil || *ev.KnownMillicents != 18400 || ev.ProjectedMillicents == nil || *ev.ProjectedMillicents != *f.ProjectedNetMillicents {
		t.Fatalf("net budget evaluation: %+v %v", ev, err)
	}
	spec.Basis = "gross_usage"
	if ev, err := EvaluateFinancialBudget(got, spec, now); err != nil || *ev.KnownMillicents != 28950 || *ev.ProjectedMillicents != *f.ProjectedGrossMillicents {
		t.Fatalf("gross budget evaluation: %+v %v", ev, err)
	}

	for name, mutate := range map[string]func(*financial.BudgetSpec){
		"app scope": func(p *financial.BudgetSpec) {
			p.Scope = financial.BudgetScope{Kind: "app", ID: "7b2d7b6e-0c8a-4a7e-9a59-5f6f7e0f9b11"}
		},
		"strict": func(p *financial.BudgetSpec) { p.Mode, p.Action, p.Basis = "strict", "suspend_workloads", "net_usage" },
	} {
		bad := spec
		mutate(&bad)
		if err := financial.ValidateBudget(bad); !errors.Is(err, financial.ErrInvalid) {
			t.Errorf("%s accepted object_storage: %v", name, err)
		}
	}

	// apid stopped observing: the meter is stale and no longer projected.
	e.ObservedAt, e.UsageThrough = now.Add(-api.FinancialObjectStorageEstimateFreshness-time.Minute), start.Add(9*24*time.Hour)
	stale := state.NewMemStore()
	store.MemStore = stale
	if a, err = stale.CreateAccount(t.Context(), "object-stale@example.com", api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	e.AccountID = a.ID
	if err := stale.PutFinancialObjectStorageEstimate(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if m := report().Meters[2]; m.Coverage.Fresh || m.Forecast.Available || m.Forecast.Reason != "stale_evidence" {
		t.Fatalf("stale estimate: %+v", m)
	}
}
