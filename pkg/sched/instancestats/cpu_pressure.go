package instancestats

// CPU pressure backstop (ADR 20261030100950). The poller publishes the CPU
// pressure readings of the nodes whose readings this schedd receives (its
// own node's capacity stream, or vmmd Stats on a single box) for the
// engine, which holds a pressured node in compute_nodes, and for the
// readings' gauges. The owner fleet poller does not publish them: each node's
// own schedd decides for it.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// cpuPressureSnapshot is each node's CPU pressure readings in one tick.
type cpuPressureSnapshot struct {
	byNode map[string]sched.NodeCPUPressure
	asOf   time.Time
}

// publishCPUPressure records each node's CPU pressure readings for the
// engine and the gauges.
func (p *Poller) publishCPUPressure(byNode map[string]sched.NodeCPUPressure, asOf time.Time) {
	if p.Reader != nil {
		p.Reader.pressure.Store(&cpuPressureSnapshot{byNode: byNode, asOf: asOf})
	}
	if p.Metrics == nil {
		return
	}
	readings := make(map[string]wire.NodeCPUPressure, len(byNode))
	for nodeID, r := range byNode {
		readings[nodeID] = wire.NodeCPUPressure{SomeAvg10: r.SomeAvg10, BusyPercent: r.BusyPercent}
	}
	p.Metrics.ReplaceNodeCPUPressure(readings)
}

// snapshotCPUPressure adds a Stats snapshot's CPU pressure readings to
// byNode when vmmd reported them.
func snapshotCPUPressure(byNode map[string]sched.NodeCPUPressure, nodeID string, snap *sched.StatsSnapshot) {
	if snap != nil && snap.CPUPressure != nil {
		byNode[nodeID] = *snap.CPUPressure
	}
}

// NodeCPUPressure returns the CPU pressure readings of every node the latest
// tick saw, or nil when that tick is older than sched.TelemetryFreshness.
func (r *Reader) NodeCPUPressure(now time.Time) map[string]sched.NodeCPUPressure {
	snap := r.pressure.Load()
	if snap == nil || now.Sub(snap.asOf) > sched.TelemetryFreshness {
		return nil
	}
	return snap.byNode
}
