package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type MCPReadinessReminderDefaultsStore interface {
	GetMCPReadinessReminderDefaults(context.Context, string, string, string) (api.MCPReadinessReminderDefaults, error)
	SetMCPReadinessReminderDefaults(context.Context, string, string, string, int64, api.MCPReadinessReminderPolicy) (api.MCPReadinessReminderDefaults, error)
}

func readinessReminderDefaultsAttention(defaults api.MCPReadinessReminderDefaults) readinessAttentionRecord {
	version := defaults.Version
	return readinessAttentionRecord{Policy: defaults.ReminderPolicy, PolicySource: "rule_default", DefaultsVersion: &version}
}
func (m *MemStore) GetMCPReadinessReminderDefaults(_ context.Context, account, app, id string) (api.MCPReadinessReminderDefaults, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessReminderDefaults{}, ErrNotFound
	}
	rule, ok := m.alertRules[id]
	if !ok {
		return api.MCPReadinessReminderDefaults{}, ErrNotFound
	}
	if err := readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	return m.readinessReminderDefaults[id], nil
}
func (m *MemStore) SetMCPReadinessReminderDefaults(_ context.Context, account, app, id string, expected int64, policy api.MCPReadinessReminderPolicy) (api.MCPReadinessReminderDefaults, error) {
	if api.ValidateMCPReadinessReminderDefaultsRequest(api.SetMCPReadinessReminderDefaultsRequest{ExpectedVersion: &expected, ReminderPolicy: &policy}) != nil {
		return api.MCPReadinessReminderDefaults{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessReminderDefaults{}, ErrNotFound
	}
	rule, ok := m.alertRules[id]
	if !ok {
		return api.MCPReadinessReminderDefaults{}, ErrNotFound
	}
	if err := readinessWebhookRule(rule, account, app); err != nil {
		return api.MCPReadinessReminderDefaults{}, err
	}
	if m.readinessReminderDefaults[id].Version != expected {
		return api.MCPReadinessReminderDefaults{}, ErrConflict
	}
	out := api.MCPReadinessReminderDefaults{Version: expected + 1, ReminderPolicy: policy}
	m.readinessReminderDefaults[id] = out
	return out, nil
}
