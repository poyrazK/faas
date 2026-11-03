package instancestats

// Measured CPU admission (ADR 20261029151111). schedd charges a running
// instance its measured host CPU instead of its CPU booking. An instance's
// measurement is the larger of its latest CPU and the p95 of its per-bucket
// peaks over the last api.MeasuredChargeWindowSeconds, one bucket per
// api.MeasuredChargeSampleSeconds, so a request burst between two samples
// still counts. An instance is measured only once its history spans
// api.MeasuredCPUMinHistorySeconds; until then admission charges its booking.

import (
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cpuHistory keeps each instance's recent CPU peaks. Only the poller's tick
// goroutine uses it.
type cpuHistory struct {
	byInstance map[string]*cpuSamples
}

type cpuSamples struct {
	since time.Time
	peaks []cpuPeak
}

// cpuPeak is the highest CPU percent seen in the bucket starting at at.
type cpuPeak struct {
	at  time.Time
	pct float64
}

func newCPUHistory() *cpuHistory {
	return &cpuHistory{byInstance: map[string]*cpuSamples{}}
}

// chargeSample records pct for instance at now and returns the percent the
// measurement uses, and whether the history is long enough to use it.
func (h *cpuHistory) chargeSample(instance string, pct float64, now time.Time) (float64, bool) {
	window := time.Duration(api.MeasuredChargeWindowSeconds) * time.Second
	every := time.Duration(api.MeasuredChargeSampleSeconds) * time.Second
	s := h.byInstance[instance]
	if s == nil {
		s = &cpuSamples{since: now}
		h.byInstance[instance] = s
	}
	keep := 0
	for keep < len(s.peaks) && now.Sub(s.peaks[keep].at) > window {
		keep++
	}
	s.peaks = s.peaks[keep:]
	if n := len(s.peaks); n == 0 || now.Sub(s.peaks[n-1].at) >= every {
		s.peaks = append(s.peaks, cpuPeak{at: now, pct: pct})
	} else {
		s.peaks[n-1].pct = math.Max(s.peaks[n-1].pct, pct)
	}
	values := make([]float64, len(s.peaks))
	for i, p := range s.peaks {
		values[i] = p.pct
	}
	sort.Float64s(values)
	p := values[int(math.Ceil(float64(len(values))*api.MeasuredChargePercentile/100))-1]
	measured := now.Sub(s.since) >= time.Duration(api.MeasuredCPUMinHistorySeconds)*time.Second
	return math.Max(pct, p), measured
}

// retain drops the history of instances no longer listed.
func (h *cpuHistory) retain(live map[string]struct{}) {
	for id := range h.byInstance {
		if _, ok := live[id]; !ok {
			delete(h.byInstance, id)
		}
	}
}

// measuredCPUSnapshot is each node's measured instances in one tick.
type measuredCPUSnapshot struct {
	byNode map[string]measuredCPUNode
	asOf   time.Time
}

// measuredCPUNode is one node's measured instances, in host CPU millicores,
// and when vmmd sampled them (the oldest row's sample time).
type measuredCPUNode struct {
	millicores map[string]int
	sampledAt  time.Time
}

// publishMeasuredCPU records each instance's measured CPU for schedd's
// measured CPU admission, the node gauge and the per-instance histogram. A
// row without a sample time counts as sampled at asOf; a row without a CPU
// reading keeps its history but is not measured this tick.
func (p *Poller) publishMeasuredCPU(rows []InstanceStat, asOf time.Time) {
	if p.cpuHistory == nil {
		p.cpuHistory = newCPUHistory()
	}
	byNode := make(map[string]measuredCPUNode)
	live := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.NodeID == "" || row.InstanceID == "" {
			continue
		}
		live[row.InstanceID] = struct{}{}
		sampled := row.SampledAt
		if sampled.IsZero() {
			sampled = asOf
		}
		node, seen := byNode[row.NodeID]
		if !seen {
			node.millicores = make(map[string]int)
		}
		if !seen || sampled.Before(node.sampledAt) {
			node.sampledAt = sampled
		}
		if row.CPU == Valid && row.CPUPct >= 0 {
			// CPUPct is percent of one core: 100 is 1000 millicores.
			if pct, measured := p.cpuHistory.chargeSample(row.InstanceID, row.CPUPct, asOf); measured {
				node.millicores[row.InstanceID] = int(math.Ceil(pct * 10))
			}
		}
		byNode[row.NodeID] = node
	}
	p.cpuHistory.retain(live)
	if p.Reader != nil {
		p.Reader.cpu.Store(&measuredCPUSnapshot{byNode: byNode, asOf: asOf})
	}
	if p.Metrics == nil {
		return
	}
	measured := make(map[string]map[string]int, len(byNode))
	for nodeID, node := range byNode {
		measured[nodeID] = node.millicores
	}
	p.Metrics.ReplaceNodeCPUMeasured(measured)
}

// MeasuredCPU returns the measured host CPU millicores of each instance a
// node reported with enough history, and when vmmd sampled them, so
// admission never applies a measurement to a booking made after it. ok is
// false when no tick has run, or the sample is older than
// MeasuredAdmissionSampleMaxAgeMillis; admission then charges every booking.
// A node that reported nothing this tick returns an empty map with ok true.
func (r *Reader) MeasuredCPU(nodeID string, now time.Time) (map[string]int, time.Time, bool) {
	maxAge := time.Duration(api.MeasuredAdmissionSampleMaxAgeMillis) * time.Millisecond
	snap := r.cpu.Load()
	if snap == nil || now.Sub(snap.asOf) > maxAge {
		return nil, time.Time{}, false
	}
	node, reported := snap.byNode[nodeID]
	if !reported {
		return nil, snap.asOf, true
	}
	if now.Sub(node.sampledAt) > maxAge {
		return nil, time.Time{}, false
	}
	return node.millicores, node.sampledAt, true
}

// MeasuredCPUAcross is measured CPU admission's source on a multi-node
// fleet, read the way QuietPausedAcross reads quiet pauses: the schedd's
// own node from the local reader, peer nodes from the owner fleet reader.
func MeasuredCPUAcross(local, fleet *Reader, ownerNodeID string) func(nodeID string, now time.Time) (map[string]int, time.Time, bool) {
	return func(nodeID string, now time.Time) (map[string]int, time.Time, bool) {
		if ownerNodeID == "" || nodeID == ownerNodeID || fleet == nil {
			return local.MeasuredCPU(nodeID, now)
		}
		return fleet.MeasuredCPU(nodeID, now)
	}
}
