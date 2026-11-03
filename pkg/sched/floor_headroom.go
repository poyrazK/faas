package sched

// FloorHeadroomMB is the most RAM one more reservation can take on any
// admitting node, the figure the floor trigger compares a refill with (ADR
// 20261031190030). NodeLedger.AdmissionHeadroomMB sees only the nodes this
// ledger holds reservations on: a node that just joined, or whose last
// reservation was released, was invisible, and while the booked nodes were
// full every floor refill reported ram_ceiling though placement had room.
// Once the recovery runner has read the node list (ObserveNodeLifecycles),
// every admitting node counts and only those: a draining node's spare RAM
// cannot take a refill. Before that read the ledger's view stands.
func (e *Engine) FloorHeadroomMB() int {
	ceilings := e.admittingCeilings.Load()
	if ceilings == nil {
		return e.ledger.AdmissionHeadroomMB()
	}
	best := 0
	for nodeID, ceiling := range *ceilings {
		best = max(best, ceiling-e.ledger.AdmissionUsedMB(nodeID))
	}
	return best
}
