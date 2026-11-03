package sched

// The vCPU and CPU gates discount bookings for instances that use less host
// CPU than they booked.
//
// Quiet pause admission (ADR-1151). vmmd pauses an eligible instance's vCPUs
// between requests; the instance stays RUNNING and booked. A paused guest
// uses no host CPU, so its whole booking is discounted.
//
// Measured CPU admission (ADR 20261029151111). An instance the latest
// instance stats sample measured is charged its measured host CPU instead of
// its CPU booking. The booking model assumes an instance uses
// 1/api.CPUOvercommit of its booking, which is why the CPU budget is the
// node's cores × api.CPUOvercommit; a measured instance is charged its
// measured millicores at that same rate, so measured charges fill the budget
// exactly when they fill the node's cores. The charge never falls below
// api.MeasuredCPUMinChargePercent of the booking and never exceeds it, so
// measurement only relaxes the gate. Guest vCPU stops being a resource for a
// measured instance: an idle vCPU thread costs the host nothing, and its
// host CPU is what the CPU gate counts.
//
// A resume or a burst never waits for admission, so the gates also keep
// every booking, discounted or not, within QuietPauseMaxVCPUOvercommitPercent
// of the budget, or MeasuredCPUMaxOvercommitPercent once measured CPU
// admission is on: a measured instance that gets busy is charged its booking
// on the next sample, while a quiet-paused one resumes unseen. Exceeding the
// budget briefly when many instances get busy together costs CPU contention,
// not failed requests.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// QuietPaused reports the instances on a node that the latest instance
// stats sample saw quiet-paused, and when that sample was taken. ok is
// false when no fresh sample exists; admission then counts every booking.
type QuietPaused func(nodeID string) (instances map[string]struct{}, asOf time.Time, ok bool)

// MeasuredCPU reports the measured host CPU millicores of each instance on a
// node that the latest instance stats sample measured, and when that sample
// was taken. ok is false when no fresh sample exists; admission then counts
// every booking.
type MeasuredCPU func(nodeID string) (millicores map[string]int, asOf time.Time, ok bool)

// EnableQuietPauseAdmission makes the vCPU and CPU gates count only active
// instances (ADR-1151).
func (l *NodeLedger) EnableQuietPauseAdmission(src QuietPaused) {
	l.mu.Lock()
	l.quietPaused = src
	l.mu.Unlock()
}

// EnableMeasuredCPUAdmission makes the vCPU and CPU gates charge measured
// instances their measured CPU (ADR 20261029151111).
func (l *NodeLedger) EnableMeasuredCPUAdmission(src MeasuredCPU) {
	l.mu.Lock()
	l.measuredCPU = src
	l.mu.Unlock()
}

// measuredCPUCharge is what the CPU gate charges an instance that booked
// booked millicores and was measured using measured: its measurement at the
// booking model's rate, between MeasuredCPUMinChargePercent of the booking
// and the booking.
func measuredCPUCharge(measured, booked int) int {
	charge := max(measured*api.CPUOvercommit, booked*api.MeasuredCPUMinChargePercent/100)
	return min(charge, booked)
}

