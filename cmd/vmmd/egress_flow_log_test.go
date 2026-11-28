// adr: 371 — egress flow log capture.
package main

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func flow(ip string, port uint16) netns.EgressFlow {
	return netns.EgressFlow{Addr: netip.MustParseAddr(ip), Port: port}
}

// A pair is logged once while it stays in the set, again after it expires
// and reappears, and a failed listing leaves other instances unaffected.
func TestEgressFlowLoggerWritesNewPairsOnce(t *testing.T) {
	store := state.NewMemStore()
	sets := map[string][]netns.EgressFlow{}
	var listErr error
	list := func(_ context.Context, ns string) ([]netns.EgressFlow, error) {
		if ns == "fc-bad" && listErr != nil {
			return nil, listErr
		}
		return sets[ns], nil
	}
	ops := wire.NewOpsMetrics("vmmd")
	l := newEgressFlowLogger(store, list, noDrops, "fsn-2", ops, nil)
	live := map[string]fcvm.LiveEgressInstance{
		"i-1":   {AppID: "app-1", AccountID: "acct-1", Netns: "fc-1"},
		"i-bad": {AppID: "app-2", AccountID: "acct-2", Netns: "fc-bad"},
	}
	t0 := time.Date(2026, 9, 24, 19, 0, 0, 0, time.UTC)
	listErr = errors.New("nft exec failed")

	sets["fc-1"] = []netns.EgressFlow{flow("198.51.100.10", 443)}
	l.tick(context.Background(), t0, live)
	sets["fc-1"] = []netns.EgressFlow{flow("198.51.100.10", 443), flow("203.0.113.9", 5432)}
	l.tick(context.Background(), t0.Add(15*time.Second), live)
	sets["fc-1"] = nil // both expired
	l.tick(context.Background(), t0.Add(11*time.Minute), live)
	sets["fc-1"] = []netns.EgressFlow{flow("198.51.100.10", 443)} // back again
	l.tick(context.Background(), t0.Add(12*time.Minute), live)

	rows, err := store.ListEgressFlows(context.Background(), state.EgressFlowFilter{From: t0, To: t0.Add(time.Hour), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want 3 (first sighting, new port, reappearance)", rows)
	}
	first := rows[len(rows)-1]
	if first.NodeName != "fsn-2" || first.AccountID != "acct-1" || first.AppID != "app-1" || first.InstanceID != "i-1" ||
		first.RemoteIP.String() != "198.51.100.10" || first.RemotePort != 443 || !first.ObservedAt.Equal(t0) {
		t.Fatalf("first row = %+v", first)
	}
	if n := testutil.ToFloat64(ops.EgressFlowLogRows()); n != 3 {
		t.Fatalf("egress_flow_log_rows_total = %v, want 3", n)
	}

	delete(live, "i-1")
	l.tick(context.Background(), t0.Add(13*time.Minute), live)
	if _, ok := l.seen["i-1"]; ok {
		t.Fatal("state kept for a gone instance")
	}
}

func noDrops(context.Context, string) ([]netns.EgressFlow, error) { return nil, nil }

// Blocked attempts from egress_blocked are logged as blocked rows with their
// protocol, once per pair while it stays in the set, independently of the
// allowed flows. A failed drop listing does not cost an instance its
// allowed flows.
func TestEgressFlowLoggerWritesBlockedAttempts(t *testing.T) {
	store := state.NewMemStore()
	flows := map[string][]netns.EgressFlow{}
	drops := map[string][]netns.EgressFlow{}
	list := func(_ context.Context, ns string) ([]netns.EgressFlow, error) { return flows[ns], nil }
	listDrops := func(_ context.Context, ns string) ([]netns.EgressFlow, error) {
		if ns == "fc-bad" {
			return nil, errors.New("nft exec failed")
		}
		return drops[ns], nil
	}
	l := newEgressFlowLogger(store, list, listDrops, "bhs-2", nil, nil)
	live := map[string]fcvm.LiveEgressInstance{
		"i-1":   {AppID: "app-1", AccountID: "acct-1", Netns: "fc-1"},
		"i-bad": {AppID: "app-2", AccountID: "acct-2", Netns: "fc-bad"},
	}
	smtp := netns.EgressFlow{Addr: netip.MustParseAddr("198.51.100.25"), Protocol: "tcp", Port: 25}
	internal := netns.EgressFlow{Addr: netip.MustParseAddr("10.0.0.5"), Protocol: "udp", Port: 161}
	t0 := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	flows["fc-1"] = []netns.EgressFlow{flow("198.51.100.10", 443)}
	flows["fc-bad"] = []netns.EgressFlow{flow("203.0.113.1", 443)}
	drops["fc-1"] = []netns.EgressFlow{smtp}
	l.tick(context.Background(), t0, live)
	drops["fc-1"] = []netns.EgressFlow{smtp, internal}
	l.tick(context.Background(), t0.Add(15*time.Second), live)

	yes, no := true, false
	blocked, err := store.ListEgressFlows(context.Background(), state.EgressFlowFilter{Blocked: &yes, From: t0, To: t0.Add(time.Hour), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked) != 2 {
		t.Fatalf("blocked rows = %+v, want smtp then internal", blocked)
	}
	if got := blocked[0]; !got.Blocked || got.Protocol != "udp" || got.RemoteIP != internal.Addr || got.RemotePort != 161 ||
		got.AccountID != "acct-1" || got.InstanceID != "i-1" || got.NodeName != "bhs-2" {
		t.Fatalf("newest blocked row = %+v", got)
	}
	if got := blocked[1]; got.Protocol != "tcp" || got.RemotePort != 25 || !got.ObservedAt.Equal(t0) {
		t.Fatalf("oldest blocked row = %+v", got)
	}
	allowed, err := store.ListEgressFlows(context.Background(), state.EgressFlowFilter{Blocked: &no, From: t0, To: t0.Add(time.Hour), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 2 || allowed[0].Blocked || allowed[0].Protocol != "tcp" {
		t.Fatalf("allowed rows = %+v, want one per instance", allowed)
	}

	delete(live, "i-1")
	l.tick(context.Background(), t0.Add(time.Minute), live)
	if _, ok := l.seenDrops["i-1"]; ok {
		t.Fatal("drop state kept for a gone instance")
	}
}

// failingFlowStore fails inserts of blocked (or allowed) rows on demand.
type failingFlowStore struct {
	*state.MemStore
	failBlocked, failAllowed bool
}

func (s *failingFlowStore) InsertEgressFlows(ctx context.Context, records []state.EgressFlowRecord) error {
	if len(records) > 0 && (records[0].Blocked && s.failBlocked || !records[0].Blocked && s.failAllowed) {
		return errors.New("statement timeout")
	}
	return s.MemStore.InsertEgressFlows(ctx, records)
}

// A failed blocked insert does not cost the allowed flows, and a failed
// insert is retried on the next poll while the pair is still in the set.
func TestEgressFlowLoggerInsertsSetsSeparatelyAndRetries(t *testing.T) {
	store := &failingFlowStore{MemStore: state.NewMemStore(), failBlocked: true}
	flows := []netns.EgressFlow{flow("198.51.100.10", 443)}
	drops := []netns.EgressFlow{{Addr: netip.MustParseAddr("198.51.100.25"), Protocol: "tcp", Port: 25}}
	list := func(context.Context, string) ([]netns.EgressFlow, error) { return flows, nil }
	listDrops := func(context.Context, string) ([]netns.EgressFlow, error) { return drops, nil }
	l := newEgressFlowLogger(store, list, listDrops, "bhs-2", nil, nil)
	live := map[string]fcvm.LiveEgressInstance{"i-1": {AccountID: "acct-1", Netns: "fc-1"}}
	t0 := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	window := state.EgressFlowFilter{From: t0, To: t0.Add(time.Hour), Limit: 100}

	l.tick(context.Background(), t0, live)
	rows, _ := store.ListEgressFlows(context.Background(), window)
	if len(rows) != 1 || rows[0].Blocked {
		t.Fatalf("rows after failed blocked insert = %+v, want the allowed flow only", rows)
	}

	store.failBlocked = false
	l.tick(context.Background(), t0.Add(15*time.Second), live)
	l.tick(context.Background(), t0.Add(30*time.Second), live)
	rows, _ = store.ListEgressFlows(context.Background(), window)
	if len(rows) != 2 || !rows[0].Blocked || rows[1].Blocked {
		t.Fatalf("rows after retry = %+v, want the blocked pair once and the allowed flow once", rows)
	}
}

// One poll logs at most egressBlockedRowsPerInstance blocked pairs per
// instance; the rest follow on later polls while they stay in the set.
func TestEgressFlowLoggerCapsBlockedRowsPerInstance(t *testing.T) {
	store := state.NewMemStore()
	var drops []netns.EgressFlow
	for i := 0; i < egressBlockedRowsPerInstance+10; i++ {
		drops = append(drops, netns.EgressFlow{Addr: netip.MustParseAddr("10.0.0.5"), Protocol: "tcp", Port: uint16(1000 + i)})
	}
	listDrops := func(context.Context, string) ([]netns.EgressFlow, error) { return drops, nil }
	l := newEgressFlowLogger(store, noDrops, listDrops, "bhs-2", nil, nil)
	live := map[string]fcvm.LiveEgressInstance{"i-1": {AccountID: "acct-1", Netns: "fc-1"}}
	t0 := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	window := state.EgressFlowFilter{From: t0, To: t0.Add(time.Hour), Limit: 1000}

	l.tick(context.Background(), t0, live)
	if rows, _ := store.ListEgressFlows(context.Background(), window); len(rows) != egressBlockedRowsPerInstance {
		t.Fatalf("first poll rows = %d, want %d", len(rows), egressBlockedRowsPerInstance)
	}
	l.tick(context.Background(), t0.Add(15*time.Second), live)
	if rows, _ := store.ListEgressFlows(context.Background(), window); len(rows) != len(drops) {
		t.Fatalf("rows after second poll = %d, want %d", len(rows), len(drops))
	}
}
