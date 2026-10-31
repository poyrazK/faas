// adr: 20261024085817 — a graceful drain finishes: it parks what it could not
// hand off, and a draining node takes no resident work back into service.
package sched_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// drainClock is a drain that began at start, read at now.
type drainClock struct{ start, now time.Time }

func (c drainClock) node(lifecycle state.NodeLifecycle) state.ComputeNode {
	return state.ComputeNode{ID: "n1", Lifecycle: lifecycle, DrainInitiatedAt: &c.start}
}

func TestDrainDecisionsFollowTheDrainTimeline(t *testing.T) {
	t.Parallel()
	const window, parkAfter = 2 * time.Minute, 5 * time.Minute
	start := time.Date(2026, 10, 22, 15, 20, 11, 0, time.UTC)
	cases := []struct {
		name      string
		lifecycle state.NodeLifecycle
		elapsed   time.Duration
		noStart   bool
		instance  state.RecoveryInstance
		want      sched.Decision
	}{
		{name: "running at drain start migrates", elapsed: 0, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionLiveMigrate},
		{name: "running inside the window migrates", elapsed: window - time.Second, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionLiveMigrate},
		{name: "running once the window closes waits for handoffs to settle", elapsed: window, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionNone},
		{name: "running just before the park point still waits", elapsed: parkAfter - time.Second, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionNone},
		{name: "running at the park point parks", elapsed: parkAfter, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionPark},
		{name: "running long after the park point parks", elapsed: time.Hour, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionPark},
		{name: "running without drain_initiated_at keeps migrating", noStart: true, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionLiveMigrate},
		{name: "warm parks at once", elapsed: 0, instance: state.RecoveryInstance{State: "warm"}, want: sched.DecisionPark},
		{name: "a withdrawn row before the park point is left to its owner", elapsed: window, instance: state.RecoveryInstance{State: "draining"}, want: sched.DecisionNone},
		{name: "a withdrawn row at the park point is finished", elapsed: parkAfter, instance: state.RecoveryInstance{State: "draining"}, want: sched.DecisionPark},
		{name: "frozen is recreated (parked)", elapsed: 0, instance: state.RecoveryInstance{State: "frozen"}, want: sched.DecisionRecreate},
		{name: "waking stays with its healthy node", elapsed: parkAfter, instance: state.RecoveryInstance{State: "waking"}, want: sched.DecisionNone},
		{name: "snapshotting is in flight", elapsed: parkAfter, instance: state.RecoveryInstance{State: "snapshotting"}, want: sched.DecisionNone},
		{name: "migrating is in flight", elapsed: parkAfter, instance: state.RecoveryInstance{State: "migrating"}, want: sched.DecisionNone},
		{name: "job task keeps its lease reaper", elapsed: parkAfter, instance: state.RecoveryInstance{State: "running", Kind: "job_task"}, want: sched.DecisionNone},
		{name: "force drain keeps migrating", lifecycle: state.NodeLifecycleForceDraining, elapsed: parkAfter, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionLiveMigrate},
		{name: "unavailable keeps migrating", lifecycle: state.NodeLifecycleUnavailable, elapsed: parkAfter, instance: state.RecoveryInstance{State: "running"}, want: sched.DecisionLiveMigrate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := drainClock{start: start, now: start.Add(tc.elapsed)}
			a := sched.NewArbiter(nil, nil).WithDrainTiming(window, parkAfter, func() time.Time { return clock.now })
			lifecycle := tc.lifecycle
			if lifecycle == "" {
				lifecycle = state.NodeLifecycleDraining
			}
			node := clock.node(lifecycle)
			if tc.noStart {
				node.DrainInitiatedAt = nil
			}
			if got := a.Decide(node, tc.instance); got != tc.want {
				t.Fatalf("Decide = %s, want %s", got, tc.want)
			}
		})
	}
}

// The default timeline starts no park while a handoff begun inside the
// migration window can still be inside its lease.
func TestDrainParkPointClearsTheLastMigrationLease(t *testing.T) {
	t.Parallel()
	if api.DrainParkAfterSeconds < api.DrainLiveMigrationWindowSeconds+api.MigrateLiveLeaseSeconds {
		t.Fatalf("DrainParkAfterSeconds = %d, want at least the window %d plus one lease %d",
			api.DrainParkAfterSeconds, api.DrainLiveMigrationWindowSeconds, api.MigrateLiveLeaseSeconds)
	}
	start := time.Now().Add(-time.Duration(api.DrainParkAfterSeconds) * time.Second)
	node := state.ComputeNode{Lifecycle: state.NodeLifecycleDraining, DrainInitiatedAt: &start}
	if got := sched.NewArbiter(nil, nil).Decide(node, state.RecoveryInstance{State: "running"}); got != sched.DecisionPark {
		t.Fatalf("default arbiter at DrainParkAfterSeconds: Decide = %s, want park", got)
	}
}

