package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestLostVMsAreDestroyedWhenTheNodeAnswers pins production-us hunt #9,
// H9-171. RecreateInstance parks the rows of a node that looked dead, but a
// node that was only slow to answer keeps running those VMs: 53 of them
// (5.5 GB) stayed up behind parked rows. Each one is destroyed once the node
// answers again, unless its row serves again.
func TestLostVMsAreDestroyedWhenTheNodeAnswers(t *testing.T) {
	ctx := context.Background()
	e, store := recreateTestEngine(t)
	vmm := &fakeVMM{}
	e.vmm = vmm
	lost := seedInstanceForRecreate(t, store, "running")
	revived := seedInstanceForRecreate(t, store, "running")
	for _, ins := range []state.Instance{lost, revived} {
		if err := e.RecreateInstance(ctx, ins.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpdateInstanceState(ctx, revived.ID, "running"); err != nil {
		t.Fatal(err)
	}

	pending := e.lostVMs.beginPass("node-a")
	if len(pending) != 2 {
		t.Fatalf("pending = %v; want both parked instances", pending)
	}
	if again := e.lostVMs.beginPass("node-a"); again != nil {
		t.Fatalf("a second pass started while one runs: %v", again)
	}
	if got := e.destroyLostVMs(ctx, "node-a", pending); got != 1 || vmm.destroys != 1 {
		t.Fatalf("destroyed = %d (vmm %d); want only the VM whose row is still parked", got, vmm.destroys)
	}
	e.lostVMs.endPass("node-a")
	if left := e.lostVMs.beginPass("node-a"); left != nil {
		t.Fatalf("pending after the pass = %v; want none", left)
	}
	if other := e.lostVMs.beginPass("node-b"); other != nil {
		t.Fatalf("node-b pending = %v; want none", other)
	}
}

func TestLostVMDestroyFailureIsRetried(t *testing.T) {
	ctx := context.Background()
	e, store := recreateTestEngine(t)
	vmm := &fakeVMM{destroyErr: errors.New("vmmd unreachable")}
	e.vmm = vmm
	ins := seedInstanceForRecreate(t, store, "running")
	if err := e.RecreateInstance(ctx, ins.ID); err != nil {
		t.Fatal(err)
	}
	pending := e.lostVMs.beginPass("node-a")
	if got := e.destroyLostVMs(ctx, "node-a", pending); got != 0 {
		t.Fatalf("destroyed = %d; want 0 while vmmd fails", got)
	}
	e.lostVMs.endPass("node-a")

	vmm.mu.Lock()
	vmm.destroyErr = nil
	vmm.mu.Unlock()
	pending = e.lostVMs.beginPass("node-a")
	if got := e.destroyLostVMs(ctx, "node-a", pending); got != 1 {
		t.Fatalf("destroyed = %d on the retry; want 1", got)
	}
	e.lostVMs.endPass("node-a")

	// Past retention a failing destroy is dropped, not retried forever.
	vmm.destroyErr = errors.New("still unreachable")
	e.lostVMs.add("node-a", "stuck", time.Now().Add(-2*lostVMRetention))
	pending = e.lostVMs.beginPass("node-a")
	e.destroyLostVMs(ctx, "node-a", pending)
	e.lostVMs.endPass("node-a")
	if left := e.lostVMs.beginPass("node-a"); left != nil {
		t.Fatalf("pending = %v; want the expired entry dropped", left)
	}
}
