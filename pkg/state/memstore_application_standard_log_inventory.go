package state

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ApplicationStandardLogInventoryStore = (*MemStore)(nil)

func (m *MemStore) standardLogInventoryLocked(app App) (ApplicationStandardLogInventory, bool) {
	_, e, ok := m.standardLogParentsLocked(AppLogDrain{AppID: app.ID, AccountID: app.AccountID})
	if !ok || e.State != "persisted" && e.State != "observed" {
		return ApplicationStandardLogInventory{}, false
	}
	i := ApplicationStandardLogInventory{OrgID: canonicalStandardUUID(app.OrgID), AppID: canonicalStandardUUID(app.ID), AccountID: canonicalStandardUUID(app.AccountID), DesiredRevision: e.DesiredRevision, EffectiveHash: e.EffectiveHash, Drains: []ApplicationStandardLogInventoryDrain{}}
	for _, d := range m.appLogDrains {
		if d.Enabled && sameStandardUUID(d.AppID, app.ID) {
			i.Drains = append(i.Drains, ApplicationStandardLogInventoryDrain{DrainID: canonicalStandardUUID(d.ID), ConfigHash: ApplicationStandardLogDrainConfigHash(d)})
		}
	}
	slices.SortFunc(i.Drains, func(a, b ApplicationStandardLogInventoryDrain) int { return strings.Compare(a.DrainID, b.DrainID) })
	if e.ExceptionExpiresAt != nil && !time.Now().Before(*e.ExceptionExpiresAt) {
		return i, false
	}
	return i, validStandardLogInventory(i)
}

func (m *MemStore) LoadApplicationStandardLogConsumerSnapshot(ctx context.Context) (ApplicationStandardLogConsumerSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogConsumerSnapshot{}, err
	}
	s := ApplicationStandardLogConsumerSnapshot{Drains: listAppLogDrains(func(d AppLogDrain) bool { return d.Enabled }, m.appLogDrains), Inventories: []ApplicationStandardLogInventory{}}
	for n := range s.Drains {
		s.Drains[n].StandardBinding = m.standardLogBindingLocked(s.Drains[n])
	}
	for _, app := range m.apps {
		if i, ok := m.standardLogInventoryLocked(app); ok {
			s.Inventories = append(s.Inventories, i)
		}
	}
	slices.SortFunc(s.Inventories, func(a, b ApplicationStandardLogInventory) int { return strings.Compare(a.AppID, b.AppID) })
	return s, nil
}

func (m *MemStore) standardLogNodeEligibleLocked(id string) bool {
	for _, n := range m.computeNodes {
		if sameStandardUUID(n.ID, id) {
			return n.Active && (n.Role == nil || *n.Role != "control-plane")
		}
	}
	return false
}

