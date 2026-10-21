// adr: 1151 — a thawed instance gets a full idle timeout from its thaw, not
// from the last request before it froze.
package sched_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us 2026-10-21 (h10-fn-go, idle timeout 30 s): the reaper froze
// an instance 330 ms after a request thawed it. The thawed row still carried
// the last_request_at it froze with, because the activity batch had not yet
// reported the request that thawed it, and the reaper measured idleness from
// that stamp. A request reaching another node's gateway at that moment
// waited for a second thaw.
func TestReaperGivesAThawedInstanceAFullIdleTimeout(t *testing.T) {
	f := sched.NewFrozenFixture(t, true)
	e, store, ins := *f.E(), *f.Store(), *f.Ins()
	e.EnableFrozenTier()
	ctx := t.Context()
	// The last request before the freeze, as the activity batch recorded it.
	if _, err := store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: ins.ID, LastRequest: time.Now().Add(-2 * time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	reap := func(at time.Time) state.State {
		t.Helper()
		sched.NewLoop(nil, e, sched.TestLog()).WithClock(func() time.Time { return at }).RunReaper(ctx)
		row, err := store.InstanceByID(ctx, ins.ID)
		if err != nil {
			t.Fatal(err)
		}
		return state.State(row.State)
	}
	if got := reap(time.Now()); got != state.StateFrozen {
		t.Fatalf("idle instance state after a reaper tick = %s, want frozen", got)
	}
	// A request thaws it; its activity has not been reported yet.
	if _, err := e.Wake(ctx, f.App().ID, "", "", ""); err != nil || *(*f.Vmm()).Thaws() != 1 {
		t.Fatalf("Wake: %v, thaws %d", err, *(*f.Vmm()).Thaws())
	}
	thawed := time.Now()
	if got := reap(thawed.Add(time.Second)); got != state.StateRunning {
		t.Fatalf("state a second after the thaw = %s, want running: the reaper measured idleness from the request before the freeze", got)
	}
	if got := reap(thawed.Add(31 * time.Second)); got != state.StateFrozen {
		t.Fatalf("state a full idle timeout after the thaw = %s, want frozen", got)
	}
}
