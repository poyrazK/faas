package sched

// CPU pressure backstop (ADR 20261030100950).
//
// Quiet pause and measured CPU admission let a node's bookings reach several
// times its CPU budgets, and a resume or a burst never waits for admission.
// When a node's tenant tasks wait for CPU while its CPUs are nearly all busy
// (NodeCPUPressure.Pressured), the schedd that receives the node's readings
// holds compute_nodes.cpu_pressure_until ahead of now. Every schedd reads the
// column from its node registry: placement takes the node only when nothing
// else fits, and the ledger stops discounting its bookings, so the vCPU and
// CPU gates count every booking there until the hold expires.

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// CPUPressureReadings returns the CPU pressure readings of the nodes whose
// readings this schedd receives: its own node's capacity stream, or vmmd
// Stats on a single box.
type CPUPressureReadings func(now time.Time) map[string]NodeCPUPressure

// cpuPressureHolds remembers the hold this schedd last wrote for each node,
// so it rewrites one only once less than half of it remains.
type cpuPressureHolds struct {
	mu   sync.Mutex
	held map[string]time.Time
}

// EnableCPUPressureBackstop makes this schedd hold the nodes whose readings
// it receives under CPU pressure while the readings say so. Placement and
// admission honour a hold whether or not this is called.
func (e *Engine) EnableCPUPressureBackstop(src CPUPressureReadings) {
	if e == nil {
		return
	}
	e.cpuPressureReadings = src
	e.cpuPressureHolds = &cpuPressureHolds{held: map[string]time.Time{}}
}

// ObserveCPUPressure holds every node whose readings put it under CPU
// pressure for api.NodeCPUPressureHoldSeconds, rewriting the hold once less
// than half of it remains: one write, and one compute_node_changed, every
// 15 s while the pressure lasts. Nothing clears a hold; it expires. A node
// whose readings stop is never held. Runs on the watchdog tick.
func (e *Engine) ObserveCPUPressure(ctx context.Context, now time.Time) {
	if e == nil || e.cpuPressureReadings == nil || e.cpuPressureHolds == nil {
		return
	}
	writer, ok := e.store.(state.ComputeNodeCPUPressureStore)
	if !ok {
		return
	}
	hold := time.Duration(api.NodeCPUPressureHoldSeconds) * time.Second
	for nodeID, reading := range e.cpuPressureReadings(now) {
		if !reading.Pressured() {
			continue
		}
		e.cpuPressureHolds.mu.Lock()
		held := e.cpuPressureHolds.held[nodeID]
		e.cpuPressureHolds.mu.Unlock()
		if held.Sub(now) > hold/2 {
			continue
		}
		until := now.Add(hold)
		if err := writer.HoldComputeNodeCPUPressure(ctx, nodeID, until); err != nil {
			e.log.Warn("sched: hold node under CPU pressure", "node_id", nodeID, "err", err)
			continue
		}
		e.cpuPressureHolds.mu.Lock()
		e.cpuPressureHolds.held[nodeID] = until
		e.cpuPressureHolds.mu.Unlock()
		if !held.After(now) {
			e.ops.NodeCPUPressureHeld(nodeID)
			e.log.Warn("sched: node under CPU pressure; placement prefers other nodes and admission counts its bookings in full",
				"node_id", nodeID, "some_avg10", reading.SomeAvg10, "busy_percent", reading.BusyPercent,
				"hold_s", api.NodeCPUPressureHoldSeconds)
		}
	}
}

// nodeCPUPressured reports whether reg holds nodeID under CPU pressure at
// now. A node the registry does not know is not held.
func nodeCPUPressured(reg *NodeRegistry, nodeID string, now time.Time) bool {
	if reg == nil {
		return false
	}
	n, ok := reg.Node(nodeID)
	return ok && n.CPUPressured(now)
}
