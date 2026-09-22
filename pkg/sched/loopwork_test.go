package sched

// adr: 191

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// blockUntil returns a task that parks on release and signals entry.
func blockUntil(entered chan<- struct{}, release <-chan struct{}) func() {
	return func() {
		entered <- struct{}{}
		<-release
	}
}

// fillSlots occupies every slot of kind and returns the release func.
// The returned wait func blocks until all workers have actually started,
// so a follow-up submit is guaranteed to find the pool saturated.
func fillSlots(t *testing.T, p *workPool, kind workKind) (release func()) {
	t.Helper()
	n := workSpecs[kind].slots
	entered := make(chan struct{}, n)
	gate := make(chan struct{})
	for i := range n {
		// Distinct keys: same-key submits would coalesce, not fill.
		if got := p.submit(kind, string(rune('a'+i)), blockUntil(entered, gate)); got != "queued" {
			t.Fatalf("filler %d: outcome=%q want queued", i, got)
		}
	}
	for range n {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("filler task never started")
		}
	}
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }
}

// TestWorkPoolPrimeRunsInlineWhenSaturated pins the load-bearing half of
// the overflow policy: a dropped snapshot_prime strands the deployment
// in `snapshotting` with nothing to retry it, so prime must run inline
// rather than be discarded.
func TestWorkPoolPrimeRunsInlineWhenSaturated(t *testing.T) {
	ops := wire.NewOpsMetrics("test")
	p := newWorkPool(quietLog(), ops)
	release := fillSlots(t, p, workPrime)
	defer release()

	ran := false
	got := p.submit(workPrime, "overflow", func() { ran = true })
	if got != "inline" {
		t.Fatalf("outcome=%q want inline", got)
	}
	if !ran {
		t.Fatal("inline task did not run on the caller's goroutine")
	}
}

// TestWorkPoolReconcileDropsWhenSaturated pins the other half: reconcile
// kinds are idempotent over a durable table with a safety ticker, so
// dropping beats unbounded goroutine growth.
func TestWorkPoolReconcileDropsWhenSaturated(t *testing.T) {
	for _, kind := range []workKind{workRestart, workAppReconcile, workDeploymentReconcile, workJobCancel} {
		t.Run(string(kind), func(t *testing.T) {
			p := newWorkPool(quietLog(), wire.NewOpsMetrics("test"))
			release := fillSlots(t, p, kind)
			defer release()

			ran := false
			if got := p.submit(kind, "overflow", func() { ran = true }); got != "dropped" {
				t.Fatalf("outcome=%q want dropped", got)
			}
			if ran {
				t.Fatal("dropped task must not run")
			}
		})
	}
}

// TestWorkPoolDroppedKeyIsReleased: a drop means the task never ran, so
// the coalescing key must not linger and swallow the next submit.
func TestWorkPoolDroppedKeyIsReleased(t *testing.T) {
	p := newWorkPool(quietLog(), wire.NewOpsMetrics("test"))
	release := fillSlots(t, p, workAppReconcile)

	if got := p.submit(workAppReconcile, "app-1", func() {}); got != "dropped" {
		t.Fatalf("first overflow outcome=%q want dropped", got)
	}
	release()
	p.drain()

	ran := make(chan struct{})
	if got := p.submit(workAppReconcile, "app-1", func() { close(ran) }); got != "queued" {
		t.Fatalf("second submit outcome=%q want queued (dropped key must not persist)", got)
	}
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("re-submitted task never ran")
	}
}

// TestWorkPoolCoalescesSameKey pins the single-flight the prime path
// relied on before ADR-191 generalised it to every kind.
func TestWorkPoolCoalescesSameKey(t *testing.T) {
	p := newWorkPool(quietLog(), wire.NewOpsMetrics("test"))
	entered := make(chan struct{}, 1)
	gate := make(chan struct{})
	defer close(gate)

	if got := p.submit(workAppReconcile, "app-1", blockUntil(entered, gate)); got != "queued" {
		t.Fatalf("first outcome=%q want queued", got)
	}
	<-entered

	second := false
	if got := p.submit(workAppReconcile, "app-1", func() { second = true }); got != "coalesced" {
		t.Fatalf("duplicate outcome=%q want coalesced", got)
	}
	if second {
		t.Fatal("coalesced task must not run")
	}
	// A different key on the same kind is unaffected.
	if got := p.submit(workAppReconcile, "app-2", func() {}); got != "queued" {
		t.Fatalf("distinct key outcome=%q want queued", got)
	}
}

// TestWorkPoolEmptyKeyDoesNotCoalesce — an empty key opts out, so two
// unrelated tasks of the same kind both run.
func TestWorkPoolEmptyKeyDoesNotCoalesce(t *testing.T) {
	p := newWorkPool(quietLog(), wire.NewOpsMetrics("test"))
	var mu sync.Mutex
	runs := 0
	for range 2 {
		if got := p.submit(workAppReconcile, "", func() {
			mu.Lock()
			runs++
			mu.Unlock()
		}); got != "queued" {
			t.Fatalf("outcome=%q want queued", got)
		}
	}
	p.drain()
	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Fatalf("runs=%d want 2", runs)
	}
}

// TestWorkPoolSurvivesPanickingTask: the loop is the only thing keeping
// this node scheduling, so one bad handler must not take it down or leak
// its slot.
func TestWorkPoolSurvivesPanickingTask(t *testing.T) {
	p := newWorkPool(quietLog(), wire.NewOpsMetrics("test"))
	p.submit(workAppReconcile, "boom", func() { panic("handler exploded") })
	p.drain()

	ran := make(chan struct{})
	p.submit(workAppReconcile, "after", func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("pool stopped accepting work after a panicking task")
	}
	// The panicking key must be released too.
	if got := p.submit(workAppReconcile, "boom", func() {}); got != "queued" {
		t.Fatalf("outcome=%q want queued (panicking task must release its key)", got)
	}
	p.drain()
}

// TestWorkPoolDrainWaitsForInFlight is the property every existing
// loop_test.go call to waitPrimes depends on.
func TestWorkPoolDrainWaitsForInFlight(t *testing.T) {
	p := newWorkPool(quietLog(), wire.NewOpsMetrics("test"))
	done := false
	p.submit(workPrime, "slow", func() {
		time.Sleep(50 * time.Millisecond)
		done = true
	})
	p.drain()
	if !done {
		t.Fatal("drain returned before the in-flight task finished")
	}
}

// TestWorkPoolNilReceiverRunsInline keeps a Loop that was never wired
// through workPool() working instead of silently dropping its work.
func TestWorkPoolNilReceiverRunsInline(t *testing.T) {
	var p *workPool
	ran := false
	if got := p.submit(workPrime, "k", func() { ran = true }); got != "inline" {
		t.Fatalf("outcome=%q want inline", got)
	}
	if !ran {
		t.Fatal("nil pool must still run the task")
	}
	p.drain() // must not panic
}

// TestWorkSpecsCoverEveryKind stops a new kind from being added without
// a budget, which would submit into a nil channel and block forever.
func TestWorkSpecsCoverEveryKind(t *testing.T) {
	for _, kind := range workKinds {
		spec, ok := workSpecs[kind]
		if !ok {
			t.Fatalf("kind %q has no workSpec", kind)
		}
		if spec.slots < 1 {
			t.Fatalf("kind %q has %d slots; must be >= 1", kind, spec.slots)
		}
	}
	if len(workSpecs) != len(workKinds) {
		t.Fatalf("workSpecs has %d entries, workKinds has %d", len(workSpecs), len(workKinds))
	}
}
