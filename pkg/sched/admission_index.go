package sched

// The ledger's per-node and CPU-boost indexes (ADR 20261104143005).
//
// Placement asks the ledger for each candidate node's admission figure on
// every wake, and under measured admission that figure adds the node's
// reservations admitted after its last measurement. It found them by scanning
// every reservation in the fleet, once per candidate node, under the mutex
// every admission and release takes; Admit also scanned every reservation to
// expire CPU startup boosts. byNode and boosted keep those walks to the
// reservations that can matter. Every insert into and delete from
// l.entries goes through putEntry_locked and dropEntry_locked.

// putEntry_locked records a new reservation and indexes it.
func (l *NodeLedger) putEntry_locked(instance string, e *reservation) {
	if old := l.entries[instance]; old != nil {
		l.unindexEntry_locked(instance, old)
	}
	l.entries[instance] = e
	if l.byNode == nil {
		l.byNode = make(map[string]map[string]*reservation)
	}
	onNode := l.byNode[e.nodeID]
	if onNode == nil {
		onNode = make(map[string]*reservation)
		l.byNode[e.nodeID] = onNode
	}
	onNode[instance] = e
	if e.cpuBoostMillicores > 0 && !e.cpuBoostUntil.IsZero() {
		if l.boosted == nil {
			l.boosted = make(map[*reservation]struct{})
		}
		l.boosted[e] = struct{}{}
	}
}

// dropEntry_locked removes a reservation and its index entries.
func (l *NodeLedger) dropEntry_locked(instance string) {
	e := l.entries[instance]
	if e == nil {
		return
	}
	delete(l.entries, instance)
	l.unindexEntry_locked(instance, e)
}

func (l *NodeLedger) unindexEntry_locked(instance string, e *reservation) {
	if onNode := l.byNode[e.nodeID]; onNode != nil {
		delete(onNode, instance)
		if len(onNode) == 0 {
			delete(l.byNode, e.nodeID)
		}
	}
	delete(l.boosted, e)
}

// entriesOnNode_locked returns nodeID's reservations. The map is the index
// itself: callers read it under l.mu and never change it.
func (l *NodeLedger) entriesOnNode_locked(nodeID string) map[string]*reservation {
	return l.byNode[nodeID]
}
