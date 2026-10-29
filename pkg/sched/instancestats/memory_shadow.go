package instancestats

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// NodeMemoryShadow compares, for one node, what admission books today with
// what admission by measured memory books (ADR-909). Admission uses
// MeasuredMB only when schedd enables measured admission.
type NodeMemoryShadow struct {
	ReservedMB int
	MeasuredMB int
}

// memoryShadow sums both bookings over the instances that count toward a
// node's RAM (invariant 2). An instance without an RSS sample books its full
// reservation in both columns. bridgeMB is each node's instance bridge
// memory (connection-survival ADR §2, the ADR-909 amendment): measured
// admission charges it on top; it reserves nothing.
func memoryShadow(instances []state.Instance, rows []InstanceStat, bridgeMB map[string]int) map[string]NodeMemoryShadow {
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
	for node, mb := range bridgeMB {
		if node == "" || mb <= 0 {
			continue
		}
		s := out[node]
		s.MeasuredMB += mb
		out[node] = s
	}
	return out
}

// publishMemoryShadow records the shadow for admission and metrics. asOf is
// when the tick started, before it listed the instances.
func (p *Poller) publishMemoryShadow(instances []state.Instance, rows []InstanceStat, asOf time.Time) {
	p.publishMemoryShadowWithBridges(instances, rows, nil, asOf)
}

// publishMemoryShadowWithBridges is publishMemoryShadow with each node's
// instance bridge memory.
func (p *Poller) publishMemoryShadowWithBridges(instances []state.Instance, rows []InstanceStat, bridgeMB map[string]int, asOf time.Time) {
	if p.rssHistory == nil {
		p.rssHistory = newRSSHistory()
	}
	charged := make([]InstanceStat, len(rows))
	live := make(map[string]struct{}, len(rows))
	for i, row := range rows {
		if row.RSSMB >= 0 { // false for NaN, an unmeasured instance
			row.RSSMB = p.rssHistory.chargeSample(row.InstanceID, row.RSSMB, asOf)
		}
		charged[i] = row
		live[row.InstanceID] = struct{}{}
	}
	p.rssHistory.retain(live)
	shadow := memoryShadow(instances, charged, bridgeMB)
	if p.Reader != nil {
		p.Reader.replaceMemoryShadow(shadow, asOf)
	}
	if p.Metrics == nil {
		return
	}
	byNode := make(map[string][2]int, len(shadow))
	for node, s := range shadow {
		byNode[node] = [2]int{s.ReservedMB, s.MeasuredMB}
	}
	p.Metrics.ReplaceNodeMemoryShadow(byNode)
}

type memoryShadowSnapshot struct {
	byNode map[string]NodeMemoryShadow
	asOf   time.Time
}

func (r *Reader) replaceMemoryShadow(byNode map[string]NodeMemoryShadow, asOf time.Time) {
	r.shadow.Store(&memoryShadowSnapshot{byNode: byNode, asOf: asOf})
}

// MeasuredMB is the node's ADR-909 measured charge from the latest tick and
// the time that tick read the instances. ok is false when the node was not
// measured or the measurement is older than MeasuredAdmissionSampleMaxAgeMillis.
func (r *Reader) MeasuredMB(nodeID string, now time.Time) (int, time.Time, bool) {
	snap := r.shadow.Load()
	if snap == nil || now.Sub(snap.asOf) > time.Duration(api.MeasuredAdmissionSampleMaxAgeMillis)*time.Millisecond {
		return 0, time.Time{}, false
	}
	s, ok := snap.byNode[nodeID]
	return s.MeasuredMB, snap.asOf, ok
}
