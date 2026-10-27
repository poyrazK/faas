// adr: 20261027160930 — one slow app cannot hold the drain: rows are served
// round-robin from a shared pool in which an app holds at most its share while
// others wait, and a tick returns while slow dispatches finish behind it.
package sched_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// fairDrainGateway holds the slow app's invocations until released and
// records every app's calls and peak concurrency.
type fairDrainGateway struct {
	slowApp string
	release chan struct{}
	mu      sync.Mutex
	calls   map[string]int
	active  map[string]int
	peak    map[string]int
}

func (g *fairDrainGateway) SynthesizeRequest(context.Context, string, string, string) error {
	return nil
}

func (g *fairDrainGateway) Invoke(ctx context.Context, _ string, inv state.Invocation) (state.Invocation, error) {
	g.mu.Lock()
	g.calls[inv.AppID]++
	g.active[inv.AppID]++
	g.peak[inv.AppID] = max(g.peak[inv.AppID], g.active[inv.AppID])
	g.mu.Unlock()
	if inv.AppID == g.slowApp {
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	}
	g.mu.Lock()
	g.active[inv.AppID]--
	g.mu.Unlock()
	inv.State = state.InvocationDispatching
	inv.InstanceID = "inst-" + inv.ID
	inv.Result = json.RawMessage(`{"ok":true}`)
	return inv, nil
}

func (g *fairDrainGateway) snapshot(appID string) (calls, peak int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[appID], g.peak[appID]
}

func fairDrainApp(t *testing.T, store *state.MemStore, email, slug string) state.App {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: 256, MaxConcurrency: 20, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	return app
}

func enqueueFairDrainRows(t *testing.T, store *state.MemStore, app state.App, n int, due time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := store.EnqueueInvocation(context.Background(), state.Invocation{AppID: app.ID, AccountID: app.AccountID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/x", DueAt: due.Add(time.Duration(i) * time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDrainServesOtherAppsWhileOneAppIsSlow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	slow := fairDrainApp(t, store, "slow@example.test", "slow-app")
	quick := fairDrainApp(t, store, "quick@example.test", "quick-app")
	// The slow app's burst leads the due order, ahead of the quick app's rows.
	enqueueFairDrainRows(t, store, slow, 20, time.Now().Add(-time.Minute))
	enqueueFairDrainRows(t, store, quick, 3, time.Now().Add(-time.Second))

	gateway := &fairDrainGateway{slowApp: slow.ID, release: make(chan struct{}),
		calls: map[string]int{}, active: map[string]int{}, peak: map[string]int{}}
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	drain := sched.NewDrain(store, engine, sched.WithDrainGatewaySynth(gateway), sched.WithDrainDispatchConcurrency(8))
	released := false
	t.Cleanup(func() {
		if !released {
			close(gateway.release)
		}
		drain.XWaitDispatches()
	})

	start := time.Now()
	drain.Tick(ctx)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("tick took %s while the slow app's dispatches were held", took)
	}
	if calls, _ := gateway.snapshot(quick.ID); calls != 3 {
		t.Fatalf("quick app calls = %d, want all 3 while the slow app is held", calls)
	}
	// Past its share the slow app may use only slots beyond the others'
	// share: at most 8 - 2 of the 8.
	if calls, peak := gateway.snapshot(slow.ID); calls == 0 || peak > 6 {
		t.Fatalf("slow app calls = %d, peak = %d; want some, at most 6 at once", calls, peak)
	}

	close(gateway.release)
	released = true
	drain.XWaitDispatches()
	for i := 0; i < 10; i++ {
		if calls, _ := gateway.snapshot(slow.ID); calls >= 20 {
			break
		}
		drain.Tick(ctx)
		drain.XWaitDispatches()
	}
	if calls, _ := gateway.snapshot(slow.ID); calls != 20 {
		t.Fatalf("slow app calls after release = %d, want every row delivered", calls)
	}
}
