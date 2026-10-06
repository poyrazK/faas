// spec: §4.3 — aggressive scale-in never parks capacity that in-flight requests still need.

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched/recentload"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeLoadScaleUpScraper is a gateway scraper that also reports per-app
// in-flight requests, like scaleup.HTTPPromScraper.
type fakeLoadScaleUpScraper struct {
	fakeScaleUpScraper
	inflight map[string]int64
}

func (f *fakeLoadScaleUpScraper) ScrapeLoad(ctx context.Context) (map[string]int64, map[string]int64, error) {
	counts, err := f.Scrape(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	inflight := make(map[string]int64, len(f.inflight))
	for k, v := range f.inflight {
		inflight[k] = v
	}
	return counts, inflight, err
}

type fakeInstanceActivity map[string]InstanceActivity

func (f fakeInstanceActivity) SnapshotActivity(time.Time) map[string]InstanceActivity {
	return f
}

// stalledSurgeMirror returns a mirror whose rate signal reads a measured
// zero (completions stopped) while the gateway reports gatewayInflight
// requests in flight for appID.
func stalledSurgeMirror(appID string, gatewayInflight int64, frozen time.Time) *recentload.RecentLoad {
	scraper := &fakeLoadScaleUpScraper{inflight: map[string]int64{appID: gatewayInflight}}
	mirror := recentload.New(scraper, 5, 5*time.Minute)
	scraper.set(map[string]int64{appID: 100})
	mirror.Touch(context.Background(), frozen)
	scraper.set(map[string]int64{appID: 0})
	mirror.Touch(context.Background(), frozen.Add(time.Minute))
	return mirror
}

func seedAutoscaledProApp(t *testing.T, store *state.MemStore) state.App {
	t.Helper()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	return app
}

// TestLoopReaperAggressiveHoldsForGatewayInflight reproduces the
// production-us surge: completions stalled, the rate read zero, and the
// reaper parked instances while 200 clients waited at the gateway. With 100
// requests in flight at the Pro bound of 25 per instance, all 5 are needed.
func TestLoopReaperAggressiveHoldsForGatewayInflight(t *testing.T) {
	store := state.NewMemStore()
	app := seedAutoscaledProApp(t, store)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wakeN(t, engine, app.ID, 5)

	frozen := time.Now().Add(35 * time.Second)
	loop := NewLoop(nil, engine, testLog()).
		WithClock(func() time.Time { return frozen }).
		WithRecentLoad(stalledSurgeMirror(app.ID, 100, frozen)).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	if running := liveCount(t, store, app.ID); running != 5 {
		t.Errorf("running = %d, want 5 (100 in flight need ceil(100/25)=4, plus the +1 buffer)", running)
	}
	if vmm.snapshots != 0 {
		t.Errorf("snapshots = %d, want 0", vmm.snapshots)
	}
}

// TestLoopReaperAggressiveCountsVMMDInflight covers requests already forwarded
// to instances, which every gateway's VMMD stats report fleet-wide. Two
// instances each hold a full Pro slot set (25), so demand is 2 instances and
// the +1 buffer keeps a third. Busy instances alone (the per-instance guard)
// would leave only 2.
func TestLoopReaperAggressiveCountsVMMDInflight(t *testing.T) {
	store := state.NewMemStore()
	app := seedAutoscaledProApp(t, store)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wakeN(t, engine, app.ID, 5)

	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	activity := fakeInstanceActivity{}
	for _, ins := range instances {
		if ins.State == string(state.StateRunning) && len(activity) < 2 {
			activity[ins.ID] = InstanceActivity{Inflight: 25}
		}
	}
	if len(activity) != 2 {
		t.Fatalf("seeded %d busy instances, want 2", len(activity))
	}

	frozen := time.Now().Add(35 * time.Second)
	loop := NewLoop(nil, engine, testLog()).
		WithClock(func() time.Time { return frozen }).
		WithRecentLoad(stalledSurgeMirror(app.ID, 0, frozen)).
		WithInstanceActivity(activity).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	if running := liveCount(t, store, app.ID); running != 3 {
		t.Errorf("running = %d, want 3 (50 in flight need 2, plus the +1 buffer)", running)
	}
	for id := range activity {
		ins, err := store.InstanceByID(context.Background(), id)
		if err != nil {
			t.Fatalf("InstanceByID(%s): %v", id, err)
		}
		if ins.State != string(state.StateRunning) {
			t.Errorf("busy instance %s state = %s, want running", id, ins.State)
		}
	}
}

// TestLoopReaperAggressiveIdleGatewayStillScalesDown pins that a fresh zero
// in-flight reading does not hold capacity: the drop still parks down to the
// +1 buffer.
func TestLoopReaperAggressiveIdleGatewayStillScalesDown(t *testing.T) {
	store := state.NewMemStore()
	app := seedAutoscaledProApp(t, store)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wakeN(t, engine, app.ID, 5)

	frozen := time.Now().Add(35 * time.Second)
	loop := NewLoop(nil, engine, testLog()).
		WithClock(func() time.Time { return frozen }).
		WithRecentLoad(stalledSurgeMirror(app.ID, 0, frozen)).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	if running := liveCount(t, store, app.ID); running != 1 {
		t.Errorf("running = %d, want 1 (no in-flight demand, one warm above the buffer)", running)
	}
}