func (m *MemStore) RegisterApplicationStandardLogConsumer(ctx context.Context, nodeID, sessionID string) (ApplicationStandardLogConsumerSession, error) {
	if !validStandardResourceRead(nodeID, sessionID) {
		return ApplicationStandardLogConsumerSession{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogConsumerSession{}, err
	}
	if !m.standardLogNodeEligibleLocked(nodeID) {
		return ApplicationStandardLogConsumerSession{}, ErrApplicationStandardLogConsumerFenced
	}
	if m.applicationStandardLogConsumers == nil {
		m.applicationStandardLogConsumers = map[string]ApplicationStandardLogConsumerSession{}
	}
	key := canonicalStandardUUID(nodeID)
	s := m.applicationStandardLogConsumers[key]
	if _, closed := m.applicationStandardLogConsumerClosures[key]; closed && sameStandardUUID(s.SessionID, sessionID) {
		return ApplicationStandardLogConsumerSession{}, ErrApplicationStandardLogConsumerFenced
	}
	historyKey := key + "\x00" + canonicalStandardUUID(sessionID)
	if _, used := m.applicationStandardLogConsumerSessions[historyKey]; used && !sameStandardUUID(s.SessionID, sessionID) {
		return ApplicationStandardLogConsumerSession{}, ErrApplicationStandardLogConsumerFenced
	}
	if !sameStandardUUID(s.SessionID, sessionID) {
		s = ApplicationStandardLogConsumerSession{NodeID: key, SessionID: canonicalStandardUUID(sessionID), Generation: s.Generation + 1}
		m.applicationStandardLogConsumers[key] = s
		delete(m.applicationStandardLogConsumerClosures, key)
		if m.applicationStandardLogConsumerSessions == nil {
			m.applicationStandardLogConsumerSessions = map[string]ApplicationStandardLogConsumerSession{}
		}
		m.applicationStandardLogConsumerSessions[historyKey] = s
	}
	return s, nil
}

func (m *MemStore) standardLogSessionRegisteredLocked(s ApplicationStandardLogConsumerSession) bool {
	for _, node := range m.computeNodes {
		if sameStandardUUID(node.ID, s.NodeID) {
			return m.applicationStandardLogConsumers[canonicalStandardUUID(s.NodeID)] == s
		}
	}
	return false
}

func (m *MemStore) CheckApplicationStandardLogConsumer(ctx context.Context, s ApplicationStandardLogConsumerSession) error {
	if !validStandardLogSession(s) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !m.standardLogSessionCurrentLocked(s) {
		return ErrApplicationStandardLogConsumerFenced
	}
	return nil
}

func (m *MemStore) RecordApplicationStandardLogInventory(ctx context.Context, s ApplicationStandardLogConsumerSession, i ApplicationStandardLogInventory) (ApplicationStandardLogInventoryObservation, error) {
	if !validStandardLogSession(s) || !validStandardLogInventory(i) {
		return ApplicationStandardLogInventoryObservation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogInventoryObservation{}, err
	}
	if !m.standardLogSessionCurrentLocked(s) {
		return ApplicationStandardLogInventoryObservation{}, ErrApplicationStandardLogConsumerFenced
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	current, ok := m.standardLogInventoryForIDLocked(i.AppID)
	if !ok || !sameStandardLogInventory(current, i) || !m.standardLogNodeEligibleLocked(s.NodeID) {
		return ApplicationStandardLogInventoryObservation{}, ErrApplicationStandardLogDeliveryStale
	}
	if err := ctx.Err(); err != nil {
		return ApplicationStandardLogInventoryObservation{}, err
	}
	o := ApplicationStandardLogInventoryObservation{ApplicationStandardLogInventory: cloneStandardLogInventory(i), ApplicationStandardLogConsumerSession: s, ObservedAt: now}
	if m.applicationStandardLogInventories == nil {
		m.applicationStandardLogInventories = map[string]ApplicationStandardLogInventoryObservation{}
	}
	m.applicationStandardLogInventories[i.AppID+"\x00"+s.NodeID] = o
	return o, nil
}

func (m *MemStore) standardLogInventoryForIDLocked(id string) (ApplicationStandardLogInventory, bool) {
	for _, app := range m.apps {
		if sameStandardUUID(app.ID, id) {
			return m.standardLogInventoryLocked(app)
		}
	}
	return ApplicationStandardLogInventory{}, false
}

func (m *MemStore) ListApplicationStandardLogInventories(ctx context.Context, orgID, appID string) ([]ApplicationStandardLogInventoryObservation, error) {
	if !validStandardResourceRead(orgID, appID) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.standardLogInventoryReadScopeLocked(orgID, appID) {
		return nil, ErrNotFound
	}
	i, eligible := m.standardLogInventoryForIDLocked(appID)
	result := []ApplicationStandardLogInventoryObservation{}
	for _, o := range m.applicationStandardLogInventories {
		if eligible && sameStandardLogInventory(i, o.ApplicationStandardLogInventory) && m.standardLogSessionCurrentLocked(o.ApplicationStandardLogConsumerSession) && m.standardLogNodeEligibleLocked(o.NodeID) && time.Since(o.ObservedAt) < api.ApplicationStandardLogInventoryFreshness {
			o.ApplicationStandardLogInventory = cloneStandardLogInventory(o.ApplicationStandardLogInventory)
			result = append(result, o)
		}
	}
	slices.SortFunc(result, func(a, b ApplicationStandardLogInventoryObservation) int { return strings.Compare(a.NodeID, b.NodeID) })
	return result, nil
}

func (m *MemStore) standardLogInventoryReadScopeLocked(orgID, appID string) bool {
	for _, a := range m.apps {
		if sameStandardUUID(a.ID, appID) && sameStandardUUID(a.OrgID, orgID) && a.Status != AppDeleted {
			return true
		}
	}
	return false
}

func (m *MemStore) eraseStandardLogInventoriesLocked(appID string) {
	for key, o := range m.applicationStandardLogInventories {
		if sameStandardUUID(o.AppID, appID) {
			delete(m.applicationStandardLogInventories, key)
		}
	}
}

func (m *MemStore) eraseStandardLogConsumerNodeLocked(nodeID string) {
	m.eraseStandardLogHealthLocked("", "", nodeID)
	delete(m.applicationStandardLogConsumers, canonicalStandardUUID(nodeID))
	delete(m.applicationStandardLogConsumerClosures, canonicalStandardUUID(nodeID))
	for key, s := range m.applicationStandardLogConsumerSessions {
		if sameStandardUUID(s.NodeID, nodeID) {
			delete(m.applicationStandardLogConsumerSessions, key)
		}
	}
	for key, o := range m.applicationStandardLogInventories {
		if sameStandardUUID(o.NodeID, nodeID) {
			delete(m.applicationStandardLogInventories, key)
		}
	}
}
