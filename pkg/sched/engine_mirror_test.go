// engine_mirror_test.go — issue #72 / ADR-133 / ADR-125 PR-A3
// adr: 133
//
// PR-A3 code-review fix #3 moved the per-rule mirror VM
// concurrency cap from pkg/sched.Engine to pkg/gateway.Handler
// (see pkg/gateway/handler_mirror_slot_test.go). The cap-at-max
// sentinel sched.ErrMirrorSlotAtCapacity stays in pkg/sched
// because it's the contract the gateway imports + wraps when its
// per-rule counter is exhausted; the gateway-side tests cover
// the slot acquisition logic end-to-end.
//
// What's still pinned here:
//
//   1. The sentinel's existence and message — a future rename
//      surfaces as a test failure rather than a silent dispatch-
//      goroutine bug (errors.Is in pkg/gateway/mirror_dispatch.go).

package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestErrMirrorSlotAtCapacity_Message pins the sentinel string used
// in the gateway-side errors.Is check (pkg/gateway/mirror_dispatch.go
// ::dispatchMirror) so a future rename of ErrMirrorSlotAtCapacity
// surfaces as a test failure rather than a silent dispatch-goroutine
// bug.
func TestErrMirrorSlotAtCapacity_Message(t *testing.T) {
	t.Parallel()
	if ErrMirrorSlotAtCapacity == nil {
		t.Fatal("ErrMirrorSlotAtCapacity is nil")
	}
	if msg := ErrMirrorSlotAtCapacity.Error(); msg == "" {
		t.Fatal("ErrMirrorSlotAtCapacity.Error() returned empty string")
	}
	// Sanity: the sentinel is distinct from any random error.
	if errors.Is(ErrMirrorSlotAtCapacity, errors.New("mirror slot at capacity")) {
		t.Fatal("ErrMirrorSlotAtCapacity should not errors.Is a fresh same-text error")
	}
}

// TestAdmitMirrorInstance_CanceledBootIsDestroyed pins cleanup ownership for
// a gateway deadline that expires after schedd has created the mirror row.
func TestAdmitMirrorInstance_CanceledBootIsDestroyed(t *testing.T) {
	store := state.NewMemStore()
	_, app, deployment := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{bootStarted: make(chan struct{}, 1)}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := engine.AdmitMirrorInstance(ctx, app.ID, "mirror-rule", deployment.ID)
		done <- err
	}()
	select {
	case <-vmm.bootStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("mirror admission did not reach the VM boot")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AdmitMirrorInstance error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mirror admission did not finish bounded cleanup")
	}

	vmm.mu.Lock()
	destroys, destroyCtxErr := vmm.destroys, vmm.lastDestroyContextErr
	vmm.mu.Unlock()
	if destroys != 1 {
		t.Fatalf("VM destroy calls = %d, want 1", destroys)
	}
	if destroyCtxErr != nil {
		t.Fatalf("VM destroy context error = %v, want nil detached cleanup context", destroyCtxErr)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 1 || instances[0].Mode != string(state.InstanceModeMirror) || instances[0].State != string(state.StateFailed) {
		t.Fatalf("mirror instance after canceled admission = %+v, want one failed mirror row", instances)
	}
}

// TestAdmitMirrorInstance_LateSuccessfulBootIsDestroyed covers a VM manager
// that completes boot after the caller deadline. Even if the VM RPC returns
// success, schedd must not leave a running mirror instance whose ID the
// gateway never received.
func TestAdmitMirrorInstance_LateSuccessfulBootIsDestroyed(t *testing.T) {
	store := state.NewMemStore()
	_, app, deployment := seedApp(t, store, api.PlanPro, 512, 5)
	ctx, cancel := context.WithCancel(context.Background())
	vmm := &fakeVMM{coldBootHook: cancel}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	_, err := engine.AdmitMirrorInstance(ctx, app.ID, "mirror-rule", deployment.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AdmitMirrorInstance error = %v, want context.Canceled", err)
	}

	vmm.mu.Lock()
	destroys, destroyCtxErr := vmm.destroys, vmm.lastDestroyContextErr
	vmm.mu.Unlock()
	if destroys != 1 {
		t.Fatalf("VM destroy calls = %d, want 1", destroys)
	}
	if destroyCtxErr != nil {
		t.Fatalf("VM destroy context error = %v, want nil detached cleanup context", destroyCtxErr)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 1 || instances[0].Mode != string(state.InstanceModeMirror) || instances[0].State != string(state.StateFailed) {
		t.Fatalf("mirror instance after late successful boot = %+v, want one failed mirror row", instances)
	}
}
