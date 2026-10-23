package main

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022105846 amendment 1 — a box one gateway makes holds on
// another after a flush and a read, and on a gateway that starts later; an
// ended box is not loaded, and a read pages through every new box.
func TestPenaltyBoxSharedAcrossGateways(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	newGateway := func() (*gateway.PenaltyBox, *penaltyBoxSharer) {
		box := gateway.NewPenaltyBox()
		s := newPenaltyBoxSharer(box, store, nil)
		box.OnBox(s.queue)
		s.sync(ctx)
		return box, s
	}
	boxA, shareA := newGateway()
	boxB, shareB := newGateway()
	ip := net.ParseIP("203.0.113.7")
	if !boxA.Hit("app-1", "rule-1", ip, api.EdgeRulePenalty{Seconds: 600, After: 1, WithinSeconds: 60}) {
		t.Fatal("gateway A did not box the client")
	}
	if _, _, ok := boxB.Boxed("app-1", ip); ok {
		t.Fatal("gateway B held the box before it was shared")
	}
	shareA.flush(ctx)
	shareB.sync(ctx)
	if rule, left, ok := boxB.Boxed("app-1", ip); !ok || rule != "rule-1" || left <= 590*time.Second {
		t.Fatalf("gateway B: boxed = %q %v %v, want rule-1 for about 600 s", rule, left, ok)
	}
	if _, _, ok := boxB.Boxed("app-1", net.ParseIP("203.0.113.8")); ok {
		t.Fatal("gateway B boxed another client")
	}

	// A gateway that starts later loads every unexpired box, paging through
	// them, and leaves ended ones out.
	ended := netip.MustParsePrefix("198.51.100.1/32")
	extra := []state.EdgePenaltyBox{{AppID: "app-1", RuleID: "rule-2", Client: ended, Until: time.Now().Add(-time.Second)}}
	for i := range 3 {
		extra = append(extra, state.EdgePenaltyBox{AppID: "app-2", RuleID: "rule-3",
			Client: netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 0, 2, byte(10 + i)}), 32), Until: time.Now().Add(time.Hour)})
	}
	if err := store.RecordEdgePenaltyBoxes(ctx, extra, db.NotifyEdgePenaltyBox); err != nil {
		t.Fatal(err)
	}
	boxC := gateway.NewPenaltyBox()
	shareC := newPenaltyBoxSharer(boxC, store, nil)
	shareC.page = 1
	shareC.sync(ctx)
	for _, held := range []struct {
		app string
		ip  string
	}{{"app-1", "203.0.113.7"}, {"app-2", "192.0.2.10"}, {"app-2", "192.0.2.11"}, {"app-2", "192.0.2.12"}} {
		if _, _, ok := boxC.Boxed(held.app, net.ParseIP(held.ip)); !ok {
			t.Errorf("restarted gateway did not hold %s for %s", held.ip, held.app)
		}
	}
	if _, _, ok := boxC.Boxed("app-1", net.ParseIP("198.51.100.1")); ok {
		t.Error("restarted gateway held an ended box")
	}
}

// adr: 20261022105846 amendment 1 — boxes queued past the bound still hold
// on this gateway but are not shared, and a flush empties the queue.
func TestPenaltyBoxShareQueueBound(t *testing.T) {
	store := state.NewMemStore()
	s := newPenaltyBoxSharer(gateway.NewPenaltyBox(), store, nil)
	rec := gateway.PenaltyBoxRecord{AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("203.0.113.7/32"), Until: time.Now().Add(time.Hour)}
	for range penaltyBoxMaxQueued + 5 {
		s.queue(rec)
	}
	if len(s.pending) != penaltyBoxMaxQueued || s.dropped != 5 {
		t.Fatalf("queued %d, dropped %d; want %d and 5", len(s.pending), s.dropped, penaltyBoxMaxQueued)
	}
	s.flush(t.Context())
	if len(s.pending) != 0 || s.dropped != 0 {
		t.Fatalf("after flush: queued %d, dropped %d", len(s.pending), s.dropped)
	}
	boxes, _, err := store.ListEdgePenaltyBoxes(t.Context(), state.EdgePenaltyBoxCursor{}, 2*penaltyBoxMaxQueued)
	if err != nil || len(boxes) != penaltyBoxMaxQueued {
		t.Fatalf("stored %d boxes, %v; want %d", len(boxes), err, penaltyBoxMaxQueued)
	}
}
