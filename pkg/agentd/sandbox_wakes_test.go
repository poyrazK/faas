// adr: 20261122132055 — a pause, close or fork request reaches the replica
// holding the session at once: apid's notification renews the lease without
// waiting a third of the lease TTL.
package agentd_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentd"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// waitWithin is waitFor with its own deadline.
func waitWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("no %s within %s", what, d)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSandboxRequestsWakeTheHoldingReplica(t *testing.T) {
	_, store := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	src := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	// A one-minute lease renews every 20 s: anything sooner is the nudge.
	sbx := newSandboxesLease(t, store, fake, agentd.SandboxConfig{Workspaces: &memStore{blob: map[string][]byte{}}}, time.Minute)
	go sbx.ListenRequests(ctx)
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, src).State == agentstore.SandboxReady })

	forkSandbox(t, store, src, agentstore.SandboxEphemeral, 1)
	waitWithin(t, 5*time.Second, "fork capture", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.forks) == 1
	})

	if _, err := store.CloseSandbox(ctx, account, src); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the close waited for a lease renewal")
	}
}

func TestSandboxPauseWakesTheHoldingReplica(t *testing.T) {
	_, store := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	src := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	sbx := newSandboxesLease(t, store, fake, agentd.SandboxConfig{Workspaces: &memStore{blob: map[string][]byte{}}}, time.Minute)
	go sbx.ListenRequests(ctx)
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, src).State == agentstore.SandboxReady })

	if _, err := store.PauseSandbox(ctx, account, src); err != nil {
		t.Fatal(err)
	}
	waitWithin(t, 5*time.Second, "pause", func() bool { return sandboxState(t, store, src).State == agentstore.SandboxPaused })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the paused session's drive did not end")
	}
}
