// adr: 1131
package gateway

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

type edgeVerdict struct{ outcome, caller, target string }

// A wake-ahead verdict belongs to the caller whose prediction started it, even
// when a different caller is the one that then calls the target.
func TestWakeAheadEdgeOutcomeAttributesToPredictingCaller(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewWakeAheadLearner(func() time.Time { return now }, nil)
	var mu sync.Mutex
	var got []edgeVerdict
	l.SetEdgeOutcome(func(outcome, caller, target string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, edgeVerdict{outcome, caller, target})
	})

	if !l.ClaimSpeculationFor("api", "auth") || !l.ClaimSpeculationFor("api", "billing") {
		t.Fatal("claims refused")
	}
	l.ObserveCall("worker", "auth", false) // auth was used while fresh
	now = now.Add(api.ServiceWakeAheadFollowWindow + time.Second)
	l.ObserveWake("api") // sweeps the expired, unused billing claim

	mu.Lock()
	defer mu.Unlock()
	want := []edgeVerdict{{WakeAheadUsed, "api", "auth"}, {WakeAheadUnused, "api", "billing"}}
	if len(got) != len(want) {
		t.Fatalf("verdicts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("verdict %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Claims made without a caller (the pre-ADR-1131 API) produce no edge verdict.
func TestWakeAheadEdgeOutcomeSkipsUnattributedClaims(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewWakeAheadLearner(func() time.Time { return now }, nil)
	calls := 0
	l.SetEdgeOutcome(func(string, string, string) { calls++ })
	l.ClaimSpeculation("auth")
	l.ObserveCall("worker", "auth", false)
	if calls != 0 {
		t.Fatalf("edge verdicts = %d, want 0 for an unattributed claim", calls)
	}
}

func TestIncServiceWakeAheadEdgeLabels(t *testing.T) {
	m := NewMetrics()
	caller, target := uuid.NewString(), uuid.NewString()
	m.IncServiceWakeAheadEdge(WakeAheadUsed, caller, target)
	m.IncServiceWakeAheadEdge(WakeAheadUsed, "not-a-uuid", target)
	m.IncServiceWakeAheadEdge(WakeAheadSkippedResidency, caller, target) // not an edge outcome
	if got := testutil.ToFloat64(m.serviceWakeAheadEdges.WithLabelValues(caller, target, WakeAheadUsed)); got != 1 {
		t.Fatalf("used = %g, want 1", got)
	}
	if got := testutil.CollectAndCount(m.serviceWakeAheadEdges); got != 1 {
		t.Fatalf("series = %d, want only the valid edge", got)
	}
}
