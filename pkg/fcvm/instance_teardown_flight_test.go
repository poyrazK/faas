// adr: 459 — a duplicate stop must not acknowledge unfinished physical cleanup.
package fcvm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type blockedInstanceCleanupRunner struct {
	*fakeRunner
	entered, release       chan struct{}
	enterOnce, releaseOnce sync.Once
	armed                  atomic.Bool
}

func (r *blockedInstanceCleanupRunner) Run(ctx context.Context, argv []string) error {
	if r.armed.Load() && strings.Contains(strings.Join(argv, " "), "ip netns del") {
		r.enterOnce.Do(func() { close(r.entered) })
		<-r.release
	}
	return r.fakeRunner.Run(ctx, argv)
}

func (r *blockedInstanceCleanupRunner) unblock() {
	r.releaseOnce.Do(func() { close(r.release) })
}

// Done is evaluated when the follower enters its context-bounded join. This
// makes assertions on an unfinished stop independent of goroutine scheduling.
type observedTeardownWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedTeardownWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type instanceRetirementVMM struct {
	*fakeVMM
	failure          error
	entered, release chan struct{}
}

func (v *instanceRetirementVMM) awaitFailure() {
	if v.failure != nil && v.entered != nil {
		close(v.entered)
		<-v.release
	}
}

func (v *instanceRetirementVMM) DestroyWithExport(ctx context.Context, lease Lease, exportDir string) (int, error) {
	_, _ = v.fakeVMM.DestroyWithExport(ctx, lease, exportDir)
	v.awaitFailure()
	return 23, v.failure
}

func (v *instanceRetirementVMM) SignalAndKill(ctx context.Context, lease Lease, signal syscall.Signal, grace time.Duration) (bool, int32, error) {
	_, _, _ = v.fakeVMM.SignalAndKill(ctx, lease, signal, grace)
	v.awaitFailure()
	return true, 23, v.failure
}

func TestInstanceTeardownJoinsCleanupAndPreservesOwnerOutcome(t *testing.T) {
	for _, stop := range []string{"destroy", "signal"} {
		for _, failed := range []bool{false, true} {
			name := stop + "/success"
			if failed {
				name = stop + "/uncertain"
			}
			t.Run(name, func(t *testing.T) {
				runner := &blockedInstanceCleanupRunner{fakeRunner: &fakeRunner{}, entered: make(chan struct{}), release: make(chan struct{})}
				t.Cleanup(runner.unblock)
				vmm := &instanceRetirementVMM{fakeVMM: &fakeVMM{}}
				unblockNative := func() {}
				if failed {
					vmm.failure = errors.New("native retirement uncertain")
					vmm.entered, vmm.release = make(chan struct{}), make(chan struct{})
					var releaseOnce sync.Once
					unblockNative = func() { releaseOnce.Do(func() { close(vmm.release) }) }
					t.Cleanup(unblockNative)
					// The owner must fail before Manager cleanup can release
					// networking or its lease. Block the native operation instead.
				}
				m := newTestManager(runner, vmm)
				id := "qualification-retirement-" + stop
				if _, err := m.ColdBoot(t.Context(), req(id)); err != nil {
					t.Fatal(err)
				}
				runner.armed.Store(!failed)
				ownerResult := make(chan error, 1)
				go func() {
					if stop == "signal" {
						_, _, err := m.SignalAndKill(t.Context(), id, syscall.SIGTERM, time.Second)
						ownerResult <- err
					} else {
						ownerResult <- m.Destroy(t.Context(), id)
					}
				}()
				entered := runner.entered
				if failed {
					entered = vmm.entered
				}
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("retirement never reached cleanup")
				}
				followerCtx := &observedTeardownWaitContext{Context: t.Context(), waiting: make(chan struct{})}
				followerResult := make(chan error, 1)
				go func() { followerResult <- m.Destroy(followerCtx, id) }()
				select {
				case <-followerCtx.waiting:
				case err := <-followerResult:
					t.Fatalf("duplicate stop acknowledged unfinished cleanup: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("follower never joined retirement")
				}
				if _, err := m.ColdBoot(t.Context(), req(id)); err == nil || !strings.Contains(err.Error(), "teardown in progress") {
					t.Fatalf("new boot entered unfinished retirement: %v", err)
				}
				waitCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				if err := m.Destroy(waitCtx, id); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("join ignored caller deadline: %v", err)
				}
				select {
				case err := <-ownerResult:
					t.Fatalf("cancelled follower interrupted owner cleanup: %v", err)
				default:
				}
				if failed {
					unblockNative()
				} else {
					runner.unblock()
				}
				for _, result := range []chan error{ownerResult, followerResult} {
					select {
					case err := <-result:
						if !errors.Is(err, vmm.failure) {
							t.Fatalf("retirement lost owner outcome: %v, want %v", err, vmm.failure)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("retirement did not finish")
					}
				}
				vmm.mu.Lock()
				stops := len(vmm.destroyedWithExport) + len(vmm.signalAndKillCalls)
				vmm.mu.Unlock()
				retained := 0
				if failed {
					retained = 1
				}
				if stops != 1 || m.LiveCount() != retained || m.LeasedCount() != retained {
					t.Fatalf("retirement stops=%d live=%d leases=%d", stops, m.LiveCount(), m.LeasedCount())
				}
				if failed {
					vmm.failure = nil
					if err := m.Destroy(t.Context(), id); err != nil || m.LiveCount() != 0 || m.LeasedCount() != 0 {
						t.Fatalf("uncertain retirement recovery: %v live=%d leases=%d", err, m.LiveCount(), m.LeasedCount())
					}
				}
			})
		}
	}
}

