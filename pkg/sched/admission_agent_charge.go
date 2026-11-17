package sched

// Measured charge of agent run VMs on agent nodes (ADR-20261027090015 §3,
// schedd agent_measured_charge, needs measured admission). Agent run VMs
// have no instances row, so the node measurement never includes them; the
// ledger adds each one:
//
//   - Charge. A run counts against the measured ceiling at
//     api.AgentChargeMB: the larger of its latest memory sample and its
//     version's p95 private peak, at least api.AgentChargeFloorMB, plus
//     8 MB, capped at ram_mb + 8. A run the latest sample cannot have seen
//     is charged its peak from its first second.
//   - Reservation. It books api.AgentReservationMB + 8 against the ADR-909
//     overcommit bound, Σ(reservation + 8) ≤ 2 × ceiling.
//   - Without a fresh measurement the node falls back to reservations, and
//     every agent run counts at ram_mb + 8 again (agentDiscountMB).
//
// A version without history (Request.AgentPeakMB 0) books and is charged
// ram_mb + 8, the plan rule. memory.max stays ram_mb + 8: only admission
// changes. The thaw headroom waits for compress-on-wait (step 5), the only
// way a run's memory falls below its charge.

import (
	"fmt"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// AgentMemory reports the memory (MB, memory.current) of each VM on a node
// that the latest instance stats sample read, and when that sample was
// taken. ok is false when no fresh sample exists.
type AgentMemory func(nodeID string) (mb map[string]float64, asOf time.Time, ok bool)

// EnableAgentMeasuredCharge charges agent run VMs their measured memory
// (ADR-20261027090015 §3). It applies only while measured admission is on.
func (l *NodeLedger) EnableAgentMeasuredCharge(src AgentMemory) {
	l.mu.Lock()
	l.agentMemory = src
	l.mu.Unlock()
}

func (l *NodeLedger) agentChargeOn_locked() bool { return l.measured != nil && l.agentMemory != nil }

// agentBooking_locked is what a request books in residentRAM, and what an
// agent run books below ram_mb + 8.
func (l *NodeLedger) agentBooking_locked(r Request) (book, discount int) {
	full := r.admissionMB()
	if r.Kind != KindAgentRun || !l.agentChargeOn_locked() {
		return full, 0
	}
	book = min(api.BillableRAMMB(api.AgentReservationMB(r.RAMMB, r.AgentPeakMB)), full)
	return book, full - book
}

// agentSamples is one read of a node's agent VM memory for measuredUsed_locked.
type agentSamples struct {
	on   bool
	mb   map[string]float64
	asOf time.Time
	ok   bool
}

func (l *NodeLedger) agentSamples_locked(nodeID string) agentSamples {
	if !l.agentChargeOn_locked() {
		return agentSamples{}
	}
	mb, asOf, ok := l.agentMemory(nodeID)
	return agentSamples{on: true, mb: mb, asOf: asOf, ok: ok}
}

// charge is what an agent run counts in the node's measured figure: its
// reservation when measured charge is off, its measured charge when on.
func (s agentSamples) charge(instance string, e *reservation) int {
	if !s.on {
		return e.admissionMB
	}
	sample := math.NaN()
	if mb, seen := s.mb[instance]; s.ok && seen && !e.admittedAt.After(s.asOf) {
		sample = mb
	}
	return api.AgentChargeMB(e.agentRAMMB, e.agentPeakMB, sample)
}

// agentRAMHeadroom_locked admits an agent run under measured charge. Its
// charge must fit under the ceiling with the node's measured figure, and
// its reservation within the overcommit bound.
func (l *NodeLedger) agentRAMHeadroom_locked(r Request, node *nodeReservation, ceiling int) error {
	measured, _ := l.measuredUsed_locked(r.NodeID)
	charge := api.AgentChargeMB(r.RAMMB, r.AgentPeakMB, math.NaN())
	if measured+charge > ceiling {
		return explainAdmission(api.ErrCapacity(fmt.Sprintf(
			"RAM headroom: node %q measured %d MB + %d MB charged exceeds the %d MB per-node admission ceiling",
			r.NodeID, measured, charge, ceiling)), resourceBlocker("node_ram", "ram_mb", int64(charge)))
	}
	book, _ := l.agentBooking_locked(r)
	bound := ceiling * api.MeasuredAdmissionMaxOvercommitPercent / 100
	if reserved := node.residentRAM - node.frozenRAM; reserved+book > bound {
		return explainAdmission(api.ErrCapacity(fmt.Sprintf(
			"RAM headroom: node %q reservations %d MB + %d MB booked exceed the %d MB overcommit bound",
			r.NodeID, reserved, book, bound)), resourceBlocker("node_ram", "ram_mb", int64(book)))
	}
	return nil
}
