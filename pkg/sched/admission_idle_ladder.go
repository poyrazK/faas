package sched

// Idle ladder admission (ADR-20261107122928). With vmmd's idle_ladder on, an
// instance quiet-paused and quiet for IdleLadderFreezeAfterSeconds is held at
// the ladder's freeze level (L2). Its row stays RUNNING and its reservation
// stays booked, but under measured admission (ADR-909) it is counted the way
// a frozen reservation is: the node's measured figure charges it with the
// frozen floor (instancestats memoryShadow), and the cap on reservations
// leaves it out, up to api.IdleLadderReservationDiscountMaxPercent of the
// ceiling. The durable node guard applies the same discount, which the
// placement and thaw bindings carry (state.NodeAdmission.ReservationDiscountMB),
// so a node the ledger admits is not refused there. A request resumes such an
// instance in vmmd without admission, as quiet pause always did; its memory
// is already resident, so the measured figure does not jump, and eviction
// above the ceiling still bounds what it grows into. Without measured
// admission nothing changes.

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// IdleLadderFrozen reports the instances on a node that the latest instance
// stats sample saw at the idle ladder's freeze level, and when that sample
// was taken. ok is false when no fresh sample exists; admission then counts
// every booking.
type IdleLadderFrozen func(nodeID string) (instances map[string]struct{}, asOf time.Time, ok bool)

// EnableIdleLadderAdmission makes measured admission's cap on reservations
// leave out instances held at the idle ladder's freeze level.
func (l *NodeLedger) EnableIdleLadderAdmission(src IdleLadderFrozen) {
	l.mu.Lock()
	l.idleLadderFrozen = src
	l.mu.Unlock()
}

// ReservationDiscountMB is the RAM this ledger leaves out of a node's
// reservation cap for instances held at the idle ladder's freeze level; the
// durable guard applies the same discount (ADR-20261107122928).
func (l *NodeLedger) ReservationDiscountMB(nodeID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	node := l.resident[nodeID]
	ceiling := 0
	if node != nil {
		ceiling = node.ceilingMB
	}
	return l.idleLadderDiscountMB_locked(nodeID, ceiling)
}

// idleLadderDiscountMB_locked is the RAM this ledger booked on a node for
// instances the latest sample saw at the ladder's freeze level, bounded at
// api.IdleLadderReservationDiscountMaxPercent of the ceiling. Only measured
// admission with a fresh measurement of the node applies it, as the guard
// takes it only with a measured allowance. A booking admitted after the
// sample is never left out by it, a frozen reservation is already left out,
// and instances booked by another schedd are not in this ledger, so it can
// only undercount.
func (l *NodeLedger) idleLadderDiscountMB_locked(nodeID string, ceiling int) int {
	if l.idleLadderFrozen == nil || l.measured == nil || ceiling <= 0 {
		return 0
	}
	if _, _, fresh := l.measured(nodeID); !fresh {
		return 0
	}
	ids, asOf, ok := l.idleLadderFrozen(nodeID)
	if !ok {
		return 0
	}
	mb := 0
	for id := range ids {
		e := l.entries[id]
		if e == nil || e.nodeID != nodeID || e.frozen || e.agentRun || e.admittedAt.After(asOf) {
			continue
		}
		mb += e.admissionMB
	}
	return min(mb, ceiling*api.IdleLadderReservationDiscountMaxPercent/100)
}
