// adr: 20261024090000 — a terminal park never pauses a guest under a forward
// it can still let finish, and a forward that arrives during the park waits
// for it and then finds the instance gone.
package fcvm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func withParkForwardDrain(t *testing.T, d time.Duration) {
	t.Helper()
	old := parkForwardDrainTimeout
	parkForwardDrainTimeout = d
	t.Cleanup(func() { parkForwardDrainTimeout = old })
}

// newParkHoldManager cold-boots instance "p" with a forward activity tracker.
func newParkHoldManager(t *testing.T, vmm *fakeVMM, act *countingActivity) *Manager {
	t.Helper()
	m := newTestManager(&fakeRunner{}, vmm).WithFreeze(FreezeConfig{Activity: act})
	if _, err := m.ColdBoot(t.Context(), req("p")); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParkForwardDrain(t *testing.T) {
	for _, tc := range []struct {
		name string
		// drain is the drain deadline; atGuest the forwards already at the
		// guest when Park starts; finishAfter ends them (0 keeps them).
		drain       time.Duration
		atGuest     int64
		finishAfter time.Duration
		// wantAtPause is the in-flight count the capture saw; wantMin and
		// wantMax bound how long Park took.
		wantAtPause      int64
		wantMin, wantMax time.Duration
	}{
		{name: "idle guest parks at once", drain: 10 * time.Second, wantMax: 2 * time.Second},
		{name: "forward at the guest finishes before the pause", drain: 10 * time.Second, atGuest: 1,
			finishAfter: 150 * time.Millisecond, wantMin: 150 * time.Millisecond, wantMax: 5 * time.Second},
		{name: "forward still running at the deadline is cut", drain: 200 * time.Millisecond, atGuest: 1,
			wantAtPause: 1, wantMin: 200 * time.Millisecond, wantMax: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withParkForwardDrain(t, tc.drain)
			act := &countingActivity{}
			for range tc.atGuest {
				act.begin("p")
			}
			var atPause atomic.Int64
			vmm := &fakeVMM{}
			vmm.snapshotHook = func(Lease) { atPause.Store(act.count("p")) }
			m := newParkHoldManager(t, vmm, act)
			if tc.finishAfter > 0 {
				timer := time.AfterFunc(tc.finishAfter, func() {
					for range tc.atGuest {
						act.end("p")
					}
				})
				defer timer.Stop()
			}
			started := time.Now()
			if _, err := m.Park(t.Context(), "p", SnapshotSpec{}); err != nil {
				t.Fatalf("Park: %v", err)
			}
			took := time.Since(started)
			if got := atPause.Load(); got != tc.wantAtPause {
				t.Fatalf("forwards at the guest when the capture paused it = %d, want %d", got, tc.wantAtPause)
			}
			if took < tc.wantMin || took > tc.wantMax {
				t.Fatalf("Park took %v, want between %v and %v", took, tc.wantMin, tc.wantMax)
			}
			if m.LiveCount() != 0 {
				t.Fatalf("live instances after Park = %d, want 0", m.LiveCount())
			}
		})
	}
}

// A forward that arrives once the park has begun waits on the park gate
// instead of dialing a guest that is about to pause. It does not hold up the
// drain, and once the VM is gone it reports a live-but-not-frozen miss, so the
// forward fails as undelivered and the gateway re-routes it.
func TestParkHoldsForwardsThatArriveDuringThePark(t *testing.T) {
	withParkForwardDrain(t, 10*time.Second)
	act := &countingActivity{}
	vmm := &fakeVMM{}
	m := newParkHoldManager(t, vmm, act)

	var arrived sync.Once
	var released atomic.Bool
	held := make(chan error, 1)
	act.onQuiet = func() {
		// The drain's first look: a new forward begins and waits.
		arrived.Do(func() {
			act.begin("p")
			go func() {
				defer act.end("p")
				frozen, err := m.AwaitFrozen(context.Background(), "p")
				released.Store(true)
				if err == nil && frozen {
					err = context.Canceled // anything non-nil fails the test
				}
				held <- err
			}()
			// Let the forward register on the gate before the drain looks.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				m.mu.Lock()
				waiting := m.live["p"] != nil && m.live["p"].parkHeld == 1
				m.mu.Unlock()
				if waiting {
					return
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	var heldAtPause atomic.Bool
	vmm.snapshotHook = func(Lease) { heldAtPause.Store(!released.Load()) }

	started := time.Now()
	if _, err := m.Park(t.Context(), "p", SnapshotSpec{}); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("Park took %v: the held forward stalled the drain", took)
	}
	if !heldAtPause.Load() {
		t.Fatal("the forward that arrived during the park was released before the capture")
	}
	select {
	case err := <-held:
		if err != nil {
			t.Fatalf("held forward: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held forward still waiting after the park ended")
	}
	if _, ok := m.NetnsFor("p"); ok {
		t.Fatal("parked instance still has a network namespace for the held forward")
	}
}
