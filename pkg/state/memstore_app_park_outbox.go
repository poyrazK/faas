package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

type memAppParkTransition struct {
	AppParkTransition
	RequestedAt  time.Time
	CompletedAt  *time.Time
	SupersededAt *time.Time
}

func (m *MemStore) BeginAppParkTransition(_ context.Context, appID string, expected AppStatus) (AppParkTransition, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		return AppParkTransition{}, false, ErrNotFound
	}
	if app.Status == AppEvictedCold {
		if id := m.appParkTransitionByApp[appID]; id != "" {
			return AppParkTransition{ID: id, AppID: appID}, false, nil
		}
	} else if app.Status != expected {
		return AppParkTransition{}, false, nil
	}

	for id, previous := range m.appParkTransitions {
		if previous.AppID == appID {
			delete(m.appParkTransitions, id)
		}
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	transition := memAppParkTransition{
		AppParkTransition: AppParkTransition{ID: id, AppID: appID},
		RequestedAt:       now,
	}
	m.clearCurrentAppWakeTransitionLocked(appID)
	m.appParkTransitions[id] = transition
	m.appParkTransitionByApp[appID] = id
	changed := app.Status != AppEvictedCold
	app.Status = AppEvictedCold
	m.apps[appID] = app
	return transition.AppParkTransition, changed, nil
}

func (m *MemStore) CompleteDrainedAppParkTransition(_ context.Context, transitionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.completeDrainedAppParkTransitionLocked(transitionID), nil
}

func (m *MemStore) DrainDrainedAppParkTransitions(_ context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, transition := range m.appParkTransitions {
		if transition.CompletedAt != nil || transition.SupersededAt != nil {
			continue
		}
		app, exists := m.apps[transition.AppID]
		if !exists || app.Status != AppEvictedCold || m.appParkTransitionByApp[transition.AppID] != id {
			now := time.Now().UTC()
			transition.SupersededAt = &now
			m.appParkTransitions[id] = transition
		}
	}
	transitions := make([]memAppParkTransition, 0, len(m.appParkTransitions))
	for _, transition := range m.appParkTransitions {
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
		if m.completeDrainedAppParkTransitionLocked(transition.ID) {
			completed++
		}
	}
	return completed, nil
}

func (m *MemStore) completeDrainedAppParkTransitionLocked(transitionID string) bool {
	transition, ok := m.appParkTransitions[transitionID]
	if !ok || transition.CompletedAt != nil || transition.SupersededAt != nil ||
		m.appParkTransitionByApp[transition.AppID] != transitionID {
		return false
	}
	app, ok := m.apps[transition.AppID]
	if !ok || app.Status != AppEvictedCold {
		return false
	}
	for _, instance := range m.instances {
		if instance.AppID == app.ID && IsLive(instance.State) {
			return false
		}
	}

	now := time.Now().UTC()
	var recipients []string
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == app.ID && hook.AccountID == app.AccountID && hook.Enabled &&
			(len(hook.EventFilter) == 0 || slices.Contains(hook.EventFilter, string(AppWebhookEventAppParked))) {
			recipients = append(recipients, hook.ID)
		}
	}
	if len(recipients) > 0 {
		payload, err := appParkedWebhookPayload(app, now)
		if err != nil {
			return false
		}
		sort.Strings(recipients)
		outboxID := uuid.NewString()
		m.appWebhookEventOutbox[outboxID] = appWebhookOutboxEvent{
			ID: outboxID, AccountID: app.AccountID, AppID: app.ID,
			Event: AppWebhookEventAppParked, SourceID: transitionID,
			Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: now,
		}
	}
	transition.CompletedAt = &now
	m.appParkTransitions[transitionID] = transition
	return true
}

func (m *MemStore) clearCurrentAppParkTransitionLocked(appID string) {
	id := m.appParkTransitionByApp[appID]
	if id == "" {
		return
	}
	if transition, ok := m.appParkTransitions[id]; ok && transition.CompletedAt == nil && transition.SupersededAt == nil {
		now := time.Now().UTC()
		transition.SupersededAt = &now
		m.appParkTransitions[id] = transition
	}
	delete(m.appParkTransitionByApp, appID)
}
