package meter

import (
	"context"
	"testing"
	"time"
)

// internalEgressSource returns vmmd-style cumulative network counters with an
// internal-egress reading (billing review R8-2).
type internalEgressSource struct {
	instance        string
	netTx, internal uint64
	internalValid   bool
}

func (c *internalEgressSource) ReadUsageDeltas(instanceID string) (UsageDeltas, bool) {
	if instanceID != c.instance {
		return UsageDeltas{}, false
	}
	return UsageDeltas{
		NetTXBytes: c.netTx, NetTXValid: true, NetworkCumulative: true,
		InternalTXBytes: c.internal, InternalTXValid: c.internalValid,
	}, true
}

func (c *internalEgressSource) CumulativeNetworkCounters() bool { return true }

// R8-2: with internal_egress_unbilled on, billed net_tx_bytes leaves out the
// bytes the instance sent to internal destinations; off, or without a vmmd
// reading, it bills every veth byte as before.
func TestSamplerInternalEgressUnbilled(t *testing.T) {
	cases := []struct {
		name          string
		on            bool
		internalValid bool
		want          int64
	}{
		{name: "on", on: true, internalValid: true, want: 6_000},
		{name: "off", on: false, internalValid: true, want: 10_000},
		{name: "no vmmd reading", on: true, internalValid: false, want: 10_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
			egress := &internalEgressSource{instance: instID, netTx: 10_000, internal: 4_000, internalValid: tc.internalValid}
			sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute.Add(time.Minute) }).
				WithInternalEgressUnbilled(tc.on)
			rows, err := sampler.SampleAndRoll(context.Background())
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			if len(rows) != 1 || rows[0].NetTxBytes != tc.want {
				t.Fatalf("rolled rows = %+v, want one with NetTxBytes %d", rows, tc.want)
			}
			usage, err := store.UsageByMonth(context.Background(), acctID, minute)
			if err != nil || len(usage) != 1 || usage[0].NetTxBytes != tc.want {
				t.Fatalf("monthly usage = (%+v, %v), want net_tx_bytes %d", usage, err, tc.want)
			}
		})
	}
}
