package state_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 240
func TestMemPlatformTenantRequestBudgetScopesAndUpdates(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "request-budget@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "budget-customer", "Budget customer", 250)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetPlatformTenantRequestBudget(ctx, account.ID, tenant.ID)
	if err != nil || initial.MaxRequestsPerMinute != 0 || initial.DayUsed != 0 {
		t.Fatalf("default budget=%+v err=%v", initial, err)
	}
	if _, err := store.SetPlatformTenantRequestBudget(ctx, account.ID, tenant.ID, 2, 3); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if decision, err := store.AdmitPlatformTenantRequest(ctx, account.ID, tenant.ID); err != nil || !decision.Allowed {
			t.Fatalf("admission %d = %+v, %v", i, decision, err)
		}
	}
	if decision, err := store.AdmitPlatformTenantRequest(ctx, account.ID, tenant.ID); err != nil || decision.Allowed || decision.Scope != "minute" || decision.RetryAfterSeconds < 1 {
		t.Fatalf("minute exhaustion = %+v, %v", decision, err)
	}
	if _, err := store.SetPlatformTenantRequestBudget(ctx, account.ID, tenant.ID, 0, 3); err != nil {
		t.Fatal(err)
	}
	if decision, err := store.AdmitPlatformTenantRequest(ctx, account.ID, tenant.ID); err != nil || !decision.Allowed {
		t.Fatalf("after minute disabled = %+v, %v", decision, err)
	}
	if decision, err := store.AdmitPlatformTenantRequest(ctx, account.ID, tenant.ID); err != nil || decision.Allowed || decision.Scope != "day" {
		t.Fatalf("day exhaustion = %+v, %v", decision, err)
	}
	current, err := store.GetPlatformTenantRequestBudget(ctx, account.ID, tenant.ID)
	if err != nil || current.DayUsed != 3 || current.MaxRequestsPerMinute != 0 || current.MaxRequestsPerDay != 3 {
		t.Fatalf("current budget=%+v err=%v", current, err)
	}
	if _, err := store.SetPlatformTenantRequestBudget(ctx, account.ID, tenant.ID, 0, 0); err != nil {
		t.Fatal(err)
	}
	if decision, err := store.AdmitPlatformTenantRequest(ctx, account.ID, tenant.ID); err != nil || !decision.Allowed {
		t.Fatalf("disabled policy = %+v, %v", decision, err)
	}
	foreign, err := store.CreateAccount(ctx, "request-budget-foreign@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPlatformTenantRequestBudget(ctx, foreign.ID, tenant.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read err=%v", err)
	}
	if _, err := store.SetPlatformTenantRequestBudget(ctx, foreign.ID, tenant.ID, 1, 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account write err=%v", err)
	}
}

// adr: 240
func TestPgPlatformTenantRequestBudgetSerializesAcrossCallers(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, _ := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "shared-budget", "Shared budget", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantRequestBudget(ctx, accountID, tenant.ID, 0, 7); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	errCh := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := store.AdmitPlatformTenantRequest(ctx, accountID, tenant.ID)
			if err != nil {
				errCh <- err
				return
			}
			if decision.Allowed {
				accepted.Add(1)
			} else if decision.Scope != "day" {
				errCh <- errors.New("unexpected denial scope: " + decision.Scope)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := accepted.Load(); got != 7 {
		t.Fatalf("admitted %d requests, want exactly 7", got)
	}
	budget, err := store.GetPlatformTenantRequestBudget(ctx, accountID, tenant.ID)
	if err != nil || budget.DayUsed != 7 {
		t.Fatalf("persisted counter=%+v err=%v", budget, err)
	}
	if replay, err := store.SetPlatformTenantRequestBudget(ctx, accountID, tenant.ID, 0, 7); err != nil || !replay.UpdatedAt.Equal(budget.UpdatedAt) {
		t.Fatalf("idempotent policy replay=%+v err=%v", replay, err)
	}
}
