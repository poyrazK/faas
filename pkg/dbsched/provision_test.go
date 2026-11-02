// adr: 1027 — schedd bootstraps a new database in the background
// (amendment 2026-11-02): ProvisionDatabase answers at once, its caller's
// deadline no longer cuts a bootstrap off, one replica's bootstrap keeps
// the others off under its lease, and a bootstrap a replica left behind is
// resumed once the lease lapses.
package dbsched

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func provisionRequest() ProvisionRequest {
	return ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}
}

// startHeldProvision starts req's bootstrap, holds it at the guest's
// bootstrap and returns once it booted; closing the channel lets it go on.
func startHeldProvision(t *testing.T, s *Scheduler, vmm *fakeVMM, req ProvisionRequest) chan struct{} {
	t.Helper()
	hold := make(chan struct{})
	vmm.mu.Lock()
	vmm.bootstrapHold = hold
	vmm.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	vm, err := s.StartProvision(ctx, req)
	// The RPC ends, as apid's request budget ended it mid-bootstrap.
	cancel()
	if err != nil || vm.State != StateProvisioning {
		t.Fatalf("start = %+v, %v; want it provisioning at once", vm, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for vmm.callCount("start") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the bootstrap never booted the VM")
		}
		time.Sleep(time.Millisecond)
	}
	return hold
}

func TestStartProvisionAnswersBeforeTheBootstrap(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	req := provisionRequest()
	hold := startHeldProvision(t, s, vmm, req)
	if got, _ := store.Get(ctx, req.DatabaseID); got.State != StateProvisioning {
		t.Fatalf("while bootstrapping: %+v", got)
	}
	close(hold)
	s.provisionWG.Wait()
	got, _ := store.Get(ctx, req.DatabaseID)
	if got.State != StateParked || !got.Bootstrapped || vmm.callCount("bootstrap") != 1 || vmm.callCount("stop") != 1 {
		t.Fatalf("after the bootstrap: %+v; calls %v", got, vmm.calls)
	}
}

// A retried ProvisionDatabase or a resume sweep on another replica leaves a
// bootstrap whose lease is held alone.
func TestAnotherReplicaLeavesARunningBootstrapAlone(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	other := New(store, vmm, s.cfg, nil)
	ctx := context.Background()
	req := provisionRequest()
	hold := startHeldProvision(t, s, vmm, req)
	if err := other.ResumeProvisions(ctx); err != nil {
		t.Fatal(err)
	}
	if again, err := other.StartProvision(ctx, req); err != nil || again.State != StateProvisioning {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	other.provisionWG.Wait()
	close(hold)
	s.provisionWG.Wait()
	got, _ := store.Get(ctx, req.DatabaseID)
	if got.State != StateParked || vmm.callCount("start") != 1 || vmm.callCount("bootstrap") != 1 {
		t.Fatalf("after the bootstrap: %+v; calls %v", got, vmm.calls)
	}
}

func TestDeleteCancelsABootstrap(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	req := provisionRequest()
	startHeldProvision(t, s, vmm, req)
	if err := s.Delete(ctx, req.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, req.DatabaseID); !errors.Is(err, ErrNotFound) || vmm.callCount("bootstrap") != 0 {
		t.Fatalf("after the delete: %v; calls %v", err, vmm.calls)
	}
}

// A replica that stops mid-bootstrap leaves the database provisioning, not
// failed; another resumes it once the lease lapses.
func TestALapsedBootstrapIsResumed(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	var ahead atomic.Int64
	store.now = func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) }
	other := New(store, vmm, s.cfg, nil)
	ctx := context.Background()
	req := provisionRequest()
	hold := startHeldProvision(t, s, vmm, req)
	s.stopProvision(req.DatabaseID)
	if got, _ := store.Get(ctx, req.DatabaseID); got.State != StateProvisioning {
		t.Fatalf("after the interruption: %+v", got)
	}
	close(hold)
	if err := other.ResumeProvisions(ctx); err != nil {
		t.Fatal(err)
	}
	other.provisionWG.Wait()
	if vmm.callCount("start") != 1 {
		t.Fatalf("resumed under a live lease; calls %v", vmm.calls)
	}
	ahead.Store(int64(s.cfg.RestoreLease + time.Second))
	if err := other.ResumeProvisions(ctx); err != nil {
		t.Fatal(err)
	}
	other.provisionWG.Wait()
	got, _ := store.Get(ctx, req.DatabaseID)
	if got.State != StateParked || !got.Bootstrapped || vmm.callCount("bootstrap") != 1 {
		t.Fatalf("after the resume: %+v; calls %v", got, vmm.calls)
	}
}

func TestABootstrapPastItsTimeoutFails(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	s.cfg.ProvisionTimeout = 100 * time.Millisecond
	ctx := context.Background()
	req := provisionRequest()
	startHeldProvision(t, s, vmm, req)
	s.provisionWG.Wait()
	got, _ := store.Get(ctx, req.DatabaseID)
	if got.State != StateFailed || got.LastError != errCodeBootstrap || vmm.callCount("stop") != 1 {
		t.Fatalf("after the timeout: %+v; calls %v", got, vmm.calls)
	}
}
