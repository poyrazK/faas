package meter

import (
	"context"
	"testing"
	"time"
)

// gatewayEgressSource returns vmmd-style cumulative network counters plus
// gateway response bytes drained from the in-memory stream.
type gatewayEgressSource struct {
	instance        string
	netTx, gateway  uint64
	networkCounters bool
}

func (c *gatewayEgressSource) ReadUsageDeltas(instanceID string) (UsageDeltas, bool) {
	if instanceID != c.instance {
		return UsageDeltas{}, false
	}
	return UsageDeltas{
		TXBytes: c.gateway, Requests: 1,
		NetTXBytes: c.netTx, NetTXValid: c.networkCounters, NetworkCumulative: c.networkCounters,
	}, true
}

// ADR-20261121210517: with gateway_response_egress on, the response bytes the
// gateway forwarded count as billed egress next to the veth bytes; off, they
// stay diagnostic tx_bytes. Both the cumulative-counter and the direct-delta
// shapes add them once.
func TestSamplerGatewayResponseEgress(t *testing.T) {
	cases := []struct {
		name            string
		on, cumulative  bool
		wantNet, wantTx int64
	}{
		{name: "on cumulative", on: true, cumulative: true, wantNet: 10_000 + 2_500, wantTx: 2_500},
		{name: "off cumulative", on: false, cumulative: true, wantNet: 10_000, wantTx: 2_500},
		{name: "on without network counters", on: true, cumulative: false, wantNet: 10_000 + 2_500, wantTx: 2_500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, acctID, _, instID, minute := seedMinuteUsageEgress(t)
			egress := &gatewayEgressSource{instance: instID, netTx: 10_000, gateway: 2_500, networkCounters: tc.cumulative}
			sampler := NewSamplerWithEgress(store, nil, egress, func() time.Time { return minute.Add(time.Minute) }).
				WithGatewayResponseEgress(tc.on)
			rows, err := sampler.SampleAndRoll(context.Background())
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			if len(rows) != 1 || rows[0].NetTxBytes != tc.wantNet || rows[0].TXBytes != tc.wantTx {
				t.Fatalf("rolled rows = %+v, want net_tx %d tx %d", rows, tc.wantNet, tc.wantTx)
			}
			usage, err := store.UsageByMonth(context.Background(), acctID, minute)
			if err != nil || len(usage) != 1 || usage[0].NetTxBytes != tc.wantNet || usage[0].TXBytes != tc.wantTx {
				t.Fatalf("monthly usage = (%+v, %v), want net_tx %d tx %d", usage, err, tc.wantNet, tc.wantTx)
			}
		})
	}
}
