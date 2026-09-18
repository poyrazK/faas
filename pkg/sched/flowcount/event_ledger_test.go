// adr: 127 — bounded egress flow-event telemetry is an additive observability
// seam for the future backend reader; it does not alter G7 reaper semantics.
package flowcount

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEventLedgerAggregatesLifecycleAndDeltas(t *testing.T) {
	base := time.Unix(100, 0)
	ledger := NewEventLedger(WithEventClock(func() time.Time { return base }))
	event := FlowEvent{
		InstanceID: "inst-A",
		Protocol:   "tcp",
		RemoteIP:   "203.0.113.9",
		RemotePort: 443,
		State:      "ESTABLISHED",
		Direction:  "outbound",
		Kind:       FlowEventOpen,
		Bytes:      100,
		Packets:    2,
		At:         base,
	}
	if err := ledger.Observe(event); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ledger.Observe(FlowEvent{
		InstanceID: "inst-A", Protocol: "tcp", RemoteIP: "203.0.113.9", RemotePort: 443,
		State: "ESTABLISHED", Direction: "outbound", Kind: FlowEventUpdate,
		Bytes: 50, Packets: 1, At: base.Add(time.Second),
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := ledger.Observe(FlowEvent{
		InstanceID: "inst-A", Protocol: "tcp", RemoteIP: "203.0.113.9", RemotePort: 443,
		State: "FIN_WAIT", Direction: "outbound", Kind: FlowEventClose,
		Bytes: 25, Packets: 1, At: base.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("close: %v", err)
	}

	rows, err := ledger.DetailSnapshot(context.Background(), "inst-A")
	if err != nil {
		t.Fatalf("DetailSnapshot: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want one detail", rows)
	}
	got := rows[0]
	if got.Count != 1 || got.Bytes != 175 || got.Packets != 4 || got.Active {
		t.Fatalf("detail = %#v, want count=1 bytes=175 packets=4 inactive", got)
	}
	if got.State != "FIN_WAIT" || !got.FirstSeen.Equal(base) || !got.LastSeen.Equal(base.Add(2*time.Second)) {
		t.Fatalf("timestamps/state = %#v, want lifecycle range and FIN_WAIT", got)
	}

	// A later open on the same key represents a new lifecycle and increments
	// Count without creating another unbounded key.
	if err := ledger.Observe(FlowEvent{
		InstanceID: "inst-A", Protocol: "tcp", RemoteIP: "203.0.113.9", RemotePort: 443,
		State: "SYN_SENT", Direction: "outbound", Kind: FlowEventOpen, At: base.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("second open: %v", err)
	}
	rows, err = ledger.DetailSnapshot(context.Background(), "inst-A")
	if err != nil {
		t.Fatalf("DetailSnapshot after reopen: %v", err)
	}
	if rows[0].Count != 2 || !rows[0].Active || rows[0].State != "SYN_SENT" {
		t.Fatalf("reopened detail = %#v, want count=2 active SYN_SENT", rows[0])
	}
}

func TestEventLedgerBoundsDistinctKeys(t *testing.T) {
	base := time.Unix(200, 0)
	ledger := NewEventLedger(
		WithMaxEventFlows(1),
		WithEventClock(func() time.Time { return base }),
	)
	newEvent := func(ip string) FlowEvent {
		return FlowEvent{
			InstanceID: "inst-A", Protocol: "tcp", RemoteIP: ip, RemotePort: 443,
			Direction: "outbound", Kind: FlowEventOpen, At: base,
		}
	}
	if err := ledger.Observe(newEvent("203.0.113.1")); err != nil {
		t.Fatalf("first flow: %v", err)
	}
	if err := ledger.Observe(newEvent("203.0.113.2")); err != nil {
		t.Fatalf("bounded flow should be dropped, got error: %v", err)
	}
	if got := ledger.DroppedEvents(); got != 1 {
		t.Fatalf("DroppedEvents = %d, want 1", got)
	}
	rows, err := ledger.DetailSnapshot(context.Background(), "inst-A")
	if err != nil {
		t.Fatalf("DetailSnapshot: %v", err)
	}
	if len(rows) != 1 || rows[0].RemoteIP != "203.0.113.1" {
		t.Fatalf("rows = %#v, want only first flow", rows)
	}
}

func TestEventLedgerPrunesQuietFlows(t *testing.T) {
	now := time.Unix(300, 0)
	ledger := NewEventLedger(
		WithEventClock(func() time.Time { return now }),
		WithEventTTL(time.Minute),
	)
	if err := ledger.Observe(FlowEvent{
		InstanceID: "inst-A", Protocol: "udp", RemoteIP: "198.51.100.4", RemotePort: 53,
		Direction: "outbound", Kind: FlowEventUpdate, At: now,
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if removed := ledger.Prune(now.Add(59 * time.Second)); removed != 0 {
		t.Fatalf("Prune before ttl = %d, want 0", removed)
	}
	if removed := ledger.Prune(now.Add(time.Minute)); removed != 1 {
		t.Fatalf("Prune at ttl = %d, want 1", removed)
	}
	rows, err := ledger.DetailSnapshot(context.Background(), "inst-A")
	if err != nil {
		t.Fatalf("DetailSnapshot after prune: %v", err)
	}
	if rows != nil {
		t.Fatalf("rows after prune = %#v, want nil", rows)
	}
}

func TestEventLedgerRejectsInvalidAndCancelledRequests(t *testing.T) {
	ledger := NewEventLedger()
	if err := ledger.Observe(FlowEvent{InstanceID: "inst-A"}); !errors.Is(err, ErrInvalidFlowEvent) {
		t.Fatalf("invalid event error = %v, want ErrInvalidFlowEvent", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ledger.DetailSnapshot(ctx, "inst-A"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot error = %v, want context.Canceled", err)
	}
}
