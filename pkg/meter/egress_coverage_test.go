package meter

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cumulativeEgress is a network source with vmmd's cumulative, checkpointed
// counters and no traffic this tick.
type cumulativeEgress struct{}

func (cumulativeEgress) ReadUsageDeltas(string) (UsageDeltas, bool) { return UsageDeltas{}, false }
func (cumulativeEgress) CumulativeNetworkCounters() bool            { return true }

// Nothing recorded a minute egress-complete, so every egress budget lacked
// coverage and strict egress could never activate. With cumulative network
// counters a fully rolled minute, caught up or live, is egress-complete too;
// without them it stays compute-only.
// adr: 20261101113742
func TestSampler_RecordsEgressCoverageWithCumulativeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		egress EgressSource
		want   int64
	}{
		{name: "no network source", egress: nil, want: 0},
		{name: "cumulative counters", egress: cumulativeEgress{}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newLedgerStore()
			base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
			seedRunningInstance(t, store, base, base.Add(3*time.Minute))
			recordComplete(t, store, base.Add(-api.MeterCatchUpWindow), base)
			now := base.Add(2 * time.Minute) // 09:00 is caught up, 09:01 is the newest closed minute
			sampler := NewSamplerWithEgress(store, nil, tc.egress, func() time.Time { return now })
			if _, err := tick(t, store, sampler, now); err != nil {
				t.Fatalf("tick: %v", err)
			}
			coverage, err := store.FinancialSamplingCoverage(context.Background(), base, base.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if coverage.ComputeMinutes != 2 || coverage.EgressMinutes != tc.want {
				t.Fatalf("coverage = %d compute, %d egress minutes; want 2 and %d", coverage.ComputeMinutes, coverage.EgressMinutes, tc.want)
			}
		})
	}
}
