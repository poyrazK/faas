// spec: §6.2

package gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestVMConcurrencyManagerEnforcesPerInstanceLimit(t *testing.T) {
	var delta atomic.Int64
	m := newVMConcurrencyManager(func(_ string, n int64) { delta.Add(n) })

	release, ok := m.tryAcquire("vm-a", "hobby", 2)
	if !ok {
		t.Fatal("first slot was rejected")
	}
	release2, ok := m.tryAcquire("vm-a", "hobby", 2)
	if !ok {
		t.Fatal("second slot was rejected")
	}
	if _, ok := m.tryAcquire("vm-a", "hobby", 2); ok {
		t.Fatal("third slot exceeded the per-instance limit")
	}
	releaseB, ok := m.tryAcquire("vm-b", "hobby", 2)
	if !ok {
		t.Fatal("a second instance must have an independent slot budget")
	}

	release()
	release2()
	if got := delta.Load(); got != 1 {
		// vm-b's slot is intentionally still held; the two vm-a releases
		// must balance their own +1/-1 deltas.
		t.Fatalf("delta after releasing vm-a slots = %d, want 1", got)
	}
	if len(m.gates) != 1 {
		t.Fatalf("idle vm-a gate should be removed while vm-b is active; gates=%d", len(m.gates))
	}
	releaseB()
}

func TestVMConcurrencyManagerWaitsAndHonorsCancellation(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	release, ok := m.tryAcquire("vm-a", "pro", 1)
	if !ok {
		t.Fatal("initial slot was rejected")
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	result := make(chan struct {
		waited bool
		err    error
	}, 1)
	go func() {
		close(started)
		_, waited, err := m.acquire(ctx, "vm-a", "pro", 1)
		result <- struct {
			waited bool
			err    error
		}{waited: waited, err: err}
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case got := <-result:
		if !got.waited {
			t.Error("cancelled request did not observe a saturated gate")
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Errorf("acquire error = %v, want context.Canceled", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled waiter did not return")
	}
	release()
	if len(m.gates) != 0 {
		t.Fatalf("idle gate was not cleaned up, gates=%d", len(m.gates))
	}
}

func TestVMConcurrencyManagerWakesWaiterOnRelease(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	release, ok := m.tryAcquire("vm-a", "scale", 1)
	if !ok {
		t.Fatal("initial slot was rejected")
	}

	result := make(chan error, 1)
	go func() {
		waitRelease, waited, err := m.acquire(context.Background(), "vm-a", "scale", 1)
		if err == nil && !waited {
			err = context.DeadlineExceeded
		}
		if waitRelease != nil {
			waitRelease()
		}
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	release()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("waiter acquire error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("release did not wake the waiting request")
	}
	if len(m.gates) != 0 {
		t.Fatalf("gate remained after all requests drained, gates=%d", len(m.gates))
	}
}

func TestEffectiveVMConcurrencyLimitMatchesPublishedListenerLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  App
		plan int
		want int
	}{
		{name: "function", app: App{Type: AppTypeFunction}, plan: 80, want: 80},
		{name: "legacy function", app: App{}, plan: 25, want: 25},
		{name: "smaller plan", app: App{Type: AppTypeFunction}, plan: 1, want: 1},
		{name: "request mode app", app: App{Type: AppTypeApp}, plan: 80, want: 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveVMConcurrencyLimit(tc.app, tc.plan); got != tc.want {
				t.Fatalf("effectiveVMConcurrencyLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAdvertisedScaleFunctionBurstDoesNotWaitForCapacity(t *testing.T) {
	m := newVMConcurrencyManager(nil)
	const concurrency = 20
	releases := make([]func(), 0, concurrency)
	for i := 0; i < concurrency; i++ {
		release, ok := m.tryAcquire("vm-a", "scale", effectiveVMConcurrencyLimit(App{Type: AppTypeFunction}, 80))
		if !ok {
			t.Fatalf("request %d entered a capacity wait below the advertised 80-request limit", i+1)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
}

func TestEffectiveAppConcurrencyLimitUsesAppOverride(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  App
		plan int
		want int
	}{
		{name: "default uses plan", app: App{}, plan: 20, want: 20},
		{name: "app override", app: App{MaxConcurrency: 1}, plan: 20, want: 1},
		{name: "override is capped by plan", app: App{MaxConcurrency: 25}, plan: 20, want: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveAppConcurrencyLimit(tc.app, tc.plan); got != tc.want {
				t.Fatalf("effectiveAppConcurrencyLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAcquireVMTargetMovesWaiterToNewSibling(t *testing.T) {
	b := &fakeBackend{app: App{ID: "app", Type: AppTypeFunction}, targets: []Target{{NodeID: "node", InstanceID: "first"}}}
	h := NewHandlerWith(b, NewMetrics(), nil)
	held, ok := h.vmConcurrency.tryAcquire("first", "scale", 1)
	if !ok {
		t.Fatal("failed to occupy first target")
	}
	defer held()

	type result struct {
		pick    PickResult
		release func()
		waited  bool
		err     error
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan result, 1)
	go func() {
		pick, release, waited, err := h.acquireVMTarget(ctx, b.app, PickResult{Target: b.targets[0], OK: true}, 1)
		done <- result{pick: pick, release: release, waited: waited, err: err}
	}()

	time.Sleep(2 * vmConcurrencyRetryInterval)
	b.AddTarget(Target{NodeID: "node", InstanceID: "second"})
	got := <-done
	if got.err != nil {
		t.Fatalf("acquireVMTarget: %v", got.err)
	}
	if got.release != nil {
		defer got.release()
	}
	if !got.waited || got.pick.Target.InstanceID != "second" {
		t.Fatalf("waited=%v target=%q, want waited sibling", got.waited, got.pick.Target.InstanceID)
	}
}
