// adr: 1152
// singleton_test.go pins ADR-1152 §4 for control-plane singletons: exactly one
// node runs the work, a standby takes over when the holder stops or loses the
// database, and the old and new holders never run at the same time.

package singleton

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

var fastTimings = Timings{TTL: time.Second, RenewEvery: 100 * time.Millisecond, SafetyMargin: 300 * time.Millisecond}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// cutStore fails every lease call while cut is set, standing in for a node
// partitioned from the database.
type cutStore struct {
	state.ControlPlaneLeaseStore
	cut atomic.Bool
}

var errPartitioned = errors.New("database unreachable")

func (s *cutStore) AcquireControlPlaneLease(ctx context.Context, name, holder string, ttl time.Duration) (state.ControlPlaneLease, bool, error) {
	if s.cut.Load() {
		return state.ControlPlaneLease{}, false, errPartitioned
	}
	return s.ControlPlaneLeaseStore.AcquireControlPlaneLease(ctx, name, holder, ttl)
}

func (s *cutStore) ReleaseControlPlaneLease(ctx context.Context, name, holder string, epoch int64) error {
	if s.cut.Load() {
		return errPartitioned
	}
	return s.ControlPlaneLeaseStore.ReleaseControlPlaneLease(ctx, name, holder, epoch)
}

// tracker records which runner is working and fails on overlap.
type tracker struct {
	mu       sync.Mutex
	running  map[string]int64 // holder -> epoch
	overlaps int
	starts   []string
}

func newTracker() *tracker { return &tracker{running: map[string]int64{}} }

func (tr *tracker) work(holder string) Work {
	return func(ctx context.Context, epoch int64) error {
		tr.mu.Lock()
		if len(tr.running) > 0 {
			tr.overlaps++
		}
		tr.running[holder] = epoch
		tr.starts = append(tr.starts, holder)
		tr.mu.Unlock()
		<-ctx.Done()
		tr.mu.Lock()
		delete(tr.running, holder)
		tr.mu.Unlock()
		return ctx.Err()
	}
}

func (tr *tracker) overlapCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.overlaps
}

func (tr *tracker) holder() (string, int64) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for h, e := range tr.running {
		return h, e
	}
	return "", 0
}

func waitFor(t *testing.T, within time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", within, what)
}

