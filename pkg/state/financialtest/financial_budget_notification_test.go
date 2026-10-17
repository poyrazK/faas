package financialtest

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type financialBudgetNotificationTestStore interface {
	state.Store
	state.FinancialBudgetStore
	state.FinancialBudgetNotificationStore
}

// adr: 1500 — only enabled monitored notify budgets are listed; a threshold is
// claimed once per budget and month; outcomes cover every pending threshold
// up to the one delivered.
func TestFinancialBudgetNotificationStores(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store financialBudgetNotificationTestStore = state.NewMemStore()
			if backend == "postgres" {
				pg, _, _ := financialPostgres(t)
				store = pg
			}
			financialBudgetNotificationSuite(t, store)
		})
	}
}

func financialBudgetNotificationSuite(t *testing.T, store financialBudgetNotificationTestStore) {
	a := financialAccount(t, store)
	create := func(mutate func(*financial.BudgetSpec)) state.FinancialBudget {
		t.Helper()
		spec := financialBudgetSpec()
		mutate(&spec)
		p, err := store.CreateFinancialBudget(t.Context(), a.ID, uuid.NewString(), "test", spec)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	active := create(func(*financial.BudgetSpec) {})
	create(func(s *financial.BudgetSpec) { s.Enabled = false })
	create(func(s *financial.BudgetSpec) { s.Action, s.DrainSeconds = "suspend_workloads", 30 })
	deleted := create(func(*financial.BudgetSpec) {})
	if _, err := store.DeleteFinancialBudget(t.Context(), a.ID, deleted.ID, deleted.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListFinancialBudgetNotifyPolicies(t.Context(), api.FinancialBudgetNotifyBatch)
	if err != nil || len(listed) != 1 || listed[0].ID != active.ID {
		t.Fatalf("notify policies = %+v, %v; want only %s", listed, err, active.ID)
	}

	period, _ := financialPeriod()
	claim := func(threshold, known int64) (bool, error) {
		return store.ClaimFinancialBudgetNotification(t.Context(), state.FinancialBudgetNotification{
			AccountID: a.ID, PolicyID: active.ID, PeriodStart: period, ThresholdMillicents: threshold,
			PolicyRevision: active.Revision, KnownMillicents: known, CoverageComplete: true,
		})
	}
	for _, threshold := range []int64{500, 1000} {
		if fresh, err := claim(threshold, 1200); err != nil || !fresh {
			t.Fatalf("claim %d: %t, %v", threshold, fresh, err)
		}
		if fresh, err := claim(threshold, 1300); err != nil || fresh {
			t.Fatalf("repeat claim %d: %t, %v; want an existing row", threshold, fresh, err)
		}
	}
	if _, err := claim(2000, 1200); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("claim below threshold: %v", err)
	}
	if _, err := store.ClaimFinancialBudgetNotification(t.Context(), state.FinancialBudgetNotification{
		AccountID: a.ID, PolicyID: active.ID, PeriodStart: period.Add(time.Hour), ThresholdMillicents: 1, PolicyRevision: 1, KnownMillicents: 1,
	}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("claim off a month boundary: %v", err)
	}

	pending, err := store.ListPendingFinancialBudgetNotifications(t.Context(), api.FinancialBudgetNotifyBatch)
	if err != nil || len(pending) != 2 || pending[0].KnownMillicents != 1200 || !pending[0].CoverageComplete || pending[0].Status != state.FinancialBudgetNotificationPending {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	record := func(maxThreshold int64, outcome, lastError string) int64 {
		t.Helper()
		changed, err := store.RecordFinancialBudgetNotificationOutcome(t.Context(), active.ID, period, maxThreshold, outcome, lastError)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	if changed := record(1000, state.FinancialBudgetNotificationFailed, "smtp down"); changed != 2 {
		t.Fatalf("failed attempt changed %d rows, want 2", changed)
	}
	pending, _ = store.ListPendingFinancialBudgetNotifications(t.Context(), api.FinancialBudgetNotifyBatch)
	if len(pending) != 2 || pending[0].Attempts != 1 || pending[0].LastError != "smtp down" || pending[0].FinishedAt != nil {
		t.Fatalf("after failure = %+v", pending)
	}
	if changed := record(500, state.FinancialBudgetNotificationSent, ""); changed != 1 {
		t.Fatalf("sent changed %d rows, want 1", changed)
	}
	if changed := record(1000, state.FinancialBudgetNotificationCancelled, "budget disabled"); changed != 1 {
		t.Fatalf("cancelled changed %d rows, want 1", changed)
	}
	if changed := record(1000, state.FinancialBudgetNotificationSent, ""); changed != 0 {
		t.Fatalf("finished rows changed again: %d", changed)
	}
	if pending, err := store.ListPendingFinancialBudgetNotifications(t.Context(), api.FinancialBudgetNotifyBatch); err != nil || len(pending) != 0 {
		t.Fatalf("pending after outcomes = %+v, %v", pending, err)
	}
	if fresh, err := claim(500, 1500); err != nil || fresh {
		t.Fatalf("finished threshold claimed again: %t, %v", fresh, err)
	}
	if _, err := store.RecordFinancialBudgetNotificationOutcome(t.Context(), active.ID, period, 1000, "pending", ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unknown outcome: %v", err)
	}
}
