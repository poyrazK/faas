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

// A reattach that finds no VM, or that cannot succeed, ends the wait: each
// claim moves the grace, so another try would never end. A session with a
// volume resumes cold from it; one without fails as vm_lost.
func TestPersistentSandboxWhoseReattachFailsIsNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		volumeGB     int
		state        string
		outcome      string
	}{
		{"gone with a volume", "lose-vm-gone", 10, agentstore.SandboxPaused, ""},
		{"refused with a volume", "lose-vm-refused", 10, agentstore.SandboxPaused, ""},
		{"gone without a volume", "lose-vm-gone", 0, agentstore.SandboxFailed, "vm_lost"},
		{"refused without a volume", "lose-vm-refused", 0, agentstore.SandboxFailed, "vm_lost"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store := openStore(t)
			ctx := context.Background()
			id := queuePersistentVolume(t, store, time.Hour, tc.volumeGB)
			fake := &fakeSandboxSchedd{scripts: map[string]string{id: tc.script}}
			sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
			if found, err := sbx.ClaimOnce(ctx); err != nil || !found {
				t.Fatalf("claim = %v %v", found, err)
			}
			time.Sleep(400 * time.Millisecond)
			if found, err := sbx.ClaimOnce(ctx); err != nil || !found {
				t.Fatalf("reattach claim = %v %v", found, err)
			}
			if sb := sandboxState(t, store, id); sb.State != tc.state || sb.OutcomeCode != tc.outcome {
				t.Fatalf("after the failed reattach = %s %q, want %s %q", sb.State, sb.OutcomeCode, tc.state, tc.outcome)
			}
		})
	}
}
