package sched

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Frozen reservations (ADR-1151). A frozen instance is a paused VM kept
// resident after its idle timeout. It holds its measured memory, at least
// FrozenChargeFloorMB (ADR 20261031143521), and no vCPU, CPU millicores or
// concurrency. Under
// measured admission its MB sit outside the reservation overcommit cap (the
// node's measured memory already includes them), and a thaw re-admits the
// instance's full reservation like a wake.

// frozenChargeMB is what a frozen reservation books: the instance's frozen
// charge (api.FrozenChargeMB), never below FrozenChargeFloorMB nor above
// its full reservation (ADR 20261031143521).
func frozenChargeMB(ramMB int, residentMB float64) int {
	charge := api.FrozenChargeMB(ramMB, residentMB)
	return max(charge, api.FrozenChargeFloorMB)
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
	chargeMB = min(max(chargeMB, api.FrozenChargeFloorMB), e.admissionMB)
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
	e.frozenAt, e.compressTried = l.clock(), false
	return true
}

// ShrinkFrozenCharge rebooks a frozen reservation at chargeMB after vmmd
// compressed the VM's memory (ADR-906 decision 6). A charge never grows
// here, and never drops below FrozenChargeFloorMB. It returns false for
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
	chargeMB = max(chargeMB, api.FrozenChargeFloorMB)
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
	charge := max(r.FrozenChargeMB, api.FrozenChargeFloorMB)
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
		admissionMB: charge, frozen: true, admittedAt: l.clock(), frozenAt: l.clock(), frozenSeeded: true,
	}
}

// SettleSeededFrozenCharge rebooks a frozen reservation that SeedLedger
// re-booked at a stand-in charge with the instance's first measured charge,
// up or down, never below FrozenChargeFloorMB. It applies once; after it only
// compression lowers the charge (ShrinkFrozenCharge). Without it a large VM
// frozen across a restart booked the stand-in until it thawed or parked
// (ADR 20261031190030). It reports whether it rebooked.
func (l *NodeLedger) SettleSeededFrozenCharge(instance string, chargeMB int) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[instance]
	if e == nil || !e.frozen || !e.frozenSeeded {
		return false
	}
	chargeMB = max(chargeMB, api.FrozenChargeFloorMB)
	if node := l.resident[e.nodeID]; node != nil {
		node.residentRAM += chargeMB - e.admissionMB
		node.frozenRAM += chargeMB - e.admissionMB
	}
	e.admissionMB, e.frozenSeeded = chargeMB, false
	return true
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

// FrozenCompressCandidates lists, per node, the frozen reservations
// proactive compression may ask vmmd about (ADR 20261031143521): frozen at
// least minAge before now and not tried yet, longest frozen first.
func (l *NodeLedger) FrozenCompressCandidates(now time.Time, minAge time.Duration) map[string][]string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.frozenOldestFirst_locked(func(e *reservation) bool {
		return !e.compressTried && now.Sub(e.frozenAt) >= minAge
	})
}

// FrozenOldestFirst lists, per node, the frozen reservations frozen at least
// minAge before now, longest frozen first: the order frozen VMs yield their
// memory to admission in (ADR 20261031190030).
func (l *NodeLedger) FrozenOldestFirst(now time.Time, minAge time.Duration) map[string][]string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.frozenOldestFirst_locked(func(e *reservation) bool { return now.Sub(e.frozenAt) >= minAge })
}

func (l *NodeLedger) frozenOldestFirst_locked(keep func(*reservation) bool) map[string][]string {
	type candidate struct {
		id string
		at time.Time
	}
	byNode := map[string][]candidate{}
	for id, e := range l.entries {
		if e.frozen && keep(e) {
			byNode[e.nodeID] = append(byNode[e.nodeID], candidate{id: id, at: e.frozenAt})
		}
	}
	out := make(map[string][]string, len(byNode))
	for nodeID, cs := range byNode {
		sort.Slice(cs, func(i, j int) bool {
			if !cs[i].at.Equal(cs[j].at) {
				return cs[i].at.Before(cs[j].at)
			}
			return cs[i].id < cs[j].id
		})
		ids := make([]string, len(cs))
		for i, c := range cs {
			ids[i] = c.id
		}
		out[nodeID] = ids
	}
	return out
}

// HasUntriedFrozen reports whether a node holds a frozen reservation that
// proactive compression has not asked vmmd about yet, of any age.
func (l *NodeLedger) HasUntriedFrozen(nodeID string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.frozen && !e.compressTried && e.nodeID == nodeID {
			return true
		}
	}
	return false
}

// FrozenToYield picks frozen reservations whose parking would give a wake
// refused for RAM the needMB it asks: on the node where parking the fewest
// VMs, longest frozen first and at most limit of them, frees what the node's
// admission headroom lacks. A node whose headroom already fits the request is
// skipped: something other than its RAM refused the wake there. ok is false
// when no node qualifies; parking would then free memory without admitting
// the wake.
func (l *NodeLedger) FrozenToYield(needMB, limit int) (nodeID string, ids []string, ok bool) {
	if l == nil || needMB <= 0 || limit <= 0 {
		return "", nil, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for node, frozen := range l.frozenOldestFirst_locked(func(*reservation) bool { return true }) {
		missing := needMB
		if r := l.resident[node]; r != nil {
			ceiling := r.ceilingMB
			if ceiling <= 0 {
				ceiling = l.ceilingForNode_locked(node, api.Limits{})
			}
			used, basis := l.admissionUsed_locked(node, r.residentRAM, r.frozenRAM, ceiling)
			if basis != "measured" && basis != "resident" {
				continue // the reservations' cap binds, and frozen VMs are not in it
			}
			missing = needMB - max(ceiling-used, 0)
		}
		if missing <= 0 {
			continue
		}
		freed, picked := 0, []string(nil)
		for _, id := range frozen {
			if len(picked) == limit || freed >= missing {
				break
			}
			picked = append(picked, id)
			freed += l.entries[id].admissionMB
		}
		if freed < missing {
			continue
		}
		if !ok || len(picked) < len(ids) || len(picked) == len(ids) && node < nodeID {
			nodeID, ids, ok = node, picked, true
		}
	}
	return nodeID, ids, ok
}

// MarkFrozenCompressTried records that proactive compression asked vmmd
// about a frozen reservation, so it is not asked again.
func (l *NodeLedger) MarkFrozenCompressTried(instance string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[instance]; e != nil && e.frozen {
		e.compressTried = true
	}
}

// FrozenChargeOf is the RAM a frozen reservation books; ok is false for an
// unknown or unfrozen instance.
func (l *NodeLedger) FrozenChargeOf(instance string) (chargeMB int, ok bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[instance]; e != nil && e.frozen {
		return e.admissionMB, true
	}
	return 0, false
}

// FrozenCompressNeed is a node's admission usage and ceiling as Admit sees
// them. ok is false when what binds is the cap on reservations beyond the
// overcommit allowance, which frozen reservations are not part of, so
// compressing a frozen VM cannot lower it.
func (l *NodeLedger) FrozenCompressNeed(nodeID string) (usedMB, ceilingMB int, ok bool) {
	if l == nil {
		return 0, 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	node := l.resident[nodeID]
	if node == nil {
		return 0, 0, false
	}
	ceiling := node.ceilingMB
	if ceiling <= 0 {
		ceiling = l.ceilingForNode_locked(nodeID, api.Limits{})
	}
	used, basis := l.admissionUsed_locked(nodeID, node.residentRAM, node.frozenRAM, ceiling)
	return used, ceiling, basis == "measured" || basis == "resident"
}
