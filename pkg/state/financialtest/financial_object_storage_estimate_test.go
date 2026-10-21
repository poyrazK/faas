package financialtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectStorageEstimateTestStore interface {
	accountCreator
	state.FinancialObjectStorageEstimateStore
	CreateFinancialBudget(context.Context, string, string, string, financial.BudgetSpec) (state.FinancialBudget, error)
}

// adr: 20261020160000 — one estimate per account and month, newest
// observation wins, invalid estimates are refused, in memory and PG.
func TestFinancialObjectStorageEstimates(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store objectStorageEstimateTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			objectStorageEstimateSuite(t, store)
		})
	}
}

func objectStorageEstimateSuite(t *testing.T, store objectStorageEstimateTestStore) {
	ctx := t.Context()
	a := financialAccount(t, store)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	e := state.FinancialObjectStorageEstimate{
		AccountID: a.ID, PeriodStart: month, ObservedAt: month.Add(48 * time.Hour), UsageThrough: month.Add(47 * time.Hour), Fresh: true,
		Usage:     api.ObjectStorageUsage{StoredByteHours: 10, RequestCount: 20, EgressBytes: 30},
		Pricing:   api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1, RequestsMillicentsPerMillion: 2, EgressMillicentsPerGiB: 3},
		Allowance: api.ObjectStorageAllowance{StoredByteHours: 4, Requests: 5, EgressBytes: 6},
	}
	if _, err := store.FinancialObjectStorageEstimate(ctx, a.ID, month); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing estimate: %v", err)
	}
	if err := store.PutFinancialObjectStorageEstimate(ctx, e); err != nil {
		t.Fatal(err)
	}
	newer := e
	newer.ObservedAt, newer.UsageThrough, newer.Usage.StoredByteHours, newer.Fresh = e.ObservedAt.Add(time.Hour), e.UsageThrough.Add(time.Hour), 11, false
	if err := store.PutFinancialObjectStorageEstimate(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := store.PutFinancialObjectStorageEstimate(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := store.FinancialObjectStorageEstimate(ctx, a.ID, month.Add(5*24*time.Hour))
	if err != nil || got.Usage.StoredByteHours != 11 || got.Fresh || !got.ObservedAt.Equal(newer.ObservedAt) || got.Pricing != e.Pricing || got.Allowance != e.Allowance || got.Usage.RequestCount != 20 || got.Usage.EgressBytes != 30 {
		t.Fatalf("newest estimate: %+v, %v", got, err)
	}
	for name, mutate := range map[string]func(*state.FinancialObjectStorageEstimate){
		"mid-month period":      func(x *state.FinancialObjectStorageEstimate) { x.PeriodStart = month.Add(time.Hour) },
		"usage after observing": func(x *state.FinancialObjectStorageEstimate) { x.UsageThrough = x.ObservedAt.Add(time.Second) },
		"negative usage":        func(x *state.FinancialObjectStorageEstimate) { x.Usage.EgressBytes = -1 },
		"euro pricing":          func(x *state.FinancialObjectStorageEstimate) { x.Pricing.Currency = "EUR" },
	} {
		bad := e
		mutate(&bad)
		if err := store.PutFinancialObjectStorageEstimate(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}

	// The widened policy check accepts object_storage on a monitored account
	// budget; ValidateBudget still refuses it on a resource scope.
	spec := financialBudgetSpec()
	spec.Meters = []string{"compute", financial.MeterObjectStorage}
	if _, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec); err != nil {
		t.Fatalf("object storage budget: %v", err)
	}
	spec.Scope = financial.BudgetScope{Kind: "app", ID: uuid.NewString()}
	if _, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "editor", spec); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("app-scoped object storage budget: %v", err)
	}
}
