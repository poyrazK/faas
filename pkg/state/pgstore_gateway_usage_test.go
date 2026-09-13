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
