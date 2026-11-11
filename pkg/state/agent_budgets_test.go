// adr: 20261111114059
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// exerciseAgentBudgets checks a key's budget round-trips (default, a
// number, unlimited, back to default), spend adds up per UTC month and never
// shrinks, and a budget approval keeps its cost.
func exerciseAgentBudgets(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	acct, err := store.CreateAccount(ctx, "agent-budget-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "budget-agent-"+suffix, []string{api.ScopeAgent, api.ScopeDeployWrite})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if b, err := store.AgentKeyBudget(ctx, key.ID); err != nil || b.Set {
		t.Fatalf("fresh key budget = %+v, %v; want none set", b, err)
	}
	for _, want := range []state.AgentKeyBudget{{Set: true, MonthlyCents: 4200}, {Set: true, Unlimited: true}, {}} {
		if err := store.SetAgentKeyBudget(ctx, key.ID, want, acct.ID); err != nil {
			t.Fatalf("SetAgentKeyBudget(%+v): %v", want, err)
		}
		if got, err := store.AgentKeyBudget(ctx, key.ID); err != nil || got != want {
			t.Fatalf("budget = %+v, %v; want %+v", got, err, want)
		}
	}
	if err := store.SetAgentKeyBudget(ctx, key.ID, state.AgentKeyBudget{Set: true, MonthlyCents: -1}, acct.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("negative budget = %v; want ErrConflict", err)
	}

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, add := range []int64{1500, 0, -700, 2500} {
		if err := store.AddAgentBudgetSpend(ctx, key.ID, now, add); err != nil {
			t.Fatalf("AddAgentBudgetSpend(%d): %v", add, err)
		}
	}
	if spent, err := store.AgentBudgetSpent(ctx, key.ID, now.AddDate(0, 0, 5)); err != nil || spent != 4000 {
		t.Fatalf("spent this month = %d, %v; want 4000 (nothing negative counts)", spent, err)
	}
	if spent, err := store.AgentBudgetSpent(ctx, key.ID, now.AddDate(0, 1, 0)); err != nil || spent != 0 {
		t.Fatalf("spent next month = %d, %v; want 0", spent, err)
	}

	request := state.AgentApproval{
		ID: uuid.NewString(), KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Category: api.AgentApprovalBudget,
		Method: "PATCH", Path: "/v1/apps/warm", ExpiresAt: time.Now().Add(api.AgentApprovalTTL), BudgetCostMillicents: 1_853_000,
	}
	opened, err := store.RequestAgentApproval(ctx, request)
	if err != nil {
		t.Fatalf("RequestAgentApproval(budget): %v", err)
	}
	if got, err := store.AgentApproval(ctx, opened.ID); err != nil || got.Category != api.AgentApprovalBudget || got.BudgetCostMillicents != 1_853_000 {
		t.Fatalf("budget approval = %+v, %v", got, err)
	}
	if lines, err := store.PendingAgentApprovalNotificationsForKey(ctx, key.ID); err == nil {
		for _, line := range lines {
			if line.ApprovalID == opened.ID && line.BudgetCostMillicents != 1_853_000 {
				t.Fatalf("notification line cost = %d", line.BudgetCostMillicents)
			}
		}
	}
}

func TestMemStoreAgentBudgets(t *testing.T) {
	exerciseAgentBudgets(t, state.NewMemStore())
}

func TestPgStoreAgentBudgets(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentBudgets(t, state.NewPgStore(pool))
}
