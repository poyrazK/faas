// adr: 1741 — the owner cache serves a production wake's reads, and a stale
// entry is refused at admission, never used.
package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

type ownerCacheFixture struct {
	mem     *state.MemStore
	counter *sched.ReadCountingStore
	e       *sched.Engine
	ops     *wire.OpsMetrics
	app     state.App
}

func newOwnerCacheFixture(t *testing.T) ownerCacheFixture {
	t.Helper()
	mem := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, mem, api.PlanPro, 512, 5)
	counter := &sched.ReadCountingStore{MemStore: mem}
	ops := wire.NewOpsMetrics("schedd")
	e := sched.XNewEngine(t, statetest.NewMem(counter), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops).WithOwnerCache()
	return ownerCacheFixture{mem: mem, counter: counter, e: e, ops: ops, app: app}
}

// wakeAndPark wakes the app and parks the instance, so the next wake admits
// a new one.
func (f ownerCacheFixture) wakeAndPark(t *testing.T) sched.WakeResult {
	t.Helper()
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := f.e.Park(t.Context(), result.InstanceID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	return result
}

func (f ownerCacheFixture) outcomes() map[string]float64 {
	out := map[string]float64{}
	for _, outcome := range wire.OwnerCacheOutcomes {
		out[outcome] = testutil.ToFloat64(f.ops.OwnerCacheTotal(outcome))
	}
	return out
}

func TestOwnerCacheServesTheNextWake(t *testing.T) {
	f := newOwnerCacheFixture(t)
	f.wakeAndPark(t)
	first := f.counter.Reads()["AppByID"]
	f.wakeAndPark(t)
	// The first wake loaded the entry (one app read) and resolved through
	// it; the second reads the app only in the boot's own bookkeeping.
	if second := f.counter.Reads()["AppByID"] - first; second >= first {
		t.Fatalf("app reads: first wake %d, second %d; want fewer once the entry is cached", first, second)
	}
	if got := f.outcomes(); got["miss"] != 1 || got["hit"] < 2 || got["refused"] != 0 {
		t.Fatalf("owner cache outcomes = %v, want one miss and hits after it", got)
	}
}

func TestOwnerCacheRefusesAStaleEntryAtAdmission(t *testing.T) {
	f := newOwnerCacheFixture(t)
	f.wakeAndPark(t)
	// The change moves the app's generation; its notification never arrives,
	// so the cached entry still says 512 MB.
	ram := 1024
	if _, err := f.mem.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake after a missed notification: %v", err)
	}
	ins, err := f.mem.InstanceByID(t.Context(), result.InstanceID)
	if err != nil || ins.RAMMB != ram {
		t.Fatalf("admitted instance RAM = %d, %v; want the current %d", ins.RAMMB, err, ram)
	}
	if got := f.outcomes(); got["refused"] != 1 {
		t.Fatalf("owner cache outcomes = %v, want the stale entry refused once", got)
	}
}

// retiringStore reports every workload as retired once retired is set. The
// flag stands in for a retirement row; a test moves the app's generation
// itself, as the row's trigger would.
type retiringStore struct {
	MemStore *state.MemStore
	retired  atomic.Bool
}

func (s *retiringStore) EnvironmentWorkloadAdmissionRetired(ctx context.Context, appID, scope string) (bool, error) {
	if s.retired.Load() {
		return true, nil
	}
	return s.MemStore.EnvironmentWorkloadAdmissionRetired(ctx, appID, scope)
}

