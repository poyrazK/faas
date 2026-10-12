package instancestats

import (
	"math"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// NodeMemoryShadow compares, for one node, what admission books today with
// what admission by measured memory would book (ADR-909, proposed). It is
// reported only; admission is unchanged.
type NodeMemoryShadow struct {
	ReservedMB int
	MeasuredMB int
}

// memoryShadow sums both bookings over the instances that count toward a
// node's RAM (invariant 2). An instance without an RSS sample books its full
// reservation in both columns.
func memoryShadow(instances []state.Instance, rows []InstanceStat) map[string]NodeMemoryShadow {
	rss := make(map[string]float64, len(rows))
	for _, row := range rows {
		rss[row.InstanceID] = row.RSSMB
	}
	out := make(map[string]NodeMemoryShadow)
	for _, in := range instances {
		if in.NodeID == "" || !state.State(in.State).CountsForRAM() {
			continue
		}
		sample, ok := rss[in.ID]
		if !ok {
			sample = math.NaN()
		}
		s := out[in.NodeID]
		s.ReservedMB += api.BillableRAMMB(in.RAMMB)
		s.MeasuredMB += api.MeasuredChargeMB(in.RAMMB, sample)
		out[in.NodeID] = s
	}
	return out
}

func (p *Poller) publishMemoryShadow(instances []state.Instance, rows []InstanceStat) {
	if p.Metrics == nil {
		return
	}
	shadow := memoryShadow(instances, rows)
	byNode := make(map[string][2]int, len(shadow))
	for node, s := range shadow {
		byNode[node] = [2]int{s.ReservedMB, s.MeasuredMB}
	}
	p.Metrics.ReplaceNodeMemoryShadow(byNode)
}
