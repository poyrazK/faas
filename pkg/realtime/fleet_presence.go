package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	fleetPresenceTombstoneTTL = 10 * time.Second
	fleetPresenceTombstoneMax = ManagedRealtimePresenceMaxMembers * 2
)

// recordFleetPresenceTombstoneLocked bounds stale-event suppression to recent
// departures. Relay and snapshot requests have short deadlines, so older
// tombstones no longer protect against an in-flight stale update.
func (m *Manager) recordFleetPresenceTombstoneLocked(key channelKey, memberID string, at time.Time) {
	if memberID == "" {
		return
	}
	tombstones := m.fleetPresenceTombstones[key]
	if tombstones == nil {
		tombstones = make(map[string]time.Time)
		m.fleetPresenceTombstones[key] = tombstones
	}
	cutoff := at.Add(-fleetPresenceTombstoneTTL)
	for id, createdAt := range tombstones {
		if createdAt.Before(cutoff) {
			delete(tombstones, id)
		}
	}
	if _, exists := tombstones[memberID]; !exists && len(tombstones) >= fleetPresenceTombstoneMax {
		var oldestID string
		var oldestAt time.Time
		for id, createdAt := range tombstones {
			if oldestID == "" || createdAt.Before(oldestAt) {
				oldestID, oldestAt = id, createdAt
			}
		}
		delete(tombstones, oldestID)
	}
	tombstones[memberID] = at
}

func (m *Manager) upsertFleetPresence(ctx context.Context, c *connection, channel, memberID, principal string, state json.RawMessage) (PresenceMember, []PresenceMember, error) {
	if m.cfg.FleetClient == nil {
		return PresenceMember{}, nil, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, realtimeChannelRouteReportTimeout)
	ownMemberID, members, err := m.cfg.FleetClient.UpsertPresence(requestCtx, c.info.EndpointID, channel, c.info.ID, memberID, principal, state)
	cancel()
	if err != nil {
		m.reportFleetFailure(err)
		return PresenceMember{}, nil, err
	}
	if ownMemberID == "" {
		err := fmt.Errorf("realtime: fleet presence upsert omitted the member ID")
		m.reportFleetFailure(err)
		return PresenceMember{}, nil, err
	}
	updated := PresenceMember{MemberID: ownMemberID, State: append(json.RawMessage(nil), state...), ConnectionCount: 1, UpdatedAt: time.Now().UTC()}
	for _, member := range members {
		if member.MemberID == ownMemberID {
			updated = clonePresenceMember(member)
			break
		}
	}
	return updated, members, nil
}

func (m *Manager) renewFleetPresence(ctx context.Context, c *connection, channel string) {
	if m.cfg.FleetClient == nil {
		return
	}
	key := channelKey{endpointID: c.info.EndpointID, channel: channel}
	m.mu.RLock()
	member := m.presenceMembers[key][c.info.ID]
	if member == nil || !member.announced {
		m.mu.RUnlock()
		return
	}
	state := append(json.RawMessage(nil), member.state...)
	memberID := member.memberID
	presenceScope := member.presenceScope
	m.mu.RUnlock()
	principal := ""
	if presenceScope == "principal" {
		principal = c.info.Principal
	}
	if _, _, err := m.upsertFleetPresence(ctx, c, channel, memberID, principal, state); err != nil {
		return
	}
}