// retireWithoutNotification retires every workload behind e's store and
// moves the app's generation, without the notification that would drop the
// cached entry.
func retireWithoutNotification(t *testing.T, e *sched.Engine, mem *state.MemStore, app state.App) {
	t.Helper()
	retiring := &retiringStore{MemStore: mem}
	retiring.retired.Store(true)
	(*e.XStore()) = statetest.NewMem(retiring)
	ram := app.RAMMB * 2
	if _, err := mem.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerCacheRunsTheDeferredGateBeforeAFreshDecision(t *testing.T) {
	f := newOwnerCacheFixture(t)
	f.wakeAndPark(t)
	retireWithoutNotification(t, f.e, f.mem, f.app)
	// The cached entry passed the gate at its generation; the admission
	// insert refuses it, and the retry from fresh reads must gate.
	if _, err := f.e.Wake(t.Context(), f.app.ID, "", "", ""); !errors.Is(err, state.ErrEnvironmentWorkloadRetired) {
		t.Fatalf("Wake of a retired workload = %v, want %v", err, state.ErrEnvironmentWorkloadRetired)
	}
	instances, err := f.mem.ListInstancesForApp(t.Context(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range instances {
		if state.State(ins.State) != state.StateParked {
			t.Fatalf("instance %s is %s; want nothing admitted for a retired workload", ins.ID, ins.State)
		}
	}
	if got := f.outcomes(); got["refused"] != 1 {
		t.Fatalf("owner cache outcomes = %v, want the stale entry refused once", got)
	}
}

func TestOwnerCacheChecksTheGenerationBeforeAnOrdinaryThaw(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	ops := wire.NewOpsMetrics("schedd")
	(*f.E()).WithOpsMetrics(ops).WithOwnerCache()
	(*f.E()).EnableFrozenTier()
	// The wake that finds the running instance loads the entry.
	if _, err := (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, ok := (*(*f.E()).Owners()).Lookup((*f.App()).ID); !ok {
		t.Fatal("no owner cache entry after a production wake")
	}
	if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	// The suspension moves the generation; its notification never arrives,
	// so the entry still holds an active account.
	if err := (*f.Store()).UpdateAccountStatus(t.Context(), (*f.App()).AccountID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "", ""); !errors.Is(err, sched.ErrPermanentWake) {
		t.Fatalf("Wake of a suspended account = %v, want %v", err, sched.ErrPermanentWake)
	}
	if row, _ := (*f.Store()).InstanceByID(t.Context(), (*f.Ins()).ID); row.State != string(state.StateFrozen) {
		t.Fatalf("frozen VM state %q after a refused wake; want it still frozen", row.State)
	}
	got := map[string]float64{}
	for _, outcome := range wire.OwnerCacheOutcomes {
		got[outcome] = testutil.ToFloat64(ops.OwnerCacheTotal(outcome))
	}
	if got["refused"] != 1 {
		t.Fatalf("owner cache outcomes = %v, want the stale entry refused once", got)
	}
}

func TestOwnerCacheNotifications(t *testing.T) {
	f := newOwnerCacheFixture(t)
	f.wakeAndPark(t)
	entry, ok := (*f.e.Owners()).Lookup(f.app.ID)
	if !ok {
		t.Fatal("no entry after a wake")
	}
	notify := func(generation int64) {
		payload, _ := json.Marshal(map[string]any{"app_id": f.app.ID, "generation": generation})
		f.e.HandleOwnerCacheNotification(db.Notification{Channel: db.NotifyAppRuntimeChanged, Payload: string(payload)})
	}
	notify((*entry.Generation()))
	if _, ok := (*f.e.Owners()).Lookup(f.app.ID); !ok {
		t.Fatal("the entry's own generation dropped it")
	}
	notify((*entry.Generation()) + 1)
	if _, ok := (*f.e.Owners()).Lookup(f.app.ID); ok {
		t.Fatal("a newer generation left the entry in place")
	}
	f.wakeAndPark(t)
	f.e.HandleOwnerCacheNotification(db.Notification{Channel: db.ResyncChannel})
	if _, ok := (*f.e.Owners()).Lookup(f.app.ID); ok {
		t.Fatal("a resync left an entry in place")
	}
	if got := f.outcomes(); got["invalidated"] != 1 || got["resync"] != 1 {
		t.Fatalf("owner cache outcomes = %v", got)
	}
}

// A boot admitted from the cache publishes on the generation its admission
// insert checked, without rereading the managed Postgres barrier; when the
// generation moves during the boot it publishes through the fence instead,
// and the booted VM serves.
func TestOwnerCacheBootPublishesOnItsAdmissionGeneration(t *testing.T) {
	for _, tc := range []struct {
		name           string
		moveDuringBoot bool
	}{
		{name: "generation held"},
		{name: "generation moved during the boot", moveDuringBoot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := state.NewMemStore()
			_, app, _ := sched.SeedApp(t, mem, api.PlanPro, 512, 5)
			counter := &sched.ReadCountingStore{MemStore: mem}
			ops := wire.NewOpsMetrics("schedd")
			vmm := &sched.FakeVMM{}
			e := sched.XNewEngine(t, statetest.NewMem(counter), vmm, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops).WithOwnerCache()
			f := ownerCacheFixture{mem: mem, counter: counter, e: e, ops: ops, app: app}
			f.wakeAndPark(t)
			if tc.moveDuringBoot {
				(*
				// A new deployment moves the generation without touching the
				// fence or the plan, so only the generation refuses the first
				// attempt.
				vmm.ColdBootHook()) = func() {
					if _, err := mem.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage}); err != nil {
						t.Error(err)
					}
				}
			}
			barrierReads := counter.Reads()["ManagedPostgresAdmissionFenced"]
			result, err := e.Wake(t.Context(), app.ID, "", "", "")
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if ins, err := mem.InstanceByID(t.Context(), result.InstanceID); err != nil || ins.State != string(state.StateRunning) {
				t.Fatalf("instance = %+v, %v; want it published RUNNING", ins, err)
			}
			got := f.outcomes()
			wantBarrierReads := 0
			if tc.moveDuringBoot {
				wantBarrierReads = 1
				if got["guarded_publish_moved"] != 1 || got["guarded_publish"] != 1 {
					t.Fatalf("owner cache outcomes = %v, want the guarded publication refused once and published through the fence", got)
				}
			} else if got["guarded_publish"] != 2 || got["guarded_publish_moved"] != 0 {
				// The first wake loaded the entry and published guarded too.
				t.Fatalf("owner cache outcomes = %v, want guarded publications only", got)
			}
			if n := counter.Reads()["ManagedPostgresAdmissionFenced"] - barrierReads; n != wantBarrierReads {
				t.Fatalf("managed Postgres barrier reads during the cached wake = %d, want %d", n, wantBarrierReads)
			}
		})
	}
}

// The gateway wakes through EnsureWake, whose coordinated selection kept the
// cache off for every real wake (production-us 2026-10-21:
// schedd_owner_cache_total stayed 0 on every series with FAAS_OWNER_CACHE=1).
// A selection of the entry's own deployment is served from the entry.
func TestOwnerCacheServesCoordinatedWakes(t *testing.T) {
	f := newOwnerCacheFixture(t)
	for range 2 {
		out, err := f.e.EnsureWake(t.Context(), f.app.ID, sched.TriggerGateway)
		if err != nil || out.Instance == nil {
			t.Fatalf("EnsureWake: %+v, %v", out.Instance, err)
		}
		if err := f.e.Park(t.Context(), out.Instance.InstanceID); err != nil {
			t.Fatalf("Park: %v", err)
		}
	}
	if got := f.outcomes(); got["miss"] != 1 || got["hit"] < 2 || got["refused"] != 0 || got["selection"] != 0 {
		t.Fatalf("owner cache outcomes = %v, want one miss and hits after it", got)
	}
}

// A coordinated wake's selection is read fresh, but the entry it agrees with
// can still be stale; the admission insert's generation guard refuses it.
func TestOwnerCacheRefusesAStaleEntryForACoordinatedWake(t *testing.T) {
	f := newOwnerCacheFixture(t)
	f.wakeAndPark(t)
	ram := 1024
	if _, err := f.mem.UpdateApp(t.Context(), f.app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	out, err := f.e.EnsureWake(t.Context(), f.app.ID, sched.TriggerGateway)
	if err != nil || out.Instance == nil {
		t.Fatalf("EnsureWake after a missed notification: %+v, %v", out.Instance, err)
	}
	ins, err := f.mem.InstanceByID(t.Context(), out.Instance.InstanceID)
	if err != nil || ins.RAMMB != ram {
		t.Fatalf("admitted instance RAM = %d, %v; want the current %d", ins.RAMMB, err, ram)
	}
	if got := f.outcomes(); got["refused"] != 1 {
		t.Fatalf("owner cache outcomes = %v, want the stale entry refused once", got)
	}
}