// drainDispatcher records every dispatch the arbiter makes.
type drainDispatcher struct {
	mu        sync.Mutex
	migrated  []string
	recreated []string
	parked    []string
	parkErr   error
}

func (d *drainDispatcher) Enqueue(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.migrated = append(d.migrated, id)
	return nil
}

func (d *drainDispatcher) RecreateInstance(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recreated = append(d.recreated, id)
	return nil
}

func (d *drainDispatcher) ParkForDrain(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.parkErr != nil {
		return d.parkErr
	}
	d.parked = append(d.parked, id)
	return nil
}

func TestDrainTickParksWhatItCouldNotHandOff(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 22, 15, 20, 11, 0, time.UTC)
	instances := []state.RecoveryInstance{
		{ID: "busy-1", State: "running"}, {ID: "busy-2", State: "running"},
		{ID: "pool", State: "warm"}, {ID: "idle", State: "frozen"}, {ID: "booting", State: "cold_booting"},
		{ID: "withdrawn", State: "draining"},
	}
	cases := []struct {
		name                            string
		elapsed                         time.Duration
		withPark                        bool
		parkErr                         error
		wantMigrated, wantParked        []string
		wantLive, wantRecreate, wantSkp int
		wantErr                         bool
	}{
		{name: "inside the window running instances migrate and the pool parks", elapsed: time.Minute, withPark: true,
			wantMigrated: []string{"busy-1", "busy-2"}, wantParked: []string{"pool"}, wantLive: 2, wantRecreate: 2, wantSkp: 2},
		{name: "past the park point everything resident parks", elapsed: 10 * time.Minute, withPark: true,
			wantParked: []string{"busy-1", "busy-2", "pool", "withdrawn"}, wantRecreate: 5, wantSkp: 1},
		{name: "a failed park is retried next tick, not counted", elapsed: 10 * time.Minute, withPark: true, parkErr: errors.New("vmmd unreachable"),
			wantRecreate: 1, wantSkp: 1, wantErr: true},
		{name: "without a park dispatcher park verdicts are skipped", elapsed: 10 * time.Minute,
			wantRecreate: 1, wantSkp: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clock := drainClock{start: start, now: start.Add(tc.elapsed)}
			d := &drainDispatcher{parkErr: tc.parkErr}
			a := sched.NewArbiter(d, d).WithDrainTiming(2*time.Minute, 5*time.Minute, func() time.Time { return clock.now })
			if tc.withPark {
				a.WithParkDispatcher(d)
			}
			node := clock.node(state.NodeLifecycleDraining)
			live, recreate, skipped, err := a.Tick(t.Context(), []state.ComputeNode{node}, map[string][]state.RecoveryInstance{node.ID: instances})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Tick err = %v, want error %v", err, tc.wantErr)
			}
			if live != tc.wantLive || recreate != tc.wantRecreate || skipped != tc.wantSkp {
				t.Fatalf("Tick = (live %d, recreate %d, skipped %d), want (%d, %d, %d)", live, recreate, skipped, tc.wantLive, tc.wantRecreate, tc.wantSkp)
			}
			if !sameIDs(d.migrated, tc.wantMigrated) || !sameIDs(d.parked, tc.wantParked) || !sameIDs(d.recreated, []string{"idle"}) {
				t.Fatalf("migrated %v parked %v recreated %v; want migrated %v parked %v recreated [idle]", d.migrated, d.parked, d.recreated, tc.wantMigrated, tc.wantParked)
			}
		})
	}
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		if seen[id] == 0 {
			return false
		}
		seen[id]--
	}
	return true
}

