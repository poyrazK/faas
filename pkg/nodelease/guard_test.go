// adr: 1344 — node lease takeover guard (§4, P3 phase 2).
package nodelease

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeLeases struct {
	lease state.NodeLease
	err   error
}

func (f fakeLeases) GetNodeLease(context.Context, string) (state.NodeLease, error) {
	return f.lease, f.err
}

func TestGuardVetoesTakeoverOfALiveLease(t *testing.T) {
	now := time.Unix(10_000, 0)
	for _, tc := range []struct {
		name   string
		leases fakeLeases
		want   bool
		reason string
	}{
		{name: "no lease row: heartbeat rules decide", leases: fakeLeases{err: state.ErrNotFound}, want: true},
		{name: "expired past the margin", leases: fakeLeases{lease: state.NodeLease{ExpiresAt: now.Add(-api.NodeLeaseTakeoverMargin), DBNow: now}}, want: true},
		{name: "expired inside the margin", leases: fakeLeases{lease: state.NodeLease{ExpiresAt: now.Add(-time.Second), DBNow: now}}, reason: "lease_live"},
		{name: "live", leases: fakeLeases{lease: state.NodeLease{ExpiresAt: now.Add(20 * time.Second), DBNow: now}}, reason: "lease_live"},
		{name: "unreadable", leases: fakeLeases{err: errors.New("connection reset")}, reason: "unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, err := NewGuard(tc.leases, quiet())
			if err != nil {
				t.Fatal(err)
			}
			g.RegisterMetrics(prometheus.NewRegistry(), "schedd")
			if got := g.MayTakeOver(context.Background(), "peer_observer", "node-1"); got != tc.want {
				t.Fatalf("MayTakeOver = %v, want %v", got, tc.want)
			}
			if tc.reason != "" {
				if got := testutil.ToFloat64(g.refusals.WithLabelValues("peer_observer", tc.reason)); got != 1 {
					t.Fatalf("refusals{reason=%s} = %v, want 1", tc.reason, got)
				}
			}
		})
	}
	var off *Guard
	if !off.MayTakeOver(context.Background(), "peer_observer", "node-1") {
		t.Fatal("a nil (disabled) guard refused a takeover")
	}
}
