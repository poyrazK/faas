package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Startup history fences late replies from superseded vmmd processes.
func (m *MemStore) RegisterComputeNodeRuntimeIdentity(ctx context.Context, identity runtimeadmission.Identity) error {
	if identity.Validate() != nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := m.computeNodes[identity.NodeID]; !ok {
		return ErrNotFound
	}
	current := runtimeadmission.Identity{NodeID: identity.NodeID, Incarnation: m.computeNodeRuntimeIncarnations[identity.NodeID], ProtocolVersion: m.computeNodeRuntimeProtocols[identity.NodeID]}
	key := identity.NodeID + "\x00" + identity.Incarnation
	if current.Incarnation == identity.Incarnation && current.ProtocolVersion != identity.ProtocolVersion {
		return ErrApplicationStandardRuntimeStale
	}
	if _, seen := m.computeNodeRuntimeHistory[key]; seen && current.Incarnation != identity.Incarnation {
		return ErrApplicationStandardRuntimeStale
	}
	if m.computeNodeRuntimeHistory == nil {
		m.computeNodeRuntimeHistory = map[string]runtimeadmission.Identity{}
	}
	if current.Incarnation != "" {
		m.computeNodeRuntimeHistory[current.NodeID+"\x00"+current.Incarnation] = current
	}
	if m.computeNodeRuntimeIncarnations == nil {
		m.computeNodeRuntimeIncarnations = map[string]string{}
	}
	if m.computeNodeRuntimeProtocols == nil {
		m.computeNodeRuntimeProtocols = map[string]uint32{}
	}
	m.computeNodeRuntimeIncarnations[identity.NodeID] = identity.Incarnation
	m.computeNodeRuntimeProtocols[identity.NodeID] = identity.ProtocolVersion
	m.computeNodeRuntimeHistory[key] = identity
	return nil
}

func (m *MemStore) eraseNativeRuntimeHistoryLocked(nodeID string) {
	for key, identity := range m.computeNodeRuntimeHistory {
		if identity.NodeID == nodeID {
			delete(m.computeNodeRuntimeHistory, key)
		}
	}
}
