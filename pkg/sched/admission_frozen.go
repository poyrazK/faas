package sched

import "github.com/onebox-faas/faas/pkg/api"

// Frozen reservations (ADR-1151). A frozen instance is a paused VM kept
// resident after its idle timeout. It holds its measured memory, at least
// MeasuredChargeFloorMB, and no vCPU, CPU millicores or concurrency. Under
// measured admission its MB sit outside the reservation overcommit cap (the
// node's measured memory already includes them), and a thaw re-admits the
// instance's full reservation like a wake.

// frozenChargeMB is what a frozen reservation books: the instance's
// measured charge, never below MeasuredChargeFloorMB nor above its full
// reservation.
func frozenChargeMB(ramMB int, residentMB float64) int {
	charge := api.MeasuredChargeMB(ramMB, residentMB)
	return max(charge, api.MeasuredChargeFloorMB)
}

// Freeze converts an instance's serving reservation into a frozen one that
// books chargeMB of RAM and releases its vCPU, CPU and concurrency. It
// returns false for an unknown or already frozen instance.
func (l *NodeLedger) Freeze(instance string, chargeMB int) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensureEnvironmentConcurrencyLocked()
	e := l.entries[instance]
	if e == nil || e.frozen {
		return false
	}
	chargeMB = min(max(chargeMB, api.MeasuredChargeFloorMB), e.admissionMB)
	if e.countsConc {
		e.countsConc = false
		l.perApp[e.appID]--
		l.cleanupApp(e.appID)
		l.releaseEnvironmentConcurrencyLocked(e)
		if e.deploymentID != "" {
			key := e.appID + "\x00" + e.deploymentID
			l.perAppDeployment[key]--
			if l.perAppDeployment[key] <= 0 {
				delete(l.perAppDeployment, key)
			}
		}
	}
	if node := l.resident[e.nodeID]; node != nil {
		node.residentRAM -= e.admissionMB - chargeMB
		node.usedVCPU -= e.vcpu
		node.usedCPUMillicores -= e.cpuMillicores + e.cpuBoostMillicores
		node.frozenRAM += chargeMB
		node.usedVCPU = max(node.usedVCPU, 0)
		node.usedCPUMillicores = max(node.usedCPUMillicores, 0)
	}
	e.admissionMB = chargeMB
	e.vcpu, e.cpuMillicores, e.cpuBoostMillicores = 0, 0, 0
	e.frozen = true
	return true
}

// ShrinkFrozenCharge rebooks a frozen reservation at chargeMB after vmmd
// compressed the VM's memory (ADR-906 decision 6). A charge never grows
// here, and never drops below MeasuredChargeFloorMB. It returns false for
// an unknown or unfrozen instance.
func (l *NodeLedger) ShrinkFrozenCharge(instance string, chargeMB int) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	if e == nil || !e.frozen {
		return false
	}
	chargeMB = max(chargeMB, api.MeasuredChargeFloorMB)
	if chargeMB >= e.admissionMB {
		return true
	}
	if node := l.resident[e.nodeID]; node != nil {
		node.residentRAM -= e.admissionMB - chargeMB
		node.frozenRAM -= e.admissionMB - chargeMB
	}
	e.admissionMB = chargeMB
	return true
}

// admitFrozen_locked books a frozen reservation directly: schedd's ledger
// seed after a restart finds FROZEN rows whose VMs are already resident.
// Like other seed reservations it is not refused for headroom.
func (l *NodeLedger) admitFrozen_locked(r Request) {
	charge := max(r.FrozenChargeMB, api.MeasuredChargeFloorMB)
	if full := r.admissionMB(); charge > full {
		charge = full
	}
	node := l.resident[r.NodeID]
	if node == nil {
		node = &nodeReservation{ceilingMB: r.NodeCeilingMB}
		l.resident[r.NodeID] = node
	}
	node.residentRAM += charge
	node.frozenRAM += charge
	l.entries[r.Instance] = &reservation{
		appID: r.AppID, deploymentID: r.DeploymentID, environmentKey: r.EnvironmentKey, nodeID: r.NodeID,
		production: r.ProductionEnvironment, deploymentScope: r.DeploymentScope,
		admissionMB: charge, frozen: true, admittedAt: l.clock(),
	}
}

// FrozenCountForNode is the number of frozen reservations on a node, which
// FrozenMaxPerNode caps.
func (l *NodeLedger) FrozenCountForNode(nodeID string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.entries {
		if e.frozen && e.nodeID == nodeID {
			n++
		}
	}
	return n
}

// FrozenCountForApp is the number of frozen reservations of an app. The
// wake path skips the thaw check for an app without one.
func (l *NodeLedger) FrozenCountForApp(appID string) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.entries {
		if e.frozen && e.appID == appID {
			n++
		}
	}
	return n
}

// IsFrozen reports whether the ledger holds a frozen reservation for the
// instance.
func (l *NodeLedger) IsFrozen(instance string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	return e != nil && e.frozen
}
