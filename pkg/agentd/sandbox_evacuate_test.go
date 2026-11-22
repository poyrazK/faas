// adr: 20261122132055 §7 — an evacuated persistent session pauses and asks
// for its resume at once, which restores its capture on its node; an
// evacuated ephemeral session ends with node_evacuated.
package agentd_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentd"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestEvacuatedPersistentSandboxPausesAndResumes(t *testing.T) {
	_, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{id: "evacuate"}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: &memStore{blob: map[string][]byte{}}})
	done := holdSandbox(t, sbx)
	if !<-done {
		t.Fatal("nothing was claimed")
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxPaused || sb.ResumeRequestedAt == nil || len(fake.pauses) != 1 {
		t.Fatalf("evacuated session = %s, resume asked %v, pauses %v", sb.State, sb.ResumeRequestedAt, fake.pauses)
	}
	// It resumes without being asked, on the node that holds its capture.
	done = holdSandbox(t, sbx)
	waitFor(t, "resumed", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	fake.mu.Lock()
	resume := fake.starts[len(fake.starts)-1].GetResume()
	fake.mu.Unlock()
	if resume == nil || !resume.GetRequireNode() || resume.GetNodeId() != "agents-1" {
		t.Fatalf("resume = %v", resume)
	}
	if _, err := store.CloseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestEvacuatedEphemeralSandboxEnds(t *testing.T) {
	_, store := openStore(t)
	id := queueSandbox(t, store, 600)
	fake := &fakeSandboxSchedd{scripts: map[string]string{id: "evacuate"}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{})
	if found, err := sbx.ClaimOnce(context.Background()); err != nil || !found {
		t.Fatalf("claim = %v %v", found, err)
	}
	sb := sandboxState(t, store, id)
	if sb.State != agentstore.SandboxFailed || sb.OutcomeCode != "node_evacuated" {
		t.Fatalf("evacuated ephemeral session = %s %s", sb.State, sb.OutcomeCode)
	}
	if _, _, closes := fake.counts(); closes != 1 {
		t.Fatalf("%d closes", closes)
	}
}