// cpuDiscount_locked is the vCPU and CPU millicores this ledger booked on a
// node that the latest samples show unused: the whole booking of an instance
// seen quiet-paused, and what a measured instance booked beyond its
// measured charge. A booking admitted after a sample is never discounted by
// it (the sample cannot have seen it), and instances booked by another
// schedd are not in this ledger, so the discount can only undercount.
func (l *NodeLedger) cpuDiscount_locked(nodeID string) (vcpu, cpuMillicores int) {
	if l.nodeCPUPressured_locked(nodeID) {
		return 0, 0
	}
	var paused map[string]struct{}
	var pausedAsOf time.Time
	if l.quietPaused != nil {
		if p, asOf, ok := l.quietPaused(nodeID); ok {
			paused, pausedAsOf = p, asOf
		}
	}
	var measured map[string]int
	var measuredAsOf time.Time
	if l.measuredCPU != nil {
		if m, asOf, ok := l.measuredCPU(nodeID); ok {
			measured, measuredAsOf = m, asOf
		}
	}
	discount := func(id string) (int, int) {
		e := l.entries[id]
		if e == nil || e.nodeID != nodeID {
			return 0, 0
		}
		booked := e.cpuMillicores + e.cpuBoostMillicores
		if _, ok := paused[id]; ok && !e.admittedAt.After(pausedAsOf) {
			return e.vcpu, booked
		}
		if m, ok := measured[id]; ok && !e.admittedAt.After(measuredAsOf) {
			return e.vcpu, booked - measuredCPUCharge(m, booked)
		}
		return 0, 0
	}
	for id := range paused {
		v, c := discount(id)
		vcpu += v
		cpuMillicores += c
	}
	for id := range measured {
		if _, counted := paused[id]; counted {
			continue
		}
		v, c := discount(id)
		vcpu += v
		cpuMillicores += c
	}
	return vcpu, cpuMillicores
}

// nodeCPUPressured_locked reports whether the node is held under CPU
// pressure (ADR 20261030100950): its CPU discount is withheld, so the gates
// and the durable guard count every booking there.
func (l *NodeLedger) nodeCPUPressured_locked(nodeID string) bool {
	return l.cpuPressured != nil && l.cpuPressured(nodeID, l.clock())
}

// overcommitCap_locked is the share of a budget a node's bookings may
// reach under the discount.
func (l *NodeLedger) overcommitCap_locked() int {
	if l.measuredCPU != nil {
		return api.MeasuredCPUMaxOvercommitPercent
	}
	return api.QuietPauseMaxVCPUOvercommitPercent
}

// CPUDiscount is the vCPU and CPU millicores placement discounts on a node
// for quiet-paused and measured instances; the durable guard applies the
// same discount (ADR-20261027220445).
func (l *NodeLedger) CPUDiscount(nodeID string) (vcpu, cpuMillicores int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cpuDiscount_locked(nodeID)
}

// quietEffective is the usage a gate compares with its budget: bookings
// less the discount, but never less than what keeps all bookings within
// capPercent of the budget. Without a discount it is the bookings.
//
//	effective + request <= budget
//	  <=> used + request <= budget  and  booked + request <= budget * cap
func quietEffective(booked, discount, budget, capPercent int) int {
	if discount <= 0 || budget <= 0 {
		return booked
	}
	active := booked - discount
	capped := booked - budget*(capPercent-100)/100
	return max(active, capped)
}

// EffectiveVCPUForNode is the vCPU usage placement compares with a node's
// budget, the same figure Admit uses.
func (l *NodeLedger) EffectiveVCPUForNode(nodeID string, budget int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	used := 0
	if node := l.resident[nodeID]; node != nil {
		used = node.usedVCPU
	}
	vcpu, _ := l.cpuDiscount_locked(nodeID)
	return quietEffective(used, vcpu, budget, l.overcommitCap_locked())
}

// EffectiveFleetVCPUForNode is EffectiveVCPUForNode over the larger of this
// ledger's booking and fleet, the store's count of every live instance on
// the node. Each owner scheduler books only its own apps' instances, so on a
// node shared by several owners the ledger alone undercounts.
func (l *NodeLedger) EffectiveFleetVCPUForNode(nodeID string, fleet int64, budget int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	used := 0
	if node := l.resident[nodeID]; node != nil {
		used = node.usedVCPU
	}
	used = max(used, int(fleet))
	vcpu, _ := l.cpuDiscount_locked(nodeID)
	return quietEffective(used, vcpu, budget, l.overcommitCap_locked())
}

// EffectiveCPUMillicoresForNode applies the quiet pause and measured CPU
// discount to a node's booked CPU millicores, which placement reads from the
// ledger and the fleet-wide store view.
func (l *NodeLedger) EffectiveCPUMillicoresForNode(nodeID string, booked int64, budget int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, cpu := l.cpuDiscount_locked(nodeID)
	return int64(quietEffective(int(booked), cpu, int(budget), l.overcommitCap_locked()))
}
