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

// ADR-20261122132055: a resumed persistent session runs a new VM under a new
// lease, whose counters start at zero. Its usage adds to the totals of the
// runs before it, past the earlier run's final record.
func TestSandboxUsageSumsAcrossResumes(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	sb := admitSandbox(t, s, agents.SandboxAdmission{IdleTimeoutSec: 120})
	if _, _, err := s.ClaimSandbox(ctx, "agentd", "tok-a", ttl, maxLive(5)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSandboxReady(ctx, sb.ID, "tok-a", "node-a", "sandbox-vm"); err != nil {
		t.Fatal(err)
	}
	usage := func(measured int64, peak int64, final bool) agents.SandboxUsage {
		return agents.SandboxUsage{Usage: agents.Usage{Method: 1, Samples: int(measured / 1000), ExpectedSamples: int(measured / 1000),
			MeasuredMillis: measured, PausedMillis: measured / 2, PrivateMBMillis: 300 * measured, UnpausedPrivateMBMillis: 150 * measured,
			BillableMBMillis: 264 * measured / 2, PeakPrivateMB: peak}, FrozenMillis: measured / 4, Final: final}
	}
	record := func(token string, u agents.SandboxUsage) bool {
		t.Helper()
		kept, err := s.RecordSandboxUsage(ctx, sb.ID, token, u)
		if err != nil {
			t.Fatal(err)
		}
		return kept
	}
	resume := func(token string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET lease_token=$2, state='ready' WHERE id=$1`, sb.ID, token); err != nil {
			t.Fatal(err)
		}
	}
	want := func(u agents.SandboxUsage) {
		t.Helper()
		got, found, err := s.SandboxSessionUsage(ctx, account, sb.ID)
		if err != nil || !found || got != u {
			t.Fatalf("usage = %+v %v %v, want %+v", got, found, err, u)
		}
	}
	if !record("tok-a", usage(60_000, 300, false)) || !record("tok-a", usage(100_000, 300, true)) {
		t.Fatal("the first run's usage was not kept")
	}
	resume("tok-b")
	if record("tok-a", usage(120_000, 300, false)) {
		t.Fatal("the ended run's lease recorded usage")
	}
	if !record("tok-b", usage(10_000, 200, false)) || !record("tok-b", usage(30_000, 200, false)) {
		t.Fatal("the resumed run's usage was not kept")
	}
	want(usage(130_000, 300, false))
	if record("tok-b", usage(20_000, 200, false)) {
		t.Fatal("an older record of the resumed run replaced a newer one")
	}
	if !record("tok-b", usage(40_000, 500, true)) || record("tok-b", usage(50_000, 500, false)) {
		t.Fatal("the resumed run's final record was not kept, or was replaced")
	}
	want(usage(140_000, 500, true))
	resume("tok-c")
	if !record("tok-c", usage(6_000, 100, false)) {
		t.Fatal("a second resume's usage was not kept")
	}
	want(usage(146_000, 500, false))
}
