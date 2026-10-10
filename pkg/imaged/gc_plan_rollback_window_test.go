package imaged

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 972
func rollbackGenerations(account, app string, count int) []state.SnapshotForGC {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]state.SnapshotForGC, 0, count)
	for i := 0; i < count; i++ {
		dep := app + "-dep-" + string(rune('1'+i))
		rows = append(rows, row("init-"+dep, app, dep, account, app,
			state.SnapshotTierInit, false, t0.Add(time.Duration(i)*time.Minute), 1, 1))
	}
	return rows
}

// adr: 972
func TestPerPlanKeepRollbackWindow_UsesEachAccountsDepth(t *testing.T) {
	rows := append(rollbackGenerations("free", "small", 4), rollbackGenerations("scale", "big", 4)...)
	depth := func(accountID string) int {
		return map[string]int{"free": 2, "scale": 5}[accountID]
	}

	ids := collectIDs(perPlanKeepRollbackWindow(rows, depth))
	want := []string{"init-small-dep-1", "init-small-dep-2"}
	if !sortedStringsEqual(ids, want) {
		t.Fatalf("per-plan drops = %v, want %v", ids, want)
	}
}

// adr: 972
func TestEvictOldestFromHeaviestAccountWithDepth_ProtectsPlanWindow(t *testing.T) {
	rows := rollbackGenerations("scale", "big", 4)
	if got := evictOldestFromHeaviestAccountWithDepth(rows, fixedRollbackDepth(5)); got != nil {
		t.Fatalf("pressure eviction inside a 5-deep window = %v, want none", got)
	}
	got := evictOldestFromHeaviestAccountWithDepth(rows, fixedRollbackDepth(2))
	if len(got) != 1 || got[0].ID != "init-big-dep-1" {
		t.Fatalf("pressure eviction outside a 2-deep window = %v, want oldest generation", got)
	}
}

// adr: 972
func TestRollbackDepthResolver_UsesPlanAndFailsSafe(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	free, err := store.CreateAccount(ctx, "free@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount free: %v", err)
	}
	scale, err := store.CreateAccount(ctx, "scale@example.com", api.PlanScale)
	if err != nil {
		t.Fatalf("CreateAccount scale: %v", err)
	}
	loop := &Loop{store: store, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	depth := loop.rollbackDepthResolver(ctx)

	for _, tc := range []struct {
		name, accountID string
		want            int
	}{
		{"free plan", free.ID, api.MustLimitsFor(api.PlanFree).RollbackRetentionDeployments},
		{"scale plan", scale.ID, api.MustLimitsFor(api.PlanScale).RollbackRetentionDeployments},
		{"missing account keeps deepest window", "00000000-0000-0000-0000-000000000000", api.MaxRollbackRetentionDeployments()},
		{"empty account keeps deepest window", "", api.MaxRollbackRetentionDeployments()},
	} {
		if got := depth(tc.accountID); got != tc.want {
			t.Errorf("%s: depth = %d, want %d", tc.name, got, tc.want)
		}
	}
}
