package main

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// countingPenaltyBoxStore counts the rows reads return.
type countingPenaltyBoxStore struct {
	flakyPenaltyBoxStore
	read int
}

func (c *countingPenaltyBoxStore) ListEdgePenaltyBoxes(ctx context.Context, after state.EdgePenaltyBoxCursor, limit int) ([]state.EdgePenaltyBox, time.Time, error) {
	boxes, now, err := c.flakyPenaltyBoxStore.ListEdgePenaltyBoxes(ctx, after, limit)
	c.read += len(boxes)
	return boxes, now, err
}

// adr: 20261022105846 amendment 1 — a read on a notification starts where
// the last one ended, so a flood of boxes costs each read only the new
// ones; the repair read goes back penaltyBoxLookback and picks up a box
// whose insert committed after newer ones were read.
func TestPenaltyBoxShareReadsMoveForward(t *testing.T) {
	ctx := t.Context()
	mem := state.NewMemStore()
	clock := time.Now().UTC()
	mem.SetEdgePenaltyBoxClock(func() time.Time { return clock })
	store := &countingPenaltyBoxStore{flakyPenaltyBoxStore: flakyPenaltyBoxStore{mem: mem}}
	box := gateway.NewPenaltyBox()
	s := newPenaltyBoxSharer(box, store, nil, nil)
	record := func(ip string) {
		t.Helper()
		if err := mem.RecordEdgePenaltyBoxes(ctx, []state.EdgePenaltyBox{{AppID: "app-1", RuleID: "rule-1",
			Client: netip.MustParsePrefix(ip + "/32"), Until: clock.Add(time.Hour)}}, db.NotifyEdgePenaltyBox); err != nil {
			t.Fatal(err)
		}
	}
	record("203.0.113.1")
	s.sync(ctx)
	clock = clock.Add(time.Second)
	record("203.0.113.2")
	store.read = 0
	s.sync(ctx)
	s.sync(ctx)
	if store.read != 1 {
		t.Fatalf("two reads after one new box returned %d rows, want 1", store.read)
	}

	// A box stamped before the last one read commits only now.
	late := clock.Add(-5 * time.Second)
	saved := clock
	clock = late
	record("203.0.113.3")
	clock = saved
	s.sync(ctx)
	if _, _, ok := box.Boxed("app-1", net.ParseIP("203.0.113.3")); ok {
		t.Fatal("a forward read went back for the late box")
	}
	s.repair(ctx)
	if _, _, ok := box.Boxed("app-1", net.ParseIP("203.0.113.3")); !ok {
		t.Fatal("the repair read missed the late box")
	}
}
