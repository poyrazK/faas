package sched

// Quiet pause admission (ADR-1151). vmmd pauses an eligible instance's vCPUs
// between requests; the instance stays RUNNING and booked. A paused guest
// uses no host CPU, so when quiet pause admission is on, the vCPU and
// physical CPU gates count only active instances: bookings less the ones
// the latest instance stats sample saw quiet-paused. A resume never waits
// for admission, so the gates also keep every booking, paused or not, within
// QuietPauseMaxVCPUOvercommitPercent of the budget. Exceeding the budget
// briefly when many paused instances resume together costs CPU contention,
// not failed requests.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// QuietPaused reports the instances on a node that the latest instance
// stats sample saw quiet-paused, and when that sample was taken. ok is
// false when no fresh sample exists; admission then counts every booking.
type QuietPaused func(nodeID string) (instances map[string]struct{}, asOf time.Time, ok bool)

// EnableQuietPauseAdmission makes the vCPU and CPU gates count only active
// instances (ADR-1151).
func (l *NodeLedger) EnableQuietPauseAdmission(src QuietPaused) {
	l.mu.Lock()
	l.quietPaused = src
	l.mu.Unlock()
}

// quietDiscount_locked is the vCPU and CPU millicores this ledger booked for
// instances quiet-paused in the latest sample. A booking admitted after the
// sample is never discounted (the sample cannot have seen it paused), and
// instances booked by another schedd are not in this ledger, so the
// discount can only undercount.
func (l *NodeLedger) quietDiscount_locked(nodeID string) (vcpu, cpuMillicores int) {
	if l.quietPaused == nil {
		return 0, 0
	}
	paused, asOf, ok := l.quietPaused(nodeID)
	if !ok {
		return 0, 0
	}
	for id := range paused {
		e := l.entries[id]
		if e == nil || e.nodeID != nodeID || e.admittedAt.After(asOf) {
			continue
		}
		vcpu += e.vcpu
		cpuMillicores += e.cpuMillicores + e.cpuBoostMillicores
	}
	return vcpu, cpuMillicores
}

// quietEffective is the usage a gate compares with its budget: bookings
// less quiet-paused ones, but never less than what keeps all bookings
// within QuietPauseMaxVCPUOvercommitPercent of the budget. Without a
// discount it is the bookings.
//
//	effective + request <= budget
//	  <=> active + request <= budget  and  booked + request <= budget * cap
func quietEffective(booked, discount, budget int) int {
	if discount <= 0 || budget <= 0 {
		return booked
	}
	active := booked - discount
	capped := booked - budget*(api.QuietPauseMaxVCPUOvercommitPercent-100)/100
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
	vcpu, _ := l.quietDiscount_locked(nodeID)
	return quietEffective(used, vcpu, budget)
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
	vcpu, _ := l.quietDiscount_locked(nodeID)
	return quietEffective(used, vcpu, budget)
}

// EffectiveCPUMillicoresForNode applies the quiet pause discount to a
// node's booked CPU millicores, which placement reads from the ledger and
// the fleet-wide store view.
func (l *NodeLedger) EffectiveCPUMillicoresForNode(nodeID string, booked int64, budget int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, cpu := l.quietDiscount_locked(nodeID)
	return int64(quietEffective(int(booked), cpu, int(budget)))
}
