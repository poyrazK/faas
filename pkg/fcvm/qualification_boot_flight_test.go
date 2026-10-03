// adr: 521 — qualification retirement must join an unfinished boot or restore.
package fcvm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type qualificationLateInstanceBootVMM struct {
	*fakeVMM
	entered, cancelSeen, release       chan struct{}
	enterOnce, cancelOnce, releaseOnce sync.Once
}

func (v *qualificationLateInstanceBootVMM) lateSuccess(ctx context.Context) error {
	v.enterOnce.Do(func() { close(v.entered) })
	<-ctx.Done()
	v.cancelOnce.Do(func() { close(v.cancelSeen) })
	<-v.release
	return nil
}

func (v *qualificationLateInstanceBootVMM) BootColdBoot(ctx context.Context, lease Lease, spec ColdBootSpec) error {
	if err := v.fakeVMM.BootColdBoot(ctx, lease, spec); err != nil {
		return err
	}
	return v.lateSuccess(ctx)
}

func (v *qualificationLateInstanceBootVMM) Restore(ctx context.Context, lease Lease, spec RestoreSpec) error {
	if err := v.fakeVMM.Restore(ctx, lease, spec); err != nil {
		return err
	}
	return v.lateSuccess(ctx)
}

func newQualificationLateInstanceBootVMM() *qualificationLateInstanceBootVMM {
	return &qualificationLateInstanceBootVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), cancelSeen: make(chan struct{}), release: make(chan struct{})}
}

func (v *qualificationLateInstanceBootVMM) unblock() {
	v.releaseOnce.Do(func() { close(v.release) })
}

func TestInstanceBootStopJoinsColdBootAndRestore(t *testing.T) {
	for _, mode := range []string{"cold_boot", "restore"} {
		for _, stop := range []string{"destroy", "signal"} {
			t.Run(mode+"/"+stop, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				vmm := newQualificationLateInstanceBootVMM()
				t.Cleanup(vmm.unblock)
				m := newTestManager(&fakeRunner{}, vmm)
				id := "qualification-" + mode + "-" + stop
				bootErr := make(chan error, 1)
				go func() {
					var err error
					if mode == "restore" {
						_, err = m.Wake(ctx, wakeReq(id, usableSnapshot()))
					} else {
						_, err = m.ColdBoot(ctx, req(id))
					}
					bootErr <- err
				}()
				select {
				case <-vmm.entered:
				case <-ctx.Done():
					t.Fatal("boot never entered VMM")
				}
				stopErr := make(chan error, 1)
				go func() {
					if stop == "signal" {
						_, _, err := m.SignalAndKill(ctx, id, syscall.SIGTERM, time.Second)
						stopErr <- err
					} else {
						stopErr <- m.Destroy(ctx, id)
					}
				}()
				select {
				case <-vmm.cancelSeen:
				case <-ctx.Done():
					t.Fatal("stop did not cancel the boot")
				}
				select {
				case err := <-stopErr:
					t.Fatalf("stop acknowledged unfinished boot: %v", err)
				default:
				}
				if _, err := m.ColdBoot(ctx, req(id)); err == nil || !strings.Contains(err.Error(), "boot already in progress") {
					t.Fatalf("duplicate boot while retiring: %v", err)
				}
				vmm.unblock()
				select {
				case err := <-bootErr:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("late boot published: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("boot did not unwind")
				}
				select {
				case err := <-stopErr:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("stop did not finish")
				}
				if m.LiveCount() != 0 || m.LeasedCount() != 0 {
					t.Fatalf("retired live=%d leased=%d", m.LiveCount(), m.LeasedCount())
				}
				m.mu.Lock()
				flights, teardowns := len(m.instanceFlights), len(m.instanceStops)
				m.mu.Unlock()
				vmm.mu.Lock()
				kills := len(vmm.killed)
				vmm.mu.Unlock()
				if flights != 0 || teardowns != 0 || kills == 0 {
					t.Fatalf("retirement flights=%d teardowns=%d kills=%d", flights, teardowns, kills)
				}
			})
		}
	}
}

func TestInstanceBootDestroyTimeoutRetainsCancellationBarrier(t *testing.T) {
	vmm := newQualificationLateInstanceBootVMM()
	t.Cleanup(vmm.unblock)
	m := newTestManager(&fakeRunner{}, vmm)
	id := "qualification-timeout"
	bootErr := make(chan error, 1)
	go func() { _, err := m.ColdBoot(t.Context(), req(id)); bootErr <- err }()
	select {
	case <-vmm.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("boot never entered VMM")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := m.Destroy(ctx, id); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished retirement reported success: %v", err)
	}
	if _, err := m.ColdBoot(t.Context(), req(id)); err == nil {
		t.Fatal("timed-out stop admitted a replacement boot")
	}
	vmm.unblock()
	select {
	case err := <-bootErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late boot: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late boot did not unwind")
	}
	if err := m.Destroy(t.Context(), id); err != nil || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("retirement recovery: %v live=%d leases=%d", err, m.LiveCount(), m.LeasedCount())
	}
}

func TestInstanceBootRejectsAlreadyCancelledRequestBeforeEffects(t *testing.T) {
	vmm := &fakeVMM{}
	runner := &fakeRunner{}
	m := newTestManager(runner, vmm)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.ColdBoot(ctx, req("cancelled-before-start")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if vmm.boots() != 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 || runner.ran("netns add") {
		t.Fatal("cancelled request produced effects")
	}
}

type lateInstanceNetworkRunner struct {
	*fakeRunner
	entered, cancelled, release chan struct{}
	releaseOnce                 sync.Once
}

func (r *lateInstanceNetworkRunner) Run(ctx context.Context, argv []string) error {
	if strings.Contains(strings.Join(argv, " "), "ip netns add") {
		close(r.entered)
		<-ctx.Done()
		close(r.cancelled)
		<-r.release
		return ctx.Err()
	}
	return r.fakeRunner.Run(ctx, argv)
}

func (r *lateInstanceNetworkRunner) unblock() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func TestInstanceBootDestroyJoinsNetworkSetupBeforeVMM(t *testing.T) {
	runner := &lateInstanceNetworkRunner{fakeRunner: &fakeRunner{}, entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(runner.unblock)
	vmm := &fakeVMM{}
	m := newTestManager(runner, vmm)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	id := "qualification-network-setup"
	bootResult := make(chan error, 1)
	go func() { _, err := m.ColdBoot(ctx, req(id)); bootResult <- err }()
	select {
	case <-runner.entered:
	case <-ctx.Done():
		t.Fatal("boot did not enter network setup")
	}
	stopResult := make(chan error, 1)
	go func() { stopResult <- m.Destroy(ctx, id) }()
	select {
	case <-runner.cancelled:
	case <-ctx.Done():
		t.Fatal("destroy did not cancel network setup")
	}
	select {
	case err := <-stopResult:
		t.Fatalf("destroy acknowledged unfinished setup: %v", err)
	default:
	}
	runner.unblock()
	select {
	case err := <-bootResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled network setup: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("network setup did not unwind")
	}
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("destroy did not finish")
	}
	if vmm.boots() != 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("cancelled setup reached VMM or retained its lease")
	}
}
