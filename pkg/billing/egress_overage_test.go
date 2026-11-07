package billing

// adr: 20261104204500

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Only the overage growth in hours the election covers counts, in hour order
// across apps, whatever order the rows come in.
func TestElectedEgressOverage(t *testing.T) {
	const gib = int64(1 << 30)
	h := func(n int) time.Time { return time.Date(2026, 11, 1, n, 0, 0, 0, time.UTC) }
	rows := []state.Usage{
		{AppID: "b", Month: h(3), NetTxBytes: 2 * gib},
		{AppID: "a", Month: h(1), NetTxBytes: gib},
		{AppID: "a", Month: h(2), NetTxBytes: 3 * gib},
		{AppID: "b", Month: h(2), NetTxBytes: gib},
		{AppID: "a", Month: h(4), NetTxBytes: 5 * gib},
	}
	start, end := h(2), h(4)
	for _, tc := range []struct {
		name     string
		election state.EgressOverageElection
		want     int64
	}{
		{"never elected", state.EgressOverageElection{}, 0},
		// Hours 2 and 3: the running total goes 1 → 5 → 7 GiB against 2 GiB
		// included, so hour 2 adds 3 GiB and hour 3 adds 2 GiB.
		{"covers hours 2-3", state.EgressOverageElection{EnabledAt: &start, DisabledAt: &end}, 5 * gib},
		{"on from hour 2", state.EgressOverageElection{EnabledAt: &start}, 10 * gib},
		{"on from hour 4", state.EgressOverageElection{EnabledAt: &end}, 5 * gib},
	} {
		if got := ElectedEgressOverage(rows, 2*gib, tc.election); got != tc.want {
			t.Errorf("%s: elected overage = %d GiB, want %d GiB", tc.name, got/gib, tc.want/gib)
		}
	}
}
