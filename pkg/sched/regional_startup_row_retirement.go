package sched

// adr: 789
import (
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Call only after an authenticated accepted original stop, never an inventory
// observation or a terminal row. Full startup recovery and the current row must
// corroborate the captured reservation. This grants no dispatch or head authority.
func (l *NodeLedger) releaseStoppedRegionalStartupRow(source runtimequota.LaunchSource, ins state.Instance) bool {
	if l == nil || source.Validate() != nil || source.Permit.Kind == runtimequota.CapacityAppTask || source.InstanceKey != ins.ID || !state.RegionalRuntimeAttemptMatches(source, ins) {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.inventoryRecovered || l.seedInProgress || l.recoveryBarrier != nil {
		return false
	}
	r := l.entries[source.InstanceKey]
	if r == nil || !r.rowShadow || r.regionalSource != nil || !regionalReservationMatches(source, Request{Instance: source.InstanceKey, AppID: r.appID, DeploymentID: r.deploymentID, NodeID: r.nodeID, RuntimeRunID: r.runtimeRunID}) {
		return false
	}
	// The exact stopped hold was already released. Any other retained physical
	// attempt with this UUID leaves the unbound row's origin ambiguous.
	for _, hold := range l.regionalHolds {
		if runtimequota.MatchesID(hold.Receipt.Permit.InstanceID, source.Permit.InstanceID) {
			return false
		}
	}
	l.releaseLocked(source.InstanceKey)
	return true
}
