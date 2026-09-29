// adr: 369 — egress flow log capture.
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
	l := newEgressFlowLogger(store, list, "fsn-2", ops, nil)
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
