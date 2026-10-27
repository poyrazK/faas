package edgerules

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-27: the convergence barrier waits for every
// serving gateway except one whose edge listener stopped reporting; a gateway
// that never reported (an older release) is still waited for.
func TestServingEdgeRuleNodesSkipsStaleListeners(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	role, gateway := "compute-node", "tcp://10.0.0.1:8080"
	for _, name := range []string{"edge-fresh", "edge-stale", "edge-legacy"} {
		if _, err := mem.CreateComputeNode(ctx, state.ComputeNode{Name: name, TargetURL: "tcp://10.0.0.1:9000", VPCPUs: 8, MemMB: 8192,
			MaxConcurrency: 10, AdmissionCeilingMB: 4096, VCPUBudget: 8, Active: true, Role: &role, GatewayTargetURL: &gateway}); err != nil {
			t.Fatal(err)
		}
	}
	if err := mem.TouchEdgeListenerHeartbeat(ctx, "edge-fresh"); err != nil {
		t.Fatal(err)
	}
	mem.SetEdgeListenerHeartbeatForTest("edge-stale", time.Now().Add(-state.EdgeListenerStaleAfter-time.Second))
	svc := &Service{Host: attackModeHost{store: mem, notif: &recordingNotifier{}}}
	nodes, err := svc.ServingEdgeRuleNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"edge-fresh", "edge-legacy"}; !slices.Equal(nodes, want) {
		t.Fatalf("ServingEdgeRuleNodes = %v, want %v", nodes, want)
	}
}

// adr: 091 — amendment 2026-10-27: mutations of one account run one at a
// time, while another account's mutation does not wait for them.
func TestLockEdgeRuleMutationsSerializesPerAccount(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acctA, _ := mem.CreateAccount(ctx, "lock-a@example.test", api.PlanPro)
	acctB, _ := mem.CreateAccount(ctx, "lock-b@example.test", api.PlanPro)
	mk := func(acct, slug string) string {
		app, err := mem.CreateApp(ctx, state.App{AccountID: acct, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app.ID
	}
	shop, blog, other := mk(acctA.ID, "lock-shop"), mk(acctA.ID, "lock-blog"), mk(acctB.ID, "lock-other")
	svc := &Service{Host: attackModeHost{store: mem, notif: &recordingNotifier{}}}

	unlockShop, err := svc.LockEdgeRuleMutations(ctx, []string{shop})
	if err != nil {
		t.Fatal(err)
	}
	unlockOther, err := svc.LockEdgeRuleMutations(ctx, []string{other})
	if err != nil {
		t.Fatal("another account waited for account A's mutation:", err)
	}
	unlockOther(ctx)

	var blogLocked atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		unlock, err := svc.LockEdgeRuleMutations(ctx, []string{blog})
		if err != nil {
			t.Error(err)
			return
		}
		blogLocked.Store(true)
		unlock(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	if blogLocked.Load() {
		t.Fatal("a second app of the same account did not wait for the first mutation")
	}
	unlockShop(ctx)
	unlockShop(ctx) // releasing twice is a no-op
	<-done
	if !blogLocked.Load() {
		t.Fatal("the waiting mutation never ran")
	}
	if len(edgeRuleAccountLocks.locks) != 0 {
		t.Fatalf("account lock entries leaked: %d", len(edgeRuleAccountLocks.locks))
	}
}
