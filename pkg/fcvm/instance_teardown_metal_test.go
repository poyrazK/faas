//go:build linux && metal

// adr: 395 — failed stops retain the real guest; concurrent retries cannot ack early.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

type heldMetalTeardownVMM struct {
	*JailerVMM
	fail             atomic.Bool
	entered, release chan struct{}
	held             atomic.Bool
}

var errMetalUnconfirmedStop = errors.New("injected unconfirmed guest stop")

func (v *heldMetalTeardownVMM) Kill(ctx context.Context, lease Lease) error {
	if v.fail.Load() {
		return errMetalUnconfirmedStop
	}
	return v.JailerVMM.Kill(ctx, lease)
}

func (v *heldMetalTeardownVMM) DestroyWithExport(ctx context.Context, lease Lease, exportDir string) (int, error) {
	if v.fail.Load() {
		return 0, errMetalUnconfirmedStop
	}
	if v.held.CompareAndSwap(false, true) {
		close(v.entered)
		<-v.release
	}
	return v.JailerVMM.DestroyWithExport(ctx, lease, exportDir)
}

func TestMetalTeardownRetainsOwnershipUntilConfirmed(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	v := &heldMetalTeardownVMM{JailerVMM: newMetalVMM(t, 30*time.Second), entered: make(chan struct{}), release: make(chan struct{})}
	v.fail.Store(true)
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	request := admissionWake("confirmed-teardown")
	request.BaseKey, request.LayerKey = base, layer
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(v.release) }) }
	t.Cleanup(func() {
		v.fail.Store(false)
		release()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := m.Destroy(cleanupCtx, request.Instance); err != nil {
			t.Errorf("cleanup guest: %v", err)
		}
	})
	inst, err := m.Wake(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(ctx, request.Instance); !errors.Is(err, errMetalUnconfirmedStop) {
		t.Fatalf("failed stop was acknowledged: %v", err)
	}
	pid, ok := v.InstancePID(request.Instance)
	if !ok || syscall.Kill(pid, 0) != nil {
		t.Fatal("fixture did not leave an actual guest running after the failed stop")
	}
	if _, err := os.Lstat(filepath.Join("/run/netns", inst.Net.Netns)); err != nil {
		t.Fatalf("failed stop removed the owned network: %v", err)
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("failed stop released the running guest's identity or lease")
	}
	if err := m.ResumeVM(ctx, request.Instance); err == nil {
		t.Fatal("resumed a guest awaiting cleanup")
	}
	v.fail.Store(false)
	result := make(chan error, 1)
	go func() { result <- m.Destroy(ctx, request.Instance) }()
	waitBootSignal(t, ctx, v.entered)
	waiterCtx, waiterCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = m.Destroy(waiterCtx, request.Instance)
	waiterCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent stop acknowledged an unfinished teardown: %v", err)
	}
	release()
	if err := waitInstanceResult(t, ctx, result); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("confirmed teardown leaked manager ownership")
	}
	leakcheck.AssertZero(t)
}
