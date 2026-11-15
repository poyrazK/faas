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
	if _, _, err := s.AppendNetworkUsageObservation(ctx, acctID, appID, instance.ID, minute, 10_000, true, 0, true); err != nil {
		t.Fatal(err)
	}
	// Before the node reports internal egress the reply bytes stay billed.
	if err := s.AppendGatewayUsageEventWithServiceReply(ctx, nodeID, uuid.NewString(), instance.ID, minute, 1, 0, 0, 9_000); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SubtractInternalEgress(ctx, appID, instance.ID, minute, 100); err != nil || got != 100 {
		t.Fatalf("first subtraction = (%d, %v), want only the 100 internal bytes", got, err)
	}
	eventID := uuid.NewString()
	for range 2 { // a replayed frame queues nothing twice
		if err := s.AppendGatewayUsageEventWithServiceReply(ctx, nodeID, eventID, instance.ID, minute, 1, 0, 0, 2_000); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.SubtractInternalEgress(ctx, appID, instance.ID, minute, 150); err != nil || got != 2_050 {
		t.Fatalf("second subtraction = (%d, %v), want 50 internal + 2000 reply bytes", got, err)
	}
	rows, err := s.UsageByHour(ctx, acctID, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil || len(rows) != 1 || rows[0].NetTxBytes != 10_000-100-2_050 {
		t.Fatalf("usage = (%+v, %v), want net_tx_bytes %d", rows, err, 10_000-100-2_050)
	}
}
