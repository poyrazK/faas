// adr: 1345 — the owner cache serves a production wake's reads, and a stale
// entry is refused at admission, never used.
package sched

import (
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

type ownerCacheFixture struct {
	mem     *state.MemStore
	counter *readCountingStore
	e       *Engine
	ops     *wire.OpsMetrics
	app     state.App
}

func newOwnerCacheFixture(t *testing.T) ownerCacheFixture {
	t.Helper()
	mem := state.NewMemStore()
	_, app, _ := seedApp(t, mem, api.PlanPro, 512, 5)
	counter := &readCountingStore{MemStore: mem}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, statetest.NewMem(counter), &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops).WithOwnerCache()
	return ownerCacheFixture{mem: mem, counter: counter, e: e, ops: ops, app: app}
}

// wakeAndPark wakes the app and parks the instance, so the next wake admits
// a new one.
func (f ownerCacheFixture) wakeAndPark(t *testing.T) WakeResult {
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
	first := f.counter.reads()["AppByID"]
	f.wakeAndPark(t)
	// The first wake loaded the entry (one app read) and resolved through
	// it; the second reads the app only in the boot's own bookkeeping.
	if second := f.counter.reads()["AppByID"] - first; second >= first {
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

func TestOwnerCacheNotifications(t *testing.T) {
	f := newOwnerCacheFixture(t)
	f.wakeAndPark(t)
	entry, ok := f.e.owners.lookup(f.app.ID)
	if !ok {
		t.Fatal("no entry after a wake")
	}
	notify := func(generation int64) {
		payload, _ := json.Marshal(map[string]any{"app_id": f.app.ID, "generation": generation})
		f.e.HandleOwnerCacheNotification(db.Notification{Channel: db.NotifyAppRuntimeChanged, Payload: string(payload)})
	}
	notify(entry.generation)
	if _, ok := f.e.owners.lookup(f.app.ID); !ok {
		t.Fatal("the entry's own generation dropped it")
	}
	notify(entry.generation + 1)
	if _, ok := f.e.owners.lookup(f.app.ID); ok {
		t.Fatal("a newer generation left the entry in place")
	}
	f.wakeAndPark(t)
	f.e.HandleOwnerCacheNotification(db.Notification{Channel: db.ResyncChannel})
	if _, ok := f.e.owners.lookup(f.app.ID); ok {
		t.Fatal("a resync left an entry in place")
	}
	if got := f.outcomes(); got["invalidated"] != 1 || got["resync"] != 1 {
		t.Fatalf("owner cache outcomes = %v", got)
	}
}