func TestInstanceTeardownRejectsDifferentExportTarget(t *testing.T) {
	runner := &blockedInstanceCleanupRunner{fakeRunner: &fakeRunner{}, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(runner.unblock)
	vmm := &instanceRetirementVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(runner, vmm)
	id := "qualification-export"
	if _, err := m.ColdBoot(t.Context(), req(id)); err != nil {
		t.Fatal(err)
	}
	runner.armed.Store(true)
	ownerResult := make(chan error, 1)
	go func() { _, err := m.DestroyWithExport(t.Context(), id, "first-export"); ownerResult <- err }()
	select {
	case <-runner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("retirement never reached cleanup")
	}
	followerCtx := &observedTeardownWaitContext{Context: t.Context(), waiting: make(chan struct{})}
	followerResult := make(chan error, 1)
	go func() { _, err := m.DestroyWithExport(followerCtx, id, "different-export"); followerResult <- err }()
	select {
	case <-followerCtx.waiting:
	case err := <-followerResult:
		t.Fatalf("export follower did not join owner: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("export follower never joined owner")
	}
	runner.unblock()
	if err := <-ownerResult; err != nil {
		t.Fatal(err)
	}
	if err := <-followerResult; err == nil || !strings.Contains(err.Error(), "export target differs") {
		t.Fatalf("different export target borrowed completion: %v", err)
	}
}

func TestInstanceTeardownJoinsParkAndUnexpectedExitCleanup(t *testing.T) {
	for _, lifecycle := range []string{"park", "process_exit"} {
		t.Run(lifecycle, func(t *testing.T) {
			runner := &blockedInstanceCleanupRunner{fakeRunner: &fakeRunner{}, entered: make(chan struct{}), release: make(chan struct{})}
			t.Cleanup(runner.unblock)
			vmm := &instanceRetirementVMM{fakeVMM: &fakeVMM{}}
			m := newTestManager(runner, vmm)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			id := "qualification-" + lifecycle
			if _, err := m.ColdBoot(ctx, req(id)); err != nil {
				t.Fatal(err)
			}
			runner.armed.Store(true)
			ownerResult := make(chan error, 1)
			go func() {
				if lifecycle == "park" {
					_, err := m.Park(ctx, id, SnapshotSpec{})
					ownerResult <- err
				} else {
					m.ProcessExited(id, 137)
					ownerResult <- nil
				}
			}()
			select {
			case <-runner.entered:
			case <-ctx.Done():
				t.Fatal("lifecycle did not reach cleanup")
			}
			followerCtx := &observedTeardownWaitContext{Context: ctx, waiting: make(chan struct{})}
			followerResult := make(chan error, 1)
			go func() { followerResult <- m.Destroy(followerCtx, id) }()
			select {
			case <-followerCtx.waiting:
			case err := <-followerResult:
				t.Fatalf("stop acknowledged unfinished %s cleanup: %v", lifecycle, err)
			case <-ctx.Done():
				t.Fatal("stop did not join lifecycle cleanup")
			}
			if _, err := m.ColdBoot(ctx, req(id)); err == nil {
				t.Fatal("replacement entered unfinished lifecycle cleanup")
			}
			runner.unblock()
			for _, result := range []chan error{ownerResult, followerResult} {
				select {
				case err := <-result:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("lifecycle cleanup did not finish")
				}
			}
			vmm.mu.Lock()
			duplicateStops := len(vmm.destroyedWithExport)
			vmm.mu.Unlock()
			if duplicateStops != 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 {
				t.Fatal("stop bypassed lifecycle cleanup or retained its lease")
			}
		})
	}
}

func TestInstanceTeardownLateProcessExitCannotRemoveReplacement(t *testing.T) {
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)
	registry := NewLivenessRegistry()
	m.WithLivenessProbes(registry, LivenessProbeConfig{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	id := "qualification-exit-replacement"
	if _, err := m.ColdBoot(ctx, req(id)); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	registry.StartProbeLoop(id, func() { close(entered); <-release })
	exited := make(chan struct{})
	go func() { m.ProcessExited(id, 137); close(exited) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("exit callback did not reach its cancellation handoff")
	}
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatal(err)
	}
	replacement, err := m.ColdBoot(ctx, req(id))
	if err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case <-exited:
	case <-ctx.Done():
		t.Fatal("late exit callback did not finish")
	}
	m.mu.Lock()
	preserved := m.live[id] == replacement
	m.mu.Unlock()
	if !preserved || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("late process exit removed a replacement incarnation")
	}
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatal(err)
	}
}
