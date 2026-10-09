package fcvm

import "fmt"

// TraceInstanceIdentity is the host-owned identity stamped on in-guest trace
// exports (ADR-829). It never carries guest-provided metadata.
type TraceInstanceIdentity struct {
	AccountID, AppID, DeploymentID string
}

// InstanceTraceIdentity resolves a serving instance's identity without
// touching any per-instance state.
func (m *Manager) InstanceTraceIdentity(id string) (TraceInstanceIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[id]
	if !ok || inst.Paused || inst.ExecutionOnly || inst.AppTaskOnly {
		return TraceInstanceIdentity{}, fmt.Errorf("instance is not serving an application")
	}
	if inst.AccountID == "" || inst.AppID == "" || inst.DeploymentID == "" {
		return TraceInstanceIdentity{}, fmt.Errorf("instance identity is incomplete")
	}
	return TraceInstanceIdentity{AccountID: inst.AccountID, AppID: inst.AppID, DeploymentID: inst.DeploymentID}, nil
}
