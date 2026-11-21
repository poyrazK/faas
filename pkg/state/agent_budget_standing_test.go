package state_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Cost-control review 2026-11-19 (N9, N10): an agent key's budget holds the
// standing cost its changes keep running. It never resets, a decrease gives
// budget back, and a reservation is atomic.
func exerciseAgentBudgetStanding(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	budgets, ok := store.(state.AgentBudgetStore)
	if !ok {
		t.Fatal("store keeps no agent budgets")
	}
	acct, err := store.CreateAccount(ctx, "standing-budget@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "agent", []string{api.ScopeAgent})
	if err != nil {
		t.Fatal(err)
	}
	spent := func(at time.Time) int64 {
		t.Helper()
		v, err := budgets.AgentBudgetSpent(ctx, key.ID, at)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	now := time.Now()
	if ok, err := budgets.ReserveAgentBudget(ctx, key.ID, 600, 1000); err != nil || !ok {
		t.Fatalf("reserve within budget = %v, %v", ok, err)
	}
	if ok, err := budgets.ReserveAgentBudget(ctx, key.ID, 600, 1000); err != nil || ok {
		t.Fatalf("reserve past budget = %v, %v; want refused", ok, err)
	}
	if got := spent(now); got != 600 {
		t.Fatalf("standing after a refused reservation = %d, want 600", got)
	}
	// No monthly reset: next month still sees the standing cost.
	if got := spent(now.AddDate(0, 1, 0)); got != 600 {
		t.Fatalf("standing next month = %d, want 600", got)
	}
	// Lowering cost gives budget back, never below zero.
	if err := budgets.AddAgentBudgetSpend(ctx, key.ID, now, -200); err != nil {
		t.Fatal(err)
	}
	if got := spent(now); got != 400 {
		t.Fatalf("standing after a decrease = %d, want 400", got)
	}
	if err := budgets.AddAgentBudgetSpend(ctx, key.ID, now, -10_000); err != nil {
		t.Fatal(err)
	}
	if got := spent(now); got != 0 {
		t.Fatalf("standing after a large decrease = %d, want 0", got)
	}
	// Concurrent reservations cannot both fit the same headroom.
	var granted atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := budgets.ReserveAgentBudget(ctx, key.ID, 300, 1000); err == nil && ok {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != 3 || spent(now) != 900 {
		t.Fatalf("concurrent reservations granted %d (standing %d), want 3 (900)", granted.Load(), spent(now))
	}
}

func TestMemStoreAgentBudgetStanding(t *testing.T) {
	exerciseAgentBudgetStanding(t, state.NewMemStore())
}

func TestPgStoreAgentBudgetStanding(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentBudgetStanding(t, state.NewPgStore(pool))
}
