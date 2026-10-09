package state

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ RuntimeUpgradeGatewayRosterStore = (*MemStore)(nil)
var _ RuntimeUpgradeGatewayHeartbeatStore = (*MemStore)(nil)

func (m *MemStore) ReviewRuntimeUpgradeGatewayRoster(ctx context.Context, expected string, members []RuntimeUpgradeGatewayMember) (RuntimeUpgradeGatewayRoster, error) {
	members, err := canonicalRuntimeUpgradeGatewayMembers(expected, members)
	if err != nil {
		return RuntimeUpgradeGatewayRoster{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeGatewayRoster{}, err
	}
	if m.runtimeUpgradeGatewayRoster.Revision != expected {
		return RuntimeUpgradeGatewayRoster{}, ErrConflict
	}
	if !slices.Equal(m.runtimeUpgradeGatewayRoster.Members, members) {
		m.runtimeUpgradeGatewayRoster = RuntimeUpgradeGatewayRoster{Revision: uuid.NewString(), Members: members, CreatedAt: time.Now().UTC()}
		m.runtimeUpgradeGatewayHeartbeats = make(map[string]runtimeUpgradeGatewayHeartbeat)
	}
	return cloneRuntimeUpgradeGatewayRoster(m.runtimeUpgradeGatewayRoster), nil
}

func (m *MemStore) RuntimeUpgradeGatewayRoster(ctx context.Context) (RuntimeUpgradeGatewayRoster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeGatewayRoster{}, err
	}
	if m.runtimeUpgradeGatewayRoster.Revision == "" {
		return RuntimeUpgradeGatewayRoster{}, ErrNotFound
	}
	return cloneRuntimeUpgradeGatewayRoster(m.runtimeUpgradeGatewayRoster), nil
}

func cloneRuntimeUpgradeGatewayRoster(r RuntimeUpgradeGatewayRoster) RuntimeUpgradeGatewayRoster {
	r.Members = slices.Clone(r.Members)
	return r
}

func (m *MemStore) HeartbeatRuntimeUpgradeGateway(ctx context.Context, slot, session string) error {
	if !canonicalRuntimeUpgradeGatewayUUID(slot) || !canonicalRuntimeUpgradeGatewayUUID(session) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	roster := m.runtimeUpgradeGatewayRoster
	if !slices.Contains(roster.Members, RuntimeUpgradeGatewayMember{SlotID: slot, SessionID: session}) {
		return ErrConflict
	}
	now := time.Now().UTC()
	m.runtimeUpgradeGatewayHeartbeats[slot] = runtimeUpgradeGatewayHeartbeat{Revision: roster.Revision, SlotID: slot, SessionID: session, SeenAt: now, ExpiresAt: now.Add(api.RuntimeUpgradeGatewayHeartbeatMaxAge)}
	return nil
}

func (m *MemStore) runtimeUpgradeGatewayMembershipLocked(out RuntimeUpgradeVerification) RuntimeUpgradeVerification {
	frozen, enrolled := m.runtimeUpgradeVerifications[out.OperationID]
	heartbeats := make([]runtimeUpgradeGatewayHeartbeat, 0, len(m.runtimeUpgradeGatewayHeartbeats))
	for _, h := range m.runtimeUpgradeGatewayHeartbeats {
		heartbeats = append(heartbeats, h)
	}
	return evaluateRuntimeUpgradeGatewayMembership(out, m.runtimeUpgradeGatewayRoster, frozen.GatewayRosterRevision, enrolled, heartbeats)
}
