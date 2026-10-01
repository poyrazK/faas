// adr: 393 — teardown joins every boot before acknowledging resource destruction.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type lateInstanceBootVMM struct {
	*fakeVMM
	entered, cancelSeen, release chan struct{}
	blockRestore                 bool
}

func (v *lateInstanceBootVMM) wait(ctx context.Context) {
	close(v.entered)
	<-ctx.Done()
	close(v.cancelSeen)
	<-v.release
}

func (v *lateInstanceBootVMM) BootColdBoot(ctx context.Context, lease Lease, spec ColdBootSpec) error {
	v.wait(ctx)
	if lease.IsBuilder {
		if err := os.MkdirAll(filepath.Join(cgroupRoot, BuilderCgroupParent, PerInstanceScope(lease.Instance)), 0o755); err != nil {
			return err
		}
	}
	return v.fakeVMM.BootColdBoot(ctx, lease, spec)
}

func (v *lateInstanceBootVMM) Restore(ctx context.Context, lease Lease, spec RestoreSpec) error {
	if v.blockRestore {
		v.wait(ctx)
	}
	return v.fakeVMM.Restore(ctx, lease, spec)
}

func newLateInstanceBootVMM() *lateInstanceBootVMM {
	return &lateInstanceBootVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), cancelSeen: make(chan struct{}), release: make(chan struct{})}
}

func waitBootSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal("boot/stop did not reach the expected barrier")
	}
}

func TestWakeStopJoinsBootAndFencesLateSuccess(t *testing.T) {
	for _, kind := range []string{"cold", "restore", "warm", "app_task", "execution", "builder"} {
		for _, stopKind := range []string{"destroy", "signal"} {
			t.Run(kind+"/"+stopKind, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				v := newLateInstanceBootVMM()
				m := newTestManager(&fakeRunner{}, v)
				req := WakeRequest{Instance: "late-boot", BaseKey: "base/node22.ext4", LayerKey: "app.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanHobby}
				switch kind {
				case "restore", "warm":
					req.Snapshot, req.KeepPaused, v.blockRestore = usableSnapshot(), kind == "warm", true
				case "app_task":
					req.AppTaskOnly = true
				case "execution":
					req.ExecutionOnly = true
				case "builder":
					req.ExportDir = t.TempDir()
				}
				bootErr := make(chan error, 1)
				go func() { _, err := m.Wake(ctx, req); bootErr <- err }()
				waitBootSignal(t, ctx, v.entered)
				stopErr := make(chan error, 1)
				go func() {
					if stopKind == "signal" {
						_, _, err := m.SignalAndKill(ctx, req.Instance, syscall.SIGTERM, time.Second)
						stopErr <- err
						return
					}
					stopErr <- m.Destroy(ctx, req.Instance)
				}()
				waitBootSignal(t, ctx, v.cancelSeen)
				select {
				case err := <-stopErr:
					t.Fatalf("stop acknowledged while boot was still in progress: %v", err)
				default:
				}
				close(v.release)
				select {
				case err := <-bootErr:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("late boot result: %v", err)
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
				if m.LiveCount() != 0 || m.alloc.InUse() != 0 {
					t.Fatalf("live=%d leases=%d", m.LiveCount(), m.alloc.InUse())
				}
				m.mu.Lock()
				flights, waking := len(m.instanceFlights), len(m.waking)
				m.mu.Unlock()
				if flights != 0 || waking != 0 {
					t.Fatalf("flights=%d waking=%d", flights, waking)
				}
				v.mu.Lock()
				kills := len(v.killed)
				v.mu.Unlock()
				if kills == 0 {
					t.Fatal("late boot did not receive cleanup")
				}
			})
		}
	}
}

func TestWakeStopTimeoutRetainsBootBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	v := newLateInstanceBootVMM()
	m := newTestManager(&fakeRunner{}, v)
	req := WakeRequest{Instance: "boot-timeout", BaseKey: "base.ext4", LayerKey: "app.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanHobby}
	bootErr := make(chan error, 1)
	go func() { _, err := m.Wake(ctx, req); bootErr <- err }()
	waitBootSignal(t, ctx, v.entered)
	stopCtx, stopCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stopCancel()
	if err := m.Destroy(stopCtx, req.Instance); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("destroy acknowledged a stuck boot: %v", err)
	}
	if _, err := m.Wake(ctx, req); err == nil {
		t.Fatal("duplicate boot entered while cancellation was pending")
	}
	close(v.release)
	select {
	case err := <-bootErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("boot did not finish")
	}
	if err := m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
}
