// spec: §4.3 — the reaper parks idle instances from a per-tick snapshot of live rows.

package sched_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// reaperReadCounter counts the per-app reads the reaper used to make on every
// tick: full instance histories, per-row instance lookups and owner account
// lookups.
type reaperReadCounter struct {
	MemStore     *state.MemStore
	historyReads map[string]int
	rowReads     map[string]int
	accountReads map[string]int
}

func newReaperReadCounter(mem *state.MemStore) *reaperReadCounter {
	return &reaperReadCounter{MemStore: mem, historyReads: map[string]int{}, rowReads: map[string]int{}, accountReads: map[string]int{}}
}

func (s *reaperReadCounter) reset() {
	clear(s.historyReads)
	clear(s.accountReads)
	clear(s.rowReads)
}

func (s *reaperReadCounter) ListInstancesForApp(ctx context.Context, appID string) ([]state.Instance, error) {
	s.historyReads[appID]++
	return s.MemStore.ListInstancesForApp(ctx, appID)
}

func (s *reaperReadCounter) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	s.rowReads[id]++
	return s.MemStore.InstanceByID(ctx, id)
}

func (s *reaperReadCounter) AccountByID(ctx context.Context, id string) (state.Account, error) {
	s.accountReads[id]++
	return s.MemStore.AccountByID(ctx, id)
}

// seedReaperApp creates a live app with one deployment.
func seedReaperApp(t *testing.T, mem *state.MemStore, accountID, slug string) state.App {
	t.Helper()
	ctx := context.Background()
	app, err := mem.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: slug, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := mem.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return app
}

func admitReaperInstance(t *testing.T, engine *sched.Engine, appID string) string {
	t.Helper()
	res, err := engine.AdmitInstance(context.Background(), appID, "", "", "")
	if err != nil {
		t.Fatalf("AdmitInstance: %v", err)
	}
	return res.InstanceID
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
	counter := newReaperReadCounter(mem)
	engine := sched.XNewEngine(t, statetest.NewMem(counter), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	var apps []state.App
	for _, slug := range []string{"reads-a", "reads-b"} {
		app := seedReaperApp(t, mem, acct.ID, slug)
		admitReaperInstance(t, engine, app.ID)
		admitReaperInstance(t, engine, app.ID)
		apps = append(apps, app)
	}
	counter.reset()

	// Within the idle timeout: the tick parks nothing, so every read below is
	// the reaper's own.
	sched.NewLoop(nil, engine, sched.TestLog()).
		WithClock(func() time.Time { return time.Now().Add(5 * time.Second) }).
		RunReaper(ctx)

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

// TestParkAppReadsOnlyLiveInstances pins the second control-plane load fix:
// the reaper calls ParkApp for every parked app on every tick, and ParkApp
// used to re-read each row of the app's history (about 1,000 lookups a second
// on production-us). It now reads live rows only and still parks them.
func TestParkAppReadsOnlyLiveInstances(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "park@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	counter := newReaperReadCounter(mem)
	engine := sched.XNewEngine(t, statetest.NewMem(counter), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	app := seedReaperApp(t, mem, acct.ID, "parked-history")
	// Five wakes that already parked: history the old loop re-read.
	var history []string
	for range 5 {
		id := admitReaperInstance(t, engine, app.ID)
		if err := engine.Park(ctx, id); err != nil {
			t.Fatalf("Park: %v", err)
		}
		history = append(history, id)
	}
	live := admitReaperInstance(t, engine, app.ID)
	evicted := state.AppEvictedCold
	if _, err := mem.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &evicted}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	counter.reset()

	acted, err := engine.ParkApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ParkApp: %v", err)
	}
	if acted != 1 {
		t.Fatalf("ParkApp acted on %d instances, want the 1 live one", acted)
	}
	if n := counter.historyReads[app.ID]; n != 0 {
		t.Errorf("%d instance history reads, want 0", n)
	}
	for _, id := range history {
		if n := counter.rowReads[id]; n != 0 {
			t.Errorf("parked history row %s read %d times, want 0", id, n)
		}
	}
	got, err := mem.InstanceByID(ctx, live)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.IsLive(got.State) {
		t.Fatalf("live instance state = %s after ParkApp, want it parked", got.State)
	}
}

// TestReconcileWorkerAppReadsOnlyLiveInstances pins the worker reconcile's
// read: the targets trigger reconciles every project worker app each second,
// so it must not list the app's history. Parked and stopped rows stay unread
// and the newest live worker still wins.
func TestReconcileWorkerAppReadsOnlyLiveInstances(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, mem, api.PlanPro, 256, 4)
	sched.ConfigureExecutionMode(t, mem, app, dep, api.ExecutionModeWorker, state.DeployLive)
	for i, st := range []state.State{state.StateParked, state.StateStopped, state.StateFailed} {
		if _, err := mem.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(st), 256, state.DefaultLocalNodeName, fmt.Sprintf("wake-history-%d", i), string(state.InstanceModeWorker)); err != nil {
			t.Fatalf("CreateInstanceWithMode(%s): %v", st, err)
		}
	}
	live, err := mem.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "wake-live", string(state.InstanceModeWorker))
	if err != nil {
		t.Fatalf("CreateInstanceWithMode(running): %v", err)
	}
	counter := newReaperReadCounter(mem)
	engine := sched.XNewEngine(t, statetest.NewMem(counter), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	counter.reset()

	if err := engine.ReconcileWorkerPools(ctx, app.ID, "test"); err != nil {
		t.Fatalf("ReconcileWorkerPools: %v", err)
	}
	if n := counter.historyReads[app.ID]; n != 0 {
		t.Fatalf("%d instance history reads, want 0", n)
	}
	got, err := mem.InstanceByID(ctx, live.ID)
	if err != nil || got.State != string(state.StateRunning) {
		t.Fatalf("live worker = %+v (%v), want it still running", got, err)
	}
}
