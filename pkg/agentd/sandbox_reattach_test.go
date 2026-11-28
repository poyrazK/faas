// adr: 20261122132055 §7 — a running persistent session whose stream is
// lost (schedd or agentd restarted) is not failed: its lease lapses, the
// sweep leaves it within the reattach grace, and the next claim reattaches
// to its VM on its node. A VM that did not outlive its stream fails the
// session as vm_lost.
package agentd_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentd"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestPersistentSandboxReattachesAfterItsStreamIsLost(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{id: "lose-vm"}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	if found, err := sbx.ClaimOnce(ctx); err != nil || !found {
		t.Fatalf("claim = %v %v", found, err)
	}
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxReady || sb.NodeID != "agents-1" {
		t.Fatalf("after the lost stream: %s on %q", sb.State, sb.NodeID)
	}
	// The lease lapses; the sweep leaves the session for a reattach.
	time.Sleep(400 * time.Millisecond)
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxReady {
		t.Fatalf("swept a detached session to %s %s", sb.State, sb.OutcomeCode)
	}
	done := holdSandbox(t, sbx)
	waitFor(t, "the reattach", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.starts) == 2
	})
	fake.mu.Lock()
	start := fake.starts[1]
	fake.mu.Unlock()
	if r := start.GetReattach(); r == nil || r.GetNodeId() != "agents-1" || r.GetInstance() != "sandbox-"+id ||
		start.GetResume() != nil || start.GetVolumeGb() != 0 || !start.GetPersistent() {
		t.Fatalf("reattach start = %v", start)
	}
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxClosed {
		t.Fatalf("closed after the reattach: %s %s", sb.State, sb.OutcomeCode)
	}
}

func TestPersistentSandboxWhoseVMDidNotOutliveItsStreamFails(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{id: "lose-vm-gone"}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	if found, err := sbx.ClaimOnce(ctx); err != nil || !found {
		t.Fatalf("claim = %v %v", found, err)
	}
	time.Sleep(400 * time.Millisecond)
	if found, err := sbx.ClaimOnce(ctx); err != nil || !found {
		t.Fatalf("reattach claim = %v %v", found, err)
	}
	if sb := sandboxState(t, store, id); sb.State != agentstore.SandboxFailed || sb.OutcomeCode != "vm_lost" {
		t.Fatalf("a session whose VM is gone = %s %s", sb.State, sb.OutcomeCode)
	}
}
