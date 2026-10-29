package main

import (
	"context"
	"errors"
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
		s := newPenaltyBoxSharer(box, store, nil, nil)
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
	shareC := newPenaltyBoxSharer(boxC, store, nil, nil)
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
	s := newPenaltyBoxSharer(gateway.NewPenaltyBox(), store, nil, nil)
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

// flakyPenaltyBoxStore fails every call while fail is set.
type flakyPenaltyBoxStore struct {
	mem  *state.MemStore
	fail error
}

func (f *flakyPenaltyBoxStore) RecordEdgePenaltyBoxes(ctx context.Context, boxes []state.EdgePenaltyBox, channel string) error {
	if f.fail != nil {
		return f.fail
	}
	return f.mem.RecordEdgePenaltyBoxes(ctx, boxes, channel)
}

func (f *flakyPenaltyBoxStore) ListEdgePenaltyBoxes(ctx context.Context, after state.EdgePenaltyBoxCursor, limit int) ([]state.EdgePenaltyBox, time.Time, error) {
	if f.fail != nil {
		return nil, time.Time{}, f.fail
	}
	return f.mem.ListEdgePenaltyBoxes(ctx, after, limit)
}

func (f *flakyPenaltyBoxStore) PruneEdgePenaltyBoxes(ctx context.Context, before time.Time) (int64, error) {
	if f.fail != nil {
		return 0, f.fail
	}
	return f.mem.PruneEdgePenaltyBoxes(ctx, before)
}

// shareMetric sums a counter, or a histogram's sample count, over the
// series whose label key has value (every series when key is empty).
func shareMetric(t *testing.T, m *gateway.Metrics, name, key, value string) float64 {
	t.Helper()
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, fam := range fams {
		if fam.GetName() != name {
			continue
		}
		for _, mt := range fam.GetMetric() {
			match := key == ""
			for _, l := range mt.GetLabel() {
				match = match || (l.GetName() == key && l.GetValue() == value)
			}
			if !match {
				continue
			}
			sum += mt.GetCounter().GetValue() + float64(mt.GetHistogram().GetSampleCount())
		}
	}
	return sum
}

// adr: 20261024082000 amendment 1 — sharing counts its operations by
// outcome, the boxes it drops at the queue bound, how long boxes wait to be
// written and how long they take to arrive. Boxes the first read loads and
// lookback re-reads are not arrivals.
func TestPenaltyBoxShareMetrics(t *testing.T) {
	ctx := t.Context()
	store := &flakyPenaltyBoxStore{mem: state.NewMemStore()}
	old := state.EdgePenaltyBox{AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("198.51.100.1/32"), Until: time.Now().Add(time.Hour)}
	if err := store.mem.RecordEdgePenaltyBoxes(ctx, []state.EdgePenaltyBox{old}, db.NotifyEdgePenaltyBox); err != nil {
		t.Fatal(err)
	}
	m := gateway.NewMetrics()
	s := newPenaltyBoxSharer(gateway.NewPenaltyBox(), store, m, nil)
	s.sync(ctx)
	if got := shareMetric(t, m, "gateway_edge_penalty_share_seconds", "stage", "propagated"); got != 0 {
		t.Fatalf("the first read counted %v arrivals, want none", got)
	}

	rec := gateway.PenaltyBoxRecord{AppID: "app-1", RuleID: "rule-1", Client: netip.MustParsePrefix("203.0.113.7/32"), Until: time.Now().Add(time.Hour)}
	s.queue(rec)
	s.queue(rec)
	s.flush(ctx)
	s.sync(ctx)
	s.sync(ctx)
	for _, c := range []struct {
		name, key, value string
		want             float64
	}{
		{"gateway_edge_penalty_share_seconds", "stage", "queued", 2},
		{"gateway_edge_penalty_share_seconds", "stage", "propagated", 2},
		{"gateway_edge_penalty_share_total", "outcome", "ok", 4},
	} {
		if got := shareMetric(t, m, c.name, c.key, c.value); got != c.want {
			t.Errorf("%s{%s=%q} = %v, want %v", c.name, c.key, c.value, got, c.want)
		}
	}

	store.fail = errors.New("database down")
	s.queue(rec)
	s.flush(ctx)
	s.sync(ctx)
	s.prune(ctx)
	if got := shareMetric(t, m, "gateway_edge_penalty_share_total", "outcome", "error"); got != 3 {
		t.Errorf("errors = %v, want one each for flush, sync and prune", got)
	}
	store.fail = nil
	for range penaltyBoxMaxQueued + 3 {
		s.queue(rec)
	}
	s.flush(ctx)
	if got := shareMetric(t, m, "gateway_edge_penalty_share_dropped_total", "", ""); got != 3 {
		t.Errorf("dropped = %v, want 3", got)
	}
}
