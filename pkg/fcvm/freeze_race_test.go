// adr: 1151 — a forward that races a freeze either reaches a running guest
// (the freeze refuses as busy) or fails fast on the frozen one; a freeze never
// pauses a guest a forward is dialing.
package fcvm

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingActivity is a concurrency-safe forward activity tracker. onQuiet
// runs inside Quiet before the count is read.
type countingActivity struct {
	mu       sync.Mutex
	inflight map[string]int64
	onQuiet  func()
}

func (a *countingActivity) begin(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight == nil {
		a.inflight = map[string]int64{}
	}
	a.inflight[id]++
}

func (a *countingActivity) end(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inflight[id]--
}

func (a *countingActivity) count(id string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inflight[id]
}

func (a *countingActivity) Quiet(id string) (int64, time.Time, bool) {
	if a.onQuiet != nil {
		a.onQuiet()
	}
	return a.count(id), time.Time{}, true
}

// trackingFreezeVMM tracks whether the guest is paused, as Firecracker does.
type trackingFreezeVMM struct {
	*fakeVMM
	paused      atomic.Bool
	pauses      atomic.Int64
	duringPause func()
}

func (v *trackingFreezeVMM) PauseVM(context.Context, Lease) error {
	if v.duringPause != nil {
		v.duringPause()
	}
	v.paused.Store(true)
	v.pauses.Add(1)
	return nil
}

func (v *trackingFreezeVMM) ReclaimVMMemory(Lease) (VMMemoryReclaim, error) {
	return VMMemoryReclaim{}, nil
}

func (v *trackingFreezeVMM) ResumeVM(ctx context.Context, l Lease) error {
	v.paused.Store(false)
	return v.fakeVMM.ResumeVM(ctx, l)
}

func newRaceFreezeManager(t *testing.T) (*Manager, *countingActivity, *trackingFreezeVMM) {
	t.Helper()
	act := &countingActivity{}
	v := &trackingFreezeVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(&fakeRunner{}, v).WithFreeze(FreezeConfig{Activity: act})
	if _, err := m.Wake(t.Context(), admissionWake("f")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "f") })
	return m, act, v
}

// forwardThroughFreeze follows vmmd's forward path: mark activity, check for
// a freeze, then dial. It reports whether it reached the guest, and fails the
// test if the guest was paused at any point while it held the guest.
func forwardThroughFreeze(t *testing.T, m *Manager, act *countingActivity, v *trackingFreezeVMM) bool {
	act.begin("f")
	defer act.end("f")
	frozen, err := m.AwaitFrozen(context.Background(), "f")
	if err != nil {
		t.Errorf("AwaitFrozen: %v", err)
		return false
	}
	if frozen {
		return false
	}
	for range 3 {
		if v.paused.Load() {
			t.Error("a forward reached a paused guest")
		}
		runtime.Gosched()
	}
	return true
}

func awaitTrue(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(time.Millisecond)
	}
}

// A request that marks activity after the freeze published its gate but
// before the freeze checked activity: the freeze refuses as busy and the
// request, which waited for that outcome, reaches the running guest.
func TestFreezeRefusesRequestThatRacesItsActivityCheck(t *testing.T) {
	m, act, v := newRaceFreezeManager(t)
	reached := make(chan bool, 1)
	act.onQuiet = func() {
		go func() { reached <- forwardThroughFreeze(t, m, act, v) }()
		awaitTrue(t, func() bool { return act.count("f") > 0 })
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatalf("Freeze = %v, want ErrInstanceBusy", err)
	}
	select {
	case ok := <-reached:
		if !ok {
			t.Fatal("the request failed although the freeze backed off")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not finish")
	}
	if v.pauses.Load() != 0 {
		t.Fatalf("a refused freeze paused the guest %d times", v.pauses.Load())
	}
	if frozen, _ := m.FrozenState("f"); frozen {
		t.Fatal("a refused freeze marked the instance frozen")
	}
}

// A request that marks activity after the freeze's activity check, while the
// pause is under way, waits for the freeze and fails fast instead of dialing
// the paused guest.
func TestRequestFailsFastWhenFreezeWinsTheRace(t *testing.T) {
	m, act, v := newRaceFreezeManager(t)
	reached := make(chan bool, 1)
	v.duringPause = func() {
		go func() { reached <- forwardThroughFreeze(t, m, act, v) }()
		awaitTrue(t, func() bool { return act.count("f") > 0 })
	}
	if _, err := m.Freeze(t.Context(), "f", FreezeOptions{}); err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	select {
	case ok := <-reached:
		if ok {
			t.Fatal("the request reached a frozen guest")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not finish")
	}
	if frozen, _ := m.FrozenState("f"); !frozen || v.pauses.Load() != 1 {
		t.Fatalf("frozen=%v pauses=%d, want a frozen instance paused once", frozen, v.pauses.Load())
	}
}

// Freeze and a request start together many times. Each round ends with the
// guest frozen or running, and the request never holds a paused guest.
func TestFreezeAndRequestRace(t *testing.T) {
	m, act, v := newRaceFreezeManager(t)
	for round := range 200 {
		var wg sync.WaitGroup
		var freezeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, freezeErr = m.Freeze(context.Background(), "f", FreezeOptions{})
		}()
		go func() {
			defer wg.Done()
			forwardThroughFreeze(t, m, act, v)
		}()
		wg.Wait()
		frozen, _ := m.FrozenState("f")
		switch {
		case freezeErr == nil && frozen && v.paused.Load():
			if err := m.Thaw(t.Context(), "f"); err != nil {
				t.Fatalf("round %d: Thaw: %v", round, err)
			}
		case errors.Is(freezeErr, ErrInstanceBusy) && !frozen && !v.paused.Load():
		default:
			t.Fatalf("round %d: freeze err=%v frozen=%v paused=%v", round, freezeErr, frozen, v.paused.Load())
		}
	}
}
