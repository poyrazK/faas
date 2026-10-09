package fcvm

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ProfileInstanceIdentity contains only host-owned telemetry identity. It
// never carries environment values, secrets, or guest-provided metadata.
type ProfileInstanceIdentity struct {
	AccountID, AppID, DeploymentID, Runtime, Generation string
	Plan                                                api.Plan
	StartedAt                                           time.Time
}

func (m *Manager) InstanceProfileIdentity(id string) (ProfileInstanceIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[id]
	if !ok || inst.Paused || inst.ExecutionOnly || inst.AppTaskOnly {
		return ProfileInstanceIdentity{}, fmt.Errorf("instance is not serving an application")
	}
	// Recovery creates a fresh acceptance epoch so buffers captured before a
	// broker restart cannot be attributed to its new observed lifetime.
	if inst.Lease.profileStartedAt.IsZero() {
		inst.Lease.profileStartedAt = time.Now()
	}
	return ProfileInstanceIdentity{AccountID: inst.AccountID, AppID: inst.AppID, DeploymentID: inst.DeploymentID, Runtime: inst.Runtime, Plan: inst.Lease.Plan, StartedAt: inst.Lease.profileStartedAt,
		Generation: fmt.Sprintf("%d-%d", inst.Lease.processGeneration, inst.Lease.profileStartedAt.UnixNano())}, nil
}
