package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type countingFleetQueueAdmission struct {
	fakeFleetQueueAdmission
	acquires atomic.Int32
}

func (c *countingFleetQueueAdmission) TryAcquireConcurrencyQueueLease(ctx context.Context, appID string, limit int, wait time.Duration) (string, int, bool, error) {
	c.acquires.Add(1)
	return c.fakeFleetQueueAdmission.TryAcquireConcurrencyQueueLease(ctx, appID, limit, wait)
}

// At saturation every request the local queue refused still wrote, then
// released, a fleet lease row in control-plane Postgres (bug hunt
// 2026-10-27 H11). A refusal decided locally now costs no lease.
func TestEnterQueueSkipsFleetLeaseWhenLocalQueueIsFull(t *testing.T) {
	admission := &countingFleetQueueAdmission{}
	m := newVMConcurrencyManager(nil)
	m.setQueueAdmission(admission)
	ctx := context.Background()
	for i := range 2 {
		if _, _, ok, err := m.enterQueue(ctx, "app", "pro", 2, time.Second); err != nil || !ok {
			t.Fatalf("waiter %d: ok=%v err=%v", i, ok, err)
		}
	}
	for range 5 {
		if _, depth, ok, err := m.enterQueue(ctx, "app", "pro", 2, time.Second); err != nil || ok || depth != 2 {
			t.Fatalf("full queue: depth=%d ok=%v err=%v; want refused at depth 2", depth, ok, err)
		}
	}
	if got := admission.acquires.Load(); got != 2 {
		t.Fatalf("fleet lease acquisitions = %d, want 2 (none for locally refused requests)", got)
	}
}
