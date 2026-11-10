package fcvm

import (
	"github.com/onebox-faas/faas/pkg/netns"
)

// A guest's workload runs before its instance is published into live: a cold
// boot starts the app, then waits for readiness and characterization. DNS
// answers during that window used to find no network owner, so ADR-373
// resolved egress and ADR-20261026150000 database egress stayed closed and a
// connection made at boot (a Data API's database, an app's migration) timed
// out. bootingEgress holds the network owner of each booting guest from the
// moment its namespace exists until publication or teardown; publication
// carries its grants and network generation over to the live instance.

// registerBootingEgressOwner records the network owner of a guest that is
// about to boot. It carries only what attribution and grants read: the lease,
// the namespace, and the identity the wake fixed. A qualification candidate
// or an execution without a network gets none, as when live.
func (m *Manager) registerBootingEgressOwner(lease Lease, nc netns.Config, req WakeRequest, qualification bool) {
	if req.ExecutionOnly || qualification || lease.Networkless || nc.Netns == "" {
		return
	}
	// AppTaskOnly stays false: a managed probe authenticates service calls
	// from its task lease alone (gatewayd managedProbeCaller), which must keep
	// waiting for publication. DNS grants need only the app identity.
	owner := &Instance{
		Lease: lease, Net: nc,
		AppID: req.AppID, AccountID: req.AccountID, DeploymentID: req.DeploymentID,
		Paused: req.KeepPaused,
		booting: true,
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bootingEgress == nil {
		m.bootingEgress = make(map[string]*Instance)
	}
	m.bootingEgress[lease.Instance] = owner
}

// resolvedEgressOwnerLocked returns the live instance, or the booting network
// owner, that holds id's network. Caller holds m.mu.
func (m *Manager) resolvedEgressOwnerLocked(id string) *Instance {
	if inst := m.live[id]; inst != nil {
		return inst
	}
	return m.bootingEgress[id]
}

// adoptBootingEgressLocked moves the booting owner's grants onto the instance
// being published, so a DNS answer captured during boot still matches its
// network generation and the namespace's sets stay tracked for refresh.
// Caller holds m.mu and publishes inst into live in the same critical section.
func (m *Manager) adoptBootingEgressLocked(inst *Instance) {
	id := inst.Lease.Instance
	owner := m.bootingEgress[id]
	delete(m.bootingEgress, id)
	if owner == nil || owner.Net.Netns != inst.Net.Netns || owner.Lease.HostIP != inst.Lease.HostIP {
		return
	}
	if inst.resolvedEgressGeneration == "" {
		inst.resolvedEgressGeneration = owner.resolvedEgressGeneration
	}
	if inst.networkPolicyMu == nil {
		inst.networkPolicyMu = owner.networkPolicyMu
	}
	if inst.resolvedEgress == nil {
		inst.resolvedEgress = owner.resolvedEgress
	}
	if inst.databaseEgress == nil {
		inst.databaseEgress = owner.databaseEgress
	}
}
