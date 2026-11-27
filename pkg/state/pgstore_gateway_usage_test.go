package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261121210517: a frame written as billed egress adds its response
// bytes to net_tx_bytes exactly once, on top of the interface bytes; an
// unbilled frame leaves net_tx_bytes alone.
func TestPg_GatewayUsageEventBilledEgress(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)
	instance, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Date(2026, 11, 21, 12, 34, 0, 0, time.UTC)
	billed := uuid.NewString()
	for range 2 { // replay before an ACK must remain exactly once.
		if err := s.AppendGatewayUsageEvent(ctx, nodeID, billed, instance.ID, minute, 1, 4096, 0, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AppendGatewayUsageEvent(ctx, nodeID, uuid.NewString(), instance.ID, minute, 1, 100, 0, false); err != nil {
		t.Fatal(err)
	}
	// The sampler's interface bytes for the same minute add on top.
	if err := s.AppendUsage(ctx, acctID, appID, instance.ID, minute, 31_200, 0, 0, 0, 1000, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.UsageByHour(ctx, acctID, minute.Add(-time.Hour), minute.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TXBytes != 4196 || rows[0].NetTxBytes != 5096 || rows[0].Requests != 2 {
		t.Fatalf("usage = %+v, want tx_bytes 4196 net_tx_bytes 5096 requests 2", rows)
	}
}

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
		if err := s.AppendGatewayUsageEvent(ctx, nodeID, eventID, instance.ID, minute, 7, 1234, 1, false); err != nil {
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
