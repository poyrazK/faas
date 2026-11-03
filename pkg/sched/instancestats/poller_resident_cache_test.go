package instancestats

// adr: 20261102205122 — the telemetry path reads its node's resident instances
// at most once a second, and at once when the node reports a VM it does not
// know.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPollerReusesTheResidentListUntilANewVMOrItsAge(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, node := seedTwoNodes(t, store)
	first := seedInstance(t, store, "app1", node.ID)
	cache := sched.NewNodeTelemetryCache()
	now := time.Unix(500, 0)
	p := NewPoller(store, &statsFakeDialer{}, nil, NewReader(), nil, nilLogger()).WithTelemetry(cache).WithOwnerNodeID(node.ID)
	p.Now = func() time.Time { return now }
	tick := func(reported ...string) {
		t.Helper()
		rows := make([]sched.NodeTelemetry, 0, len(reported))
		for _, id := range reported {
			rows = append(rows, sched.NodeTelemetry{InstanceID: id})
		}
		cache.Replace(node.ID, now, now, rows)
		if err := p.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	has := func(id string) bool {
		_, ok := p.Reader.SnapshotForInstance(id)
		return ok
	}

	tick(first.ID)
	if !has(first.ID) {
		t.Fatal("the first tick did not join the reported instance")
	}
	// A VM the node reports that the list does not hold is joined at once,
	// well inside the list's age.
	second := seedInstance(t, store, "app2", node.ID)
	now = now.Add(200 * time.Millisecond)
	tick(first.ID, second.ID)
	if !has(second.ID) {
		t.Fatal("a new VM the node reported was not joined before the list aged out")
	}
	// A VM that left keeps its row while the list is young, then drops out.
	if err := store.UpdateInstanceState(ctx, first.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(200 * time.Millisecond)
	tick(first.ID, second.ID)
	if !has(first.ID) {
		t.Fatal("the list was reread inside its age without a new VM")
	}
	now = now.Add(residentListMaxAge)
	tick(first.ID, second.ID)
	if has(first.ID) || !has(second.ID) {
		t.Fatalf("after the list aged out: stopped VM listed %v, running VM listed %v", has(first.ID), has(second.ID))
	}
	// The stopped VM vmmd still reports does not force a read every tick.
	if _, unlisted := p.resident.unlisted[first.ID]; !unlisted {
		t.Fatal("a reported VM without a live row was not remembered as unlisted")
	}
}