func TestRecoveryRunnerHandsItsNodeListToTheObserver(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NodeSetLifecycle(t.Context(), node.ID, node.Lifecycle, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	var seen []state.ComputeNode
	runner := sched.NewRecoveryRunner(store, sched.NewArbiter(nil, nil), nil, nil).
		WithNodeObserver(func(nodes []state.ComputeNode) { seen = nodes })
	if err := runner.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(seen) != 1 || seen[0].ID != node.ID || seen[0].Lifecycle != state.NodeLifecycleDraining {
		t.Fatalf("observer saw %+v, want the draining node", seen)
	}
}

// drainFixtureNode moves the fixture's node to draining and lets the
// engine observe it, as the recovery runner does every tick.
func drainFixtureNode(t *testing.T, store *state.MemStore, e *sched.Engine, nodeID string) {
	t.Helper()
	node, err := store.ComputeNodeByID(t.Context(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.NodeSetLifecycle(t.Context(), node.ID, node.Lifecycle, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.NodeList(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	e.ObserveNodeLifecycles(nodes)
}

func frozenParks(ops *wire.OpsMetrics, reason string) float64 {
	return testutil.ToFloat64(ops.FrozenParksTotal(reason))
}

// A frozen VM on a draining node is parked instead of thawed, on the
// ordinary path (fresh node read) and on the ticketed fast path (the
// engine's observed view), so a request cannot hold the node in service.
func TestDrainingNodeParksFrozenInsteadOfThawing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fastPath bool
		observe  bool
		wantSkip float64
	}{
		{name: "ordinary path reads the draining node", observe: false},
		{name: "fast path consults the observed view", fastPath: true, observe: true, wantSkip: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var (
				f   *sched.FrozenFixture
				ops *wire.OpsMetrics
			)
			if tc.fastPath {
				f, ops = frozenWithTicket(t)
			} else {
				f = sched.NewFrozenFixture(t, true)
				ops = wire.NewOpsMetrics("schedd")
				(*f.E()).WithOpsMetrics(ops)
				(*f.E()).EnableFrozenTier()
				if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
					t.Fatalf("FreezeIdle: %v, %v", handled, err)
				}
			}
			store, e := *f.Store(), *f.E()
			node, err := store.ComputeNodeByID(t.Context(), (*f.Ins()).NodeID)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.NodeSetLifecycle(t.Context(), node.ID, node.Lifecycle, state.NodeLifecycleDraining); err != nil {
				t.Fatal(err)
			}
			if tc.observe {
				nodes, err := store.NodeList(t.Context(), "")
				if err != nil {
					t.Fatal(err)
				}
				e.ObserveNodeLifecycles(nodes)
			}
			_, _ = e.Wake(t.Context(), (*f.App()).ID, "", "", "")
			if thaws := *(*f.Vmm()).Thaws(); thaws != 0 {
				t.Fatalf("thaws = %d, want none on a draining node", thaws)
			}
			if row, _ := store.InstanceByID(t.Context(), (*f.Ins()).ID); row.State != string(state.StateParked) {
				t.Fatalf("frozen VM on a draining node: state %q, want parked", row.State)
			}
			if got := frozenParks(ops, "node_draining"); got != 1 {
				t.Fatalf("frozen parks{node_draining} = %v, want 1", got)
			}
			if got := fastPathSkips(ops)["node_draining"]; got != tc.wantSkip {
				t.Fatalf("fast path skips{node_draining} = %v, want %v", got, tc.wantSkip)
			}
		})
	}
}

// An idle instance on a draining node is parked by the reaper, not frozen
// in place where it would stay resident.
func TestDrainingNodeRefusesToFreeze(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	ops := wire.NewOpsMetrics("schedd")
	(*f.E()).WithOpsMetrics(ops)
	(*f.E()).EnableFrozenTier()
	drainFixtureNode(t, *f.Store(), *f.E(), (*f.Ins()).NodeID)
	handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150)
	if err != nil || handled {
		t.Fatalf("FreezeIdle on a draining node = %v, %v; want not handled so the reaper parks", handled, err)
	}
	if got := testutil.ToFloat64(ops.FrozenFreezeTotal("node_draining")); got != 1 {
		t.Fatalf("freeze outcome{node_draining} = %v, want 1", got)
	}
	if row, _ := (*f.Store()).InstanceByID(t.Context(), (*f.Ins()).ID); row.State != string(state.StateRunning) {
		t.Fatalf("state %q, want running for the reaper", row.State)
	}
}

// A warm-pool VM on a draining node is discarded, not resumed into service.
func TestDrainingNodeDiscardsWarmPoolVMs(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	target := 1
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{WarmPoolSize: &target, SetWarmPoolSize: true}); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	warm = sched.PublishWarmFixture(t, store, warm)
	vmm := sched.NewWarmResumeFakeVMM()
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	limits := api.MustLimitsFor(api.PlanPro)
	if err := e.Ledger().Admit(sched.Request{
		Instance: warm.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		NodeID: node.ID, Kind: sched.KindWarmPool,
	}); err != nil {
		t.Fatal(err)
	}
	drainFixtureNode(t, store, e, node.ID)
	_, _ = e.Wake(ctx, app.ID, "", "", "")
	if calls := *vmm.ResumeCalls(); calls != 0 {
		t.Fatalf("resume calls = %d, want none on a draining node", calls)
	}
	if row, _ := store.InstanceByID(ctx, warm.ID); row.State != string(state.StateParked) {
		t.Fatalf("warm row on a draining node: state %q, want parked", row.State)
	}
}

