// adr: 20261027090013 step 11 — a session VM's cumulative usage is kept
// while the lease is held and the session has a VM; an older record never
// replaces a newer one, and nothing replaces the final one.
package agents_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxUsageRecord(t *testing.T) {
	_, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{IdleTimeoutSec: 120})
	if _, _, err := s.ClaimSandbox(ctx, "agentd", "tok-a", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	usage := func(measured int64, final bool) agents.SandboxUsage {
		return agents.SandboxUsage{Usage: agents.Usage{Method: 1, Samples: int(measured / 1000), ExpectedSamples: int(measured / 1000),
			MeasuredMillis: measured, PausedMillis: measured / 2, PrivateMBMillis: 300 * measured, UnpausedPrivateMBMillis: 150 * measured,
			BillableMBMillis: 264 * measured / 2, PeakPrivateMB: 300}, FrozenMillis: measured / 4, Final: final}
	}
	record := func(token string, u agents.SandboxUsage) bool {
		t.Helper()
		kept, err := s.RecordSandboxUsage(ctx, sb.ID, token, u)
		if err != nil {
			t.Fatal(err)
		}
		return kept
	}
	if record("tok-a", usage(60_000, false)) {
		t.Fatal("a starting session, which has no VM, kept usage")
	}
	if err := s.MarkSandboxReady(ctx, sb.ID, "tok-a", "node-a", "sandbox-vm"); err != nil {
		t.Fatal(err)
	}
	if !record("tok-a", usage(60_000, false)) || !record("tok-a", usage(120_000, false)) {
		t.Fatal("a ready session's usage was not kept")
	}
	if record("tok-other", usage(180_000, false)) {
		t.Fatal("another lease's usage was kept")
	}
	if record("tok-a", usage(60_000, false)) {
		t.Fatal("an older record replaced a newer one")
	}
	if err := s.SetSandboxFrozen(ctx, sb.ID, "tok-a", true); err != nil {
		t.Fatal(err)
	}
	if !record("tok-a", usage(150_000, true)) || record("tok-a", usage(200_000, false)) {
		t.Fatal("the final record was not kept, or was replaced")
	}
	got, found, err := s.SandboxSessionUsage(ctx, account, sb.ID)
	if err != nil || !found || got != usage(150_000, true) {
		t.Fatalf("usage = %+v %v %v", got, found, err)
	}
	if _, err := s.RecordSandboxUsage(ctx, sb.ID, "tok-a", agents.SandboxUsage{Usage: agents.Usage{Method: 1, Samples: 3, ExpectedSamples: 2}}); err == nil {
		t.Fatal("more samples than expected was accepted")
	}
}