func startRunner(t *testing.T, store state.ControlPlaneLeaseStore, holder string, work Work) (cancel func(), done <-chan error) {
	t.Helper()
	r, err := New(store, "central-scheduler", holder, fastTimings, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { ch <- r.Run(ctx, work) }()
	return stop, ch
}

// adr: 1152
func TestRunnerStandbyTakesOverAfterShutdownWithANewEpoch(t *testing.T) {
	store := state.NewMemStore()
	tr := newTracker()
	stopA, doneA := startRunner(t, store, "node-a", tr.work("node-a"))
	waitFor(t, time.Second, func() bool { h, _ := tr.holder(); return h == "node-a" }, "node-a to hold")
	_, epochA := tr.holder()
	stopB, doneB := startRunner(t, store, "node-b", tr.work("node-b"))
	defer func() { stopB(); <-doneB }()

	time.Sleep(3 * fastTimings.RenewEvery)
	if h, _ := tr.holder(); h != "node-a" {
		t.Fatalf("holder changed to %q while node-a was healthy", h)
	}

	stopA()
	if err := <-doneA; !errors.Is(err, context.Canceled) {
		t.Fatalf("node-a Run = %v", err)
	}
	// node-a released on shutdown, so node-b needs no expiry wait.
	waitFor(t, 3*fastTimings.RenewEvery, func() bool { h, _ := tr.holder(); return h == "node-b" }, "node-b to take over")
	if _, epochB := tr.holder(); epochB <= epochA {
		t.Fatalf("takeover epoch %d not above %d", epochB, epochA)
	}
	if n := tr.overlapCount(); n != 0 {
		t.Fatalf("%d overlapping holders", n)
	}
}

// adr: 1152
// TestRunnerPartitionedHolderStopsBeforeTakeover: the holder loses the
// database. It must stop its work on its own before the lease expires by the
// database clock, so the standby's takeover never overlaps it.
func TestRunnerPartitionedHolderStopsBeforeTakeover(t *testing.T) {
	mem := state.NewMemStore()
	storeA := &cutStore{ControlPlaneLeaseStore: mem}
	tr := newTracker()
	stopA, doneA := startRunner(t, storeA, "node-a", tr.work("node-a"))
	defer func() { stopA(); <-doneA }()
	waitFor(t, time.Second, func() bool { h, _ := tr.holder(); return h == "node-a" }, "node-a to hold")
	stopB, doneB := startRunner(t, mem, "node-b", tr.work("node-b"))
	defer func() { stopB(); <-doneB }()

	storeA.cut.Store(true)
	cutAt := time.Now()
	waitFor(t, fastTimings.TTL, func() bool { h, _ := tr.holder(); return h != "node-a" }, "partitioned node-a to stop")
	if stoppedAfter := time.Since(cutAt); stoppedAfter >= fastTimings.TTL {
		t.Fatalf("node-a stopped %s after the cut, at or past the %s TTL", stoppedAfter, fastTimings.TTL)
	}
	waitFor(t, 2*fastTimings.TTL, func() bool { h, _ := tr.holder(); return h == "node-b" }, "node-b to take over after expiry")
	if n := tr.overlapCount(); n != 0 {
		t.Fatalf("%d overlapping holders", n)
	}

	// node-a's database comes back: it must not take the lease back.
	storeA.cut.Store(false)
	time.Sleep(3 * fastTimings.RenewEvery)
	if h, _ := tr.holder(); h != "node-b" {
		t.Fatalf("holder = %q after node-a reconnected, want node-b", h)
	}
}

// adr: 1152
func TestRunnerWorkReturningReleasesAndBacksOff(t *testing.T) {
	store := state.NewMemStore()
	var runs atomic.Int32
	stop, done := startRunner(t, store, "node-a", func(context.Context, int64) error {
		runs.Add(1)
		return errors.New("work failed")
	})
	defer func() { stop(); <-done }()
	time.Sleep(fastTimings.RenewEvery / 2)
	// Released: another holder can take the lease while node-a backs off.
	if _, held, err := store.AcquireControlPlaneLease(context.Background(), "central-scheduler", "node-b", time.Second); err != nil || !held {
		t.Fatalf("lease not released after work returned: held=%v err=%v", held, err)
	}
	if n := runs.Load(); n != 1 {
		t.Fatalf("work ran %d times within half a renew interval, want 1 (back off)", n)
	}
}

func TestTimingsValidation(t *testing.T) {
	bad := []Timings{
		{TTL: 1500 * time.Millisecond, RenewEvery: 100 * time.Millisecond, SafetyMargin: 100 * time.Millisecond},
		{TTL: time.Second, RenewEvery: 800 * time.Millisecond, SafetyMargin: 300 * time.Millisecond},
	}
	for _, tm := range bad {
		if _, err := New(state.NewMemStore(), "x", "h", tm, quietLog()); err == nil {
			t.Errorf("timings %+v accepted", tm)
		}
	}
	if _, err := New(state.NewMemStore(), "x", "h", Timings{}, quietLog()); err != nil {
		t.Fatalf("production timings rejected: %v", err)
	}
}

// adr: 1152
// TestAcquireWaitsAsStandbyUntilRelease: a second process blocks in Acquire
// while the lease is held and gets it as soon as the holder releases.
func TestAcquireWaitsAsStandbyUntilRelease(t *testing.T) {
	store := state.NewMemStore()
	ra, _ := New(store, "central-scheduler", "node-a", fastTimings, quietLog())
	rb, _ := New(store, "central-scheduler", "node-b", fastTimings, quietLog())
	ha, err := ra.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan *Holding, 1)
	go func() {
		hb, err := rb.Acquire(context.Background())
		if err == nil {
			got <- hb
		}
	}()
	select {
	case <-got:
		t.Fatal("standby acquired a held lease")
	case <-time.After(3 * fastTimings.RenewEvery):
	}
	ha.Release(context.Background())
	if ha.Lost() || ha.Context().Err() == nil {
		t.Fatalf("released holding: lost=%v ctxErr=%v", ha.Lost(), ha.Context().Err())
	}
	select {
	case hb := <-got:
		defer hb.Release(context.Background())
		if hb.Epoch() <= ha.Epoch() {
			t.Fatalf("standby epoch %d not above %d", hb.Epoch(), ha.Epoch())
		}
	case <-time.After(3 * fastTimings.RenewEvery):
		t.Fatal("standby did not take over after release")
	}
}

// adr: 1152
// TestHoldingContextEndsWithErrLeaseLostOnPartition: a whole daemon gated by
// Acquire learns it must exit from the context cause.
func TestHoldingContextEndsWithErrLeaseLostOnPartition(t *testing.T) {
	store := &cutStore{ControlPlaneLeaseStore: state.NewMemStore()}
	r, _ := New(store, "central-scheduler", "node-a", fastTimings, quietLog())
	h, err := r.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release(context.Background())
	store.cut.Store(true)
	select {
	case <-h.Context().Done():
	case <-time.After(fastTimings.TTL):
		t.Fatal("holding outlived the TTL while partitioned")
	}
	if !errors.Is(context.Cause(h.Context()), ErrLeaseLost) || !h.Lost() {
		t.Fatalf("cause=%v lost=%v, want ErrLeaseLost", context.Cause(h.Context()), h.Lost())
	}
}