func (m *Manager) deleteFleetPresence(ctx context.Context, c *connection, channel string, memberID string) {
	if m.cfg.FleetClient == nil {
		return
	}
	cleanupParent := context.WithoutCancel(ctx)
	deleteCtx, cancel := context.WithTimeout(cleanupParent, realtimeChannelRouteReportTimeout)
	members, observedAt, err := m.cfg.FleetClient.DeletePresence(deleteCtx, c.info.EndpointID, channel, c.info.ID)
	cancel()
	if err != nil {
		m.reportFleetFailure(err)
		snapshotCtx, snapshotCancel := context.WithTimeout(cleanupParent, realtimeChannelRouteReportTimeout)
		members, err = m.cfg.FleetClient.ReadPresenceSnapshot(snapshotCtx, c.info.EndpointID, channel)
		snapshotCancel()
		if err != nil {
			m.reportFleetFailure(err)
			return
		}
		observedAt = time.Time{}
	}
	if memberID == "" {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	frame := EphemeralFrame{Type: "presence", Event: "left", MemberID: memberID, UpdatedAt: observedAt}
	for _, member := range members {
		if member.MemberID == memberID {
			frame.Event = "updated"
			frame.State = append(json.RawMessage(nil), member.State...)
			frame.ConnectionCount = member.ConnectionCount
			frame.UpdatedAt = member.UpdatedAt
			break
		}
	}
	if err := m.BroadcastEphemeral(cleanupParent, c.info.EndpointID, channel, frame); err != nil {
		m.reportFleetFailure(err)
	}
	relayCtx, cancel := context.WithTimeout(cleanupParent, realtimeChannelRouteReportTimeout)
	err = m.relayFleetEphemeral(relayCtx, c.info.EndpointID, channel, frame)
	cancel()
	if err != nil {
		m.reportFleetFailure(err)
	}
}

func (m *Manager) relayFleetEphemeral(ctx context.Context, endpointID, channel string, frame EphemeralFrame) error {
	if m.cfg.FleetClient == nil {
		return nil
	}
	if frame.UpdatedAt.IsZero() {
		frame.UpdatedAt = time.Now().UTC()
	}
	relayCtx, cancel := context.WithTimeout(ctx, realtimeChannelRouteReportTimeout)
	err := m.cfg.FleetClient.RelayEphemeral(relayCtx, endpointID, channel, frame)
	cancel()
	return err
}

func (m *Manager) reportFleetFailure(err error) {
	if err != nil && m.cfg.FleetReportFailure != nil {
		m.cfg.FleetReportFailure(err)
	}
}

func (m *Manager) installFleetPresenceSnapshotLocked(key channelKey, members []PresenceMember) {
	if m.fleetPresence[key] == nil {
		m.fleetPresence[key] = make(map[string]PresenceMember)
	}
	for _, member := range members {
		if member.MemberID == "" {
			continue
		}
		if member.ConnectionCount < 1 {
			member.ConnectionCount = 1
		}
		if member.UpdatedAt.IsZero() {
			member.UpdatedAt = time.Now().UTC()
		}
		if existing, ok := m.fleetPresence[key][member.MemberID]; ok && existing.UpdatedAt.After(member.UpdatedAt) {
			continue
		}
		if tombstone := m.fleetPresenceTombstones[key][member.MemberID]; !tombstone.IsZero() && !member.UpdatedAt.After(tombstone) {
			continue
		}
		m.setFleetPresenceLocked(key, member)
	}
}

func (m *Manager) setFleetPresenceLocked(key channelKey, member PresenceMember) {
	if member.MemberID == "" {
		return
	}
	if member.UpdatedAt.IsZero() {
		member.UpdatedAt = time.Now().UTC()
	}
	if member.ConnectionCount < 1 {
		member.ConnectionCount = 1
	}
	if m.fleetPresence[key] == nil {
		m.fleetPresence[key] = make(map[string]PresenceMember)
	}
	member = clonePresenceMember(member)
	m.fleetPresence[key][member.MemberID] = member
	if tombstones := m.fleetPresenceTombstones[key]; tombstones != nil && !member.UpdatedAt.Before(tombstones[member.MemberID]) {
		delete(tombstones, member.MemberID)
	}
}

func (m *Manager) localPresenceMemberLocked(key channelKey, memberID string) *presenceMember {
	for _, member := range m.presenceMembers[key] {
		if member.memberID == memberID {
			return member
		}
	}
	return nil
}

func (m *Manager) syncFleetPresence(ctx context.Context, key channelKey) {
	if m.cfg.FleetClient == nil {
		return
	}
	started := time.Now().UTC()
	m.mu.Lock()
	if last := m.fleetPresenceLastSync[key]; !last.IsZero() && started.Sub(last) < m.cfg.PresenceSyncInterval {
		m.mu.Unlock()
		return
	}
	m.fleetPresenceLastSync[key] = started
	m.mu.Unlock()

	readCtx, cancel := context.WithTimeout(ctx, realtimeChannelRouteReportTimeout)
	members, err := m.cfg.FleetClient.ReadPresenceSnapshot(readCtx, key.endpointID, key.channel)
	cancel()
	if err != nil {
		m.reportFleetFailure(err)
		return
	}
	current := make(map[string]PresenceMember, len(members))
	for _, member := range members {
		if member.MemberID == "" {
			continue
		}
		if member.UpdatedAt.IsZero() {
			member.UpdatedAt = started
		}
		if member.ConnectionCount < 1 {
			member.ConnectionCount = 1
		}
		current[member.MemberID] = clonePresenceMember(member)
	}

	var slow []*connection
	m.mu.Lock()
	known := m.fleetPresence[key]
	if known == nil {
		known = make(map[string]PresenceMember)
		m.fleetPresence[key] = known
	}
	for memberID, member := range current {
		if tombstone := m.fleetPresenceTombstones[key][memberID]; !tombstone.IsZero() && !member.UpdatedAt.After(tombstone) {
			continue
		}
		existing, exists := known[memberID]
		if exists && existing.UpdatedAt.After(member.UpdatedAt) {
			continue
		}
		if !exists || !bytes.Equal(existing.State, member.State) || existing.ConnectionCount != member.ConnectionCount {
			event := "joined"
			if exists {
				event = "updated"
			}
			slow = append(slow, m.queueEphemeralToSubscribersLocked(ctx, key, "", resumeServerFrame{
				Type: "presence", Channel: key.channel, Event: event, MemberID: memberID,
				State: append(json.RawMessage(nil), member.State...), ConnectionCount: member.ConnectionCount, UpdatedAt: member.UpdatedAt,
			})...)
		}
		m.setFleetPresenceLocked(key, member)
	}
	for memberID, previous := range known {
		if _, exists := current[memberID]; exists || m.localPresenceMemberLocked(key, memberID) != nil || previous.UpdatedAt.After(started) {
			continue
		}
		delete(known, memberID)
		m.recordFleetPresenceTombstoneLocked(key, memberID, started)
		slow = append(slow, m.queueEphemeralToSubscribersLocked(ctx, key, "", resumeServerFrame{
			Type: "presence", Channel: key.channel, Event: "left", MemberID: memberID, UpdatedAt: started,
		})...)
	}
	if len(m.resumeSubscribers[key]) == 0 {
		delete(m.fleetPresence, key)
		delete(m.fleetPresenceTombstones, key)
		delete(m.fleetPresenceLastSync, key)
	}
	m.mu.Unlock()
	for _, c := range slow {
		m.closeResumeOnQueueFull(c, ErrOutboundQueueFull)
	}
}

func clonePresenceMember(member PresenceMember) PresenceMember {
	member.State = append(json.RawMessage(nil), member.State...)
	return member
}