// A drain park never cuts requests in flight: the instance leaves routing
// (DRAINING) before the request barrier, and is destroyed only once vmmd
// reports it idle or the bound passes. On 2026-10-22 the instance holding
// fsn-3's drain was busy, which is exactly the instance a drain park meets.
func TestDrainParkWithdrawsAndDrainsRequestsBeforeRetiring(t *testing.T) {
	cases := []struct {
		name        string
		start       state.State
		busySamples int // Stats calls that report a request in flight; -1 = always
		bound       time.Duration
		minElapsed  time.Duration
	}{
		{name: "requests finish inside the bound", start: state.StateRunning, busySamples: 3, bound: time.Minute,
			minElapsed: time.Duration(api.ServiceReplicaDrainQuietSeconds) * time.Second},
		{name: "requests outlast the bound", start: state.StateRunning, busySamples: -1, bound: 400 * time.Millisecond,
			minElapsed: 400 * time.Millisecond},
		{name: "a withdrawn row is resumed", start: state.StateDraining, busySamples: 1, bound: time.Minute,
			minElapsed: time.Duration(api.ServiceReplicaDrainQuietSeconds) * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(tc.start), app.RAMMB, node.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			vmm := &sched.FakeVMM{}
			var (
				mu                sync.Mutex
				calls             int
				stateAtFirstStats string
				destroysWhileBusy int
			)
			(*vmm.StatsHook()) = func(ctx context.Context, _ string) (*sched.StatsSnapshot, error) {
				row, _ := store.InstanceByID(ctx, ins.ID)
				vmm.Mu().Lock()
				destroys := *vmm.Destroys()
				vmm.Mu().Unlock()
				mu.Lock()
				defer mu.Unlock()
				calls++
				if calls == 1 {
					stateAtFirstStats = row.State
				}
				inflight := int64(0)
				if tc.busySamples < 0 || calls <= tc.busySamples {
					inflight = 1
					destroysWhileBusy += destroys
				}
				return &sched.StatsSnapshot{Instances: []sched.VMInstanceStat{{InstanceID: ins.ID, InflightRequests: inflight}}}, nil
			}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
			*e.DrainRequestBound() = tc.bound
			started := time.Now()
			if err := e.ParkForDrain(ctx, ins.ID); err != nil {
				t.Fatalf("ParkForDrain: %v", err)
			}
			elapsed := time.Since(started)
			mu.Lock()
			defer mu.Unlock()
			if stateAtFirstStats != string(state.StateDraining) {
				t.Fatalf("instance state while its requests drained = %q, want draining (withdrawn from routing)", stateAtFirstStats)
			}
			if destroysWhileBusy != 0 {
				t.Fatalf("the VM was destroyed while vmmd still reported a request in flight")
			}
			if elapsed < tc.minElapsed {
				t.Fatalf("retired after %s, want the request barrier to hold at least %s", elapsed, tc.minElapsed)
			}
			if row, _ := store.InstanceByID(ctx, ins.ID); row.State != string(state.StateStopped) || *vmm.Destroys() != 1 {
				t.Fatalf("state %q destroys %d, want stopped after one destroy", row.State, *vmm.Destroys())
			}
		})
	}
}

// WARM rows serve no requests and take the ordinary park.
func TestDrainParkParksAWarmInstance(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 256, 5)
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	warm, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	warm = sched.PublishWarmFixture(t, store, warm)
	e := sched.XNewEngine(t, store, sched.NewWarmResumeFakeVMM(), &sched.FakeNotifier{}, "1.10.0")
	if err := e.ParkForDrain(ctx, warm.ID); err != nil {
		t.Fatalf("ParkForDrain: %v", err)
	}
	if row, _ := store.InstanceByID(ctx, warm.ID); row.State != string(state.StateParked) {
		t.Fatalf("warm row: state %q, want parked", row.State)
	}
	// A second dispatch (another schedd's arbiter) finds nothing to do.
	if err := e.ParkForDrain(ctx, warm.ID); err != nil {
		t.Fatalf("second ParkForDrain: %v", err)
	}
}
