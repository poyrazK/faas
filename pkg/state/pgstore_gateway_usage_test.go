package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_GatewayUsageEventIsExactlyOnceAndSamplerFillsResidency(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)
	instance, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Date(2026, 9, 13, 12, 34, 30, 0, time.UTC)
	eventID := uuid.NewString()
	for range 2 { // replay before an ACK must remain exactly once.
		if err := s.AppendGatewayUsageEvent(ctx, nodeID, eventID, instance.ID, minute, 7, 1234, 1); err != nil {
			t.Fatal(err)
		}
	}
	// The event may arrive before the minute sampler. AppendUsage must fill the
	// zero residency placeholder without adding the request counters again.
	if err := s.AppendUsage(ctx, acctID, appID, instance.ID, minute, 31_200, 0, 99, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.UsageByHour(ctx, acctID, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %#v", rows)
	}
	row := rows[0]
	if row.MBSeconds != 31_200 || row.Requests != 7 || row.TXBytes != 1234 || row.ColdBootCount != 1 || row.CPUUsec != 99 {
		t.Fatalf("usage = %+v", row)
	}
}

// Billing review R8-2 slice 3: a frame's service reply bytes are queued once
// for the instance's next internal-egress subtraction, and only once its node
// reports internal egress.
func TestPg_GatewayServiceReplyBytesQueueOnceForSubtraction(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)
	instance, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Minute)
	if err := s.AppendUsage(ctx, acctID, appID, instance.ID, minute, 60, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	observe := func(netTx, internal int64) (int64, int64) {
		t.Helper()
		billed, _, excluded, err := s.AppendNetworkUsageObservationExcludingInternal(ctx, acctID, appID, instance.ID, minute, netTx, true, 0, true, internal)
		if err != nil {
			t.Fatal(err)
		}
		return billed, excluded
	}
	// Before the node reports internal egress the reply bytes stay billed.
	if err := s.AppendGatewayUsageEventWithServiceReply(ctx, nodeID, uuid.NewString(), instance.ID, minute, 1, 0, 0, 9_000); err != nil {
		t.Fatal(err)
	}
	if billed, excluded := observe(10_000, 100); billed != 9_900 || excluded != 100 {
		t.Fatalf("first observation = (billed %d, excluded %d), want (9900, 100)", billed, excluded)
	}
	eventID := uuid.NewString()
	for range 2 { // a replayed frame queues nothing twice
		if err := s.AppendGatewayUsageEventWithServiceReply(ctx, nodeID, eventID, instance.ID, minute, 1, 0, 0, 2_000); err != nil {
			t.Fatal(err)
		}
	}
	if billed, excluded := observe(13_000, 150); billed != 950 || excluded != 2_050 {
		t.Fatalf("second observation = (billed %d, excluded %d), want (950, 50 internal + 2000 reply)", billed, excluded)
	}
	rows, err := s.UsageByHour(ctx, acctID, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil || len(rows) != 1 || rows[0].NetTxBytes != 13_000-100-2_050 {
		t.Fatalf("usage = (%+v, %v), want net_tx_bytes %d", rows, err, 13_000-100-2_050)
	}
}
