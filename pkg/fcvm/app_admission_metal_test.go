//go:build linux && metal

// adr: 468 — durable admission and joined resumes on a real Firecracker guest.
package fcvm

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalAppAdmissionFence(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	var fenced atomic.Bool
	guard := func(_ context.Context, _ string) error {
		if fenced.Load() {
			return ErrAppAdmissionFenced
		}
		return nil
	}
	m := newMetalManager(t, kernel).WithAppAdmissionGuard(guard)
	req := WakeRequest{Instance: "admission-source", AppID: "admission-app", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby"}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := m.Destroy(cleanupCtx, req.Instance); err != nil {
			t.Errorf("cleanup guest: %v", err)
		}
	})
	if _, err := m.Wake(ctx, req); err != nil {
		t.Fatalf("unfenced cold boot: %v", err)
	}
	fenced.Store(true)
	for _, kind := range []string{"resume", "warm_snapshot", "migration_snapshot"} {
		if err := liveAdmissionOperation(ctx, m, req.Instance, kind); !errors.Is(err, ErrAppAdmissionFenced) {
			t.Fatalf("fenced %s: %v", kind, err)
		}
	}
	if err := m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
	for _, manager := range []*Manager{m, newMetalManager(t, kernel).WithAppAdmissionGuard(guard)} {
		if _, err := manager.Wake(ctx, req); !errors.Is(err, ErrAppAdmissionFenced) {
			t.Fatalf("delayed boot after destroy or manager restart: %v", err)
		}
		if manager.LiveCount() != 0 || manager.LeasedCount() != 0 {
			t.Fatal("fenced boot allocated guest resources")
		}
	}
	fenced.Store(false)
	if _, err := m.Wake(ctx, req); err != nil {
		t.Fatalf("boot after completed fence cancellation: %v", err)
	}
	if err := m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}

// Delay the real resume until Destroy's join times out, then model a VMM that
// completes successfully despite RPC cancellation. The guest must still be
// owned by the manager until a retry confirms teardown.
type delayedMetalResumeVMM struct {
	*JailerVMM
	entered, release chan struct{}
	succeeded        atomic.Bool
}

func (v *delayedMetalResumeVMM) ResumeVM(ctx context.Context, lease Lease) error {
	close(v.entered)
	<-ctx.Done()
	<-v.release
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := v.JailerVMM.ResumeVM(resumeCtx, lease)
	v.succeeded.Store(err == nil)
	return err
}

func TestMetalDestroyJoinsLateResume(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	vmm := &delayedMetalResumeVMM{JailerVMM: newMetalVMM(t, 30*time.Second), entered: make(chan struct{}), release: make(chan struct{})}
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil).
		WithAppAdmissionGuard(func(context.Context, string) error { return nil })
	req := WakeRequest{Instance: "admission-late-resume", AppID: "admission-app", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby"}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(vmm.release) }) }
	t.Cleanup(func() {
		release()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := m.Destroy(cleanupCtx, req.Instance); err != nil {
			t.Errorf("cleanup guest: %v", err)
		}
	})
	inst, err := m.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := vmm.apiPatch(ctx, inst.Lease.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		t.Fatalf("pause real guest: %v", err)
	}
	resumeResult := make(chan error, 1)
	go func() { resumeResult <- m.ResumeVM(ctx, req.Instance) }()
	waitBootSignal(t, ctx, vmm.entered)
	stopCtx, stopCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = m.Destroy(stopCtx, req.Instance)
	stopCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("destroy acknowledged an unfinished resume: %v", err)
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("timed-out destruction lost the live guest or lease identity")
	}
	release()
	if err := waitInstanceResult(t, ctx, resumeResult); !errors.Is(err, context.Canceled) {
		t.Fatalf("late resume did not preserve stop cancellation: %v", err)
	}
	if !vmm.succeeded.Load() {
		t.Fatal("fixture did not actually resume the paused Firecracker guest")
	}
	if err := m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("retry leaked the resumed guest or lease")
	}
	leakcheck.AssertZero(t)
}
