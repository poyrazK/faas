// adr: 20261122090000
package meter

import (
	"context"
	"testing"
	"time"
)

// publicResponseSource reports gateway response bytes beside vmmd's
// cumulative veth counter.
type publicResponseSource struct {
	instance    string
	netTx, gwTx uint64
	cumulative  bool
}

func (c *publicResponseSource) ReadUsageDeltas(instanceID string) (UsageDeltas, bool) {
	if instanceID != c.instance {
		return UsageDeltas{}, false
	}
	return UsageDeltas{TXBytes: c.gwTx, NetTXBytes: c.netTx, NetTXValid: true, NetworkCumulative: c.cumulative}, true
}

func (c *publicResponseSource) CumulativeNetworkCounters() bool { return c.cumulative }

// Public responses reach the guest over tap0 inside its netns, so the veth
// counter never sees them: with public response egress on, billed egress is
// the guest's own outbound bytes plus the gateway's response bytes.
func TestSamplerPublicResponseEgress(t *testing.T) {
	for _, tc := range []struct {
		name       string
		on         bool
		cumulative bool
		want       int64
	}{
		{name: "on, cumulative veth", on: true, cumulative: true, want: 10_000 + 2_500},
		{name: "on, delta veth", on: true, cumulative: false, want: 10_000 + 2_500},
		{name: "off", on: false, cumulative: true, want: 10_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
			egress := &publicResponseSource{instance: instID, netTx: 10_000, gwTx: 2_500, cumulative: tc.cumulative}
			sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute.Add(time.Minute) }).
				WithPublicResponseEgress(tc.on)
			rows, err := sampler.SampleAndRoll(context.Background())
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			if len(rows) != 1 || rows[0].NetTxBytes != tc.want || rows[0].TXBytes != 2_500 {
				t.Fatalf("rolled rows = %+v, want one with NetTxBytes %d and TXBytes 2500", rows, tc.want)
			}
			usage, err := store.UsageByMonth(context.Background(), acctID, minute)
			if err != nil || len(usage) != 1 || usage[0].NetTxBytes != tc.want {
				t.Fatalf("monthly usage = (%+v, %v), want net_tx_bytes %d", usage, err, tc.want)
			}
		})
	}
}
