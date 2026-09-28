package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

type memAppWakeTransition struct {
	AppWakeTransition
	RequestedAt  time.Time
	CompletedAt  *time.Time
	SupersededAt *time.Time
	InstanceID   string
	WakeID       string
}

func (m *MemStore) BeginAppWakeTransition(_ context.Context, appID string) (AppWakeTransition, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return AppWakeTransition{}, false, ErrNotFound
	}
	if app.Status == AppActive {
		if id := m.appWakeTransitionByApp[appID]; id != "" {
			if transition, exists := m.appWakeTransitions[id]; exists && transition.CompletedAt == nil && transition.SupersededAt == nil {
				return AppWakeTransition{ID: id, AppID: appID}, false, nil
			}
		}
		return AppWakeTransition{}, false, nil
	}
	if app.Status != AppEvictedCold {
		return AppWakeTransition{}, false, nil
	}

	m.clearCurrentAppParkTransitionLocked(appID)
	if id := m.appWakeTransitionByApp[appID]; id != "" {
		m.supersedeAppWakeTransitionLocked(id)
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	transition := memAppWakeTransition{
		AppWakeTransition: AppWakeTransition{ID: id, AppID: appID},
		RequestedAt:       now,
	}
	m.appWakeTransitions[id] = transition
	m.appWakeTransitionByApp[appID] = id
	app.Status = AppActive
	m.apps[appID] = app
	return transition.AppWakeTransition, true, nil
}

func (m *MemStore) AbortAppWakeTransition(_ context.Context, transitionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	transition, ok := m.appWakeTransitions[transitionID]
	if !ok || transition.CompletedAt != nil || transition.SupersededAt != nil {
		return false, nil
	}
	app, exists := m.apps[transition.AppID]
	if !exists || app.Status != AppActive || m.appWakeTransitionByApp[transition.AppID] != transitionID {
		m.supersedeAppWakeTransitionLocked(transitionID)
		return false, nil
	}
	m.supersedeAppWakeTransitionLocked(transitionID)
	app.Status = AppEvictedCold
	m.apps[transition.AppID] = app
	delete(m.appWakeTransitionByApp, transition.AppID)
	return true, nil
}

func (m *MemStore) CompleteReadyAppWakeTransition(_ context.Context, transitionID, instanceID, wakeID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.completeReadyAppWakeTransitionLocked(transitionID, instanceID, wakeID), nil
}

func (m *MemStore) DrainReadyAppWakeTransitions(_ context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, transition := range m.appWakeTransitions {
		if transition.CompletedAt != nil || transition.SupersededAt != nil {
			continue
		}
		app, exists := m.apps[transition.AppID]
		account, accountExists := m.accounts[app.AccountID]
		if !exists || !accountExists || !account.Active() || app.Status != AppActive || m.appWakeTransitionByApp[transition.AppID] != id {
			m.supersedeAppWakeTransitionLocked(id)
		}
	}
	transitions := make([]memAppWakeTransition, 0, len(m.appWakeTransitions))
	for _, transition := range m.appWakeTransitions {
		if transition.CompletedAt == nil && transition.SupersededAt == nil {
			transitions = append(transitions, transition)
		}
	}
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].RequestedAt.Equal(transitions[j].RequestedAt) {
			return transitions[i].ID < transitions[j].ID
		}
		return transitions[i].RequestedAt.Before(transitions[j].RequestedAt)
	})
	completed := 0
	for _, transition := range transitions {
		if completed >= limit {
			break
		}
		var ready *Instance
		for _, instance := range m.instances {
			if instance.AppID != transition.AppID || instance.State != string(StateRunning) {
				continue
			}
			if ready == nil || instance.StartedAt.Before(ready.StartedAt) ||
				(instance.StartedAt.Equal(ready.StartedAt) && instance.ID < ready.ID) {
				copy := instance
				ready = &copy
			}
		}
		if ready != nil && m.completeReadyAppWakeTransitionLocked(transition.ID, ready.ID, ready.WakeID) {
			completed++
		}
	}
	return completed, nil
}

func (m *MemStore) completeReadyAppWakeTransitionLocked(transitionID, instanceID, wakeID string) bool {
	transition, ok := m.appWakeTransitions[transitionID]
	if !ok || transition.CompletedAt != nil || transition.SupersededAt != nil || wakeID == "" ||
		m.appWakeTransitionByApp[transition.AppID] != transitionID {
		return false
	}
	app, ok := m.apps[transition.AppID]
	if !ok || app.Status != AppActive {
		return false
	}
	account, ok := m.accounts[app.AccountID]
	if !ok || !account.Active() {
		return false
	}
	instance, ok := m.instances[instanceID]
	if !ok || instance.AppID != app.ID || instance.State != string(StateRunning) || instance.WakeID != wakeID {
		return false
	}
	now := time.Now().UTC()
	var recipients []string
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == app.ID && hook.AccountID == app.AccountID && hook.Enabled &&
			(len(hook.EventFilter) == 0 || slices.Contains(hook.EventFilter, string(AppWebhookEventAppWoken))) {
			recipients = append(recipients, hook.ID)
		}
	}
	if len(recipients) > 0 {
		payload, err := appWokenWebhookPayload(app, instanceID, wakeID, now)
		if err != nil {
			return false
		}
		sort.Strings(recipients)
		outboxID := uuid.NewString()
		m.appWebhookEventOutbox[outboxID] = appWebhookOutboxEvent{
			ID: outboxID, AccountID: app.AccountID, AppID: app.ID,
			Event: AppWebhookEventAppWoken, SourceID: transitionID,
			Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: now,
		}
	}
	transition.CompletedAt = &now
	transition.InstanceID = instanceID
	transition.WakeID = wakeID
	m.appWakeTransitions[transitionID] = transition
	return true
}

func (m *MemStore) clearCurrentAppWakeTransitionLocked(appID string) {
	if id := m.appWakeTransitionByApp[appID]; id != "" {
		m.supersedeAppWakeTransitionLocked(id)
		delete(m.appWakeTransitionByApp, appID)
	}
}

func (m *MemStore) supersedeAppWakeTransitionLocked(id string) {
	transition, ok := m.appWakeTransitions[id]
	if !ok || transition.CompletedAt != nil || transition.SupersededAt != nil {
		return
	}
	now := time.Now().UTC()
	transition.SupersededAt = &now
	m.appWakeTransitions[id] = transition
}
