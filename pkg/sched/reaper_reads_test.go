// spec: §4.3 — the reaper parks idle instances from a per-tick snapshot of live rows.

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// reaperReadCounter counts the per-app reads the reaper used to make on every
// tick: full instance histories and owner account lookups.
type reaperReadCounter struct {
	MemStore     *state.MemStore
	historyReads map[string]int
	accountReads map[string]int
}

func (s *reaperReadCounter) ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error) {
	s.historyReads[appID]++
	return s.MemStore.ListInstancesForApp(ctx, appID)
}

func (s *reaperReadCounter) AccountByID(ctx context.Context, id string) (state.Account, error) {
	s.accountReads[id]++
	return s.MemStore.AccountByID(ctx, id)
}

// TestRunReaperReadsLiveInstancesOnceAndEachOwnerOnce pins the control-plane
// load fix: one reaper tick over two busy apps of one account reads no
// instance history and the account once, and still sees the live instances.
func TestRunReaperReadsLiveInstancesOnceAndEachOwnerOnce(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "reads@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	counter := &reaperReadCounter{MemStore: mem, historyReads: map[string]int{}, accountReads: map[string]int{}}
	engine := newEngine(t, statetest.NewMem(counter), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	var apps []state.App
	for _, slug := range []string{"reads-a", "reads-b"} {
		app, err := mem.CreateApp(ctx, state.App{
			AccountID: acct.ID, Slug: slug, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
		})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		if _, err := mem.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive,
		}); err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		wakeN(t, engine, app.ID, 2)
		apps = append(apps, app)
	}
	clear(counter.historyReads)
	clear(counter.accountReads)

	// Within the idle timeout: the tick parks nothing, so every read below is
	// the reaper's own.
	loop := NewLoop(nil, engine, testLog()).WithClock(func() time.Time { return time.Now().Add(5 * time.Second) })
	loop.runReaper(ctx)

	if loop.lastFloorByApp == nil {
		t.Fatalf("reaper did not finish the tick")
	}
	for _, app := range apps {
		if n := counter.historyReads[app.ID]; n != 0 {
			t.Errorf("app %s: %d instance history reads, want 0", app.Slug, n)
		}
	}
	if n := counter.accountReads[acct.ID]; n != 1 {
		t.Errorf("account read %d times, want 1 per tick", n)
	}
	for _, app := range apps {
		live, err := mem.ListLiveInstancesForApps(ctx, []string{app.ID})
		if err != nil || len(live) != 2 {
			t.Fatalf("app %s live instances = %d (err %v), want 2 left running", app.Slug, len(live), err)
		}
	}
}
