package fcvm

import (
	"net/netip"

	"github.com/google/uuid"
)

// ServiceCallerIdentity is host-owned attribution for one current network
// slot. An occupied non-app slot still matches, preventing a stale database
// identity from being used when an address has been recycled.
type ServiceCallerIdentity struct {
	InstanceID, AccountID, AppID, DeploymentID string
	NetworkGeneration                          string
	AppTask, JobTask                           bool
}

func (m *Manager) ResolveServiceCaller(address netip.Addr) (ServiceCallerIdentity, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var identity ServiceCallerIdentity
	matched := false
	for id, inst := range m.live {
		if inst == nil || inst.Lease.Networkless || inst.Lease.HostIP != address {
			continue
		}
		if matched {
			return ServiceCallerIdentity{}, false // Ambiguity cannot authenticate.
		}
		matched = true
		if inst.resolvedEgressGeneration == "" {
			inst.resolvedEgressGeneration = uuid.NewString()
		}
		identity.InstanceID = id
		identity.NetworkGeneration = inst.resolvedEgressGeneration
		identity.JobTask = inst.IsJob && !inst.Paused && !inst.ExecutionOnly && !inst.QualificationOnly
		if inst.Paused || inst.ExecutionOnly || inst.QualificationOnly || inst.Lease.IsBuilder || inst.IsJob {
			continue
		}
		identity.AccountID, identity.AppID, identity.DeploymentID = inst.AccountID, inst.AppID, inst.DeploymentID
		identity.AppTask = inst.AppTaskOnly
	}
	return identity, matched
}
