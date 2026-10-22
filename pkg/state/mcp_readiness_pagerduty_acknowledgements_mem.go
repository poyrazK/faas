package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) pagerDutyAcknowledgementRuleLocked(account, app, id string) (AlertRule, error) {
	rule, ok := m.alertRules[id]
	if !ok || !m.readinessAppLocked(account, app) {
		return rule, ErrNotFound
	}
	return rule, readinessWebhookRule(rule, account, app)
}
func (m *MemStore) GetMCPReadinessPagerDutyAcknowledgements(_ context.Context, account, app, id string, destinations ...string) (api.MCPReadinessPagerDutyAcknowledgementPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.pagerDutyAcknowledgementRuleLocked(account, app, id); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	destination, err := readinessPagerDutyAcknowledgementSelector(destinations)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	config := m.readinessPagerDutyAcknowledgementLocked(id, destination)
	if destination != "" {
		config.LegacyReceiver = false
	}
	return readinessPagerDutyAcknowledgementView(id, config), nil
}
func (m *MemStore) SetMCPReadinessPagerDutyAcknowledgements(_ context.Context, account, app, id string, expected int64, config MCPReadinessPagerDutyAcknowledgementConfig) (api.MCPReadinessPagerDutyAcknowledgementPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.pagerDutyAcknowledgementRuleLocked(account, app, id); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	destination := config.Destination
	if config.Enabled && destination == "" {
		destination = "primary"
	}
	old := m.readinessPagerDutyAcknowledgementLocked(id, destination)
	config.Destination, config.LegacyReceiver = readinessPagerDutyAcknowledgementDestination(old), old.LegacyReceiver
	if old.Version != expected {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, ErrConflict
	}
	config, err := bindReadinessPagerDutyAcknowledgements(expected, config, m.readinessDeliveryPolicyLocked(id))
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementPolicy{}, err
	}
	m.readinessPagerDutyAckPolicies[id+":"+config.Destination] = config
	return readinessPagerDutyAcknowledgementView(id, config), nil
}
func (m *MemStore) ReadMCPReadinessPagerDutyAcknowledgementReceiver(_ context.Context, id string, destinations ...string) (MCPReadinessPagerDutyAcknowledgementConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[id]
	destination, err := readinessPagerDutyAcknowledgementSelector(destinations)
	if err != nil {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, err
	}
	config := m.readinessPagerDutyAcknowledgementLocked(id, destination)
	if !ok || !config.Enabled {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, ErrNotFound
	}
	if _, err := m.pagerDutyAcknowledgementRuleLocked(rule.AccountID, rule.AppID, id); err != nil {
		return MCPReadinessPagerDutyAcknowledgementConfig{}, err
	}
	config.AccountID, config.AppID = rule.AccountID, rule.AppID
	config.SigningSecretSealed = append([]byte{}, config.SigningSecretSealed...)
	return config, nil
}
func (m *MemStore) ApplyMCPReadinessPagerDutyAcknowledgement(_ context.Context, ruleID string, version int64, e MCPReadinessPagerDutyAcknowledgementInput, now time.Time) (api.MCPReadinessPagerDutyAcknowledgementEvent, error) {
	if err := validateReadinessPagerDutyAcknowledgement(e, now); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	e.IncidentID = uuid.MustParse(e.IncidentID).String()
	e.OccurredAt = e.OccurredAt.UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[ruleID]
	if !ok {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrNotFound
	}
	if _, err := m.pagerDutyAcknowledgementRuleLocked(rule.AccountID, rule.AppID, ruleID); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	if _, err := readinessPagerDutyAcknowledgementSelector([]string{e.BindingDestination}); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, err
	}
	config := m.readinessPagerDutyAcknowledgementLocked(ruleID, e.BindingDestination)
	if !config.Enabled || config.Version != version || e.RequireLegacyReceiver && !config.LegacyReceiver {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrConflict
	}
	config.AccountID, config.AppID = rule.AccountID, rule.AppID
	incident, ok := m.readinessIncidents[e.IncidentID]
	if !ok || incident.RuleID != ruleID || !readinessPagerDutyAcknowledgementMatches(config, incident, e) {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrNotFound
	}

	if e.Action == "release" && !config.SyncUnacknowledgements {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, nil
	}
	base := ruleID + ":" + e.EventID
	key := base
	delivery := readinessPagerDutyAcknowledgementDelivery(e, config)
	if delivery != "" {
		key += ":" + delivery
	}
	for _, candidate := range []string{base, base + ":primary", base + ":fallback"} {
		if old, ok := m.readinessPagerDutyAckEvents[candidate]; ok {
			if readinessPagerDutyAcknowledgementDigest(old.Input) != readinessPagerDutyAcknowledgementDigest(e) {
				return api.MCPReadinessPagerDutyAcknowledgementEvent{}, ErrConflict
			}
			if candidate == key {
				return cloneReadiness(old.Receipt), nil
			}
		}
	}

	count := 0
	for _, old := range m.readinessPagerDutyAckEvents {
		if old.Receipt.IncidentID == e.IncidentID {
			count++
		}
	}
	if count >= api.MCPReadinessPagerDutyWebhookEventsPerIncident {
		return api.MCPReadinessPagerDutyAcknowledgementEvent{}, nil
	}
	attention, receipt, action, err := decideReadinessPagerDutyAcknowledgement(incident, m.readinessAttention[incident.ID], config, e, now)
	if err != nil {
		return receipt, err
	}
	if action != nil {
		actionKey := rule.AppID + ":" + action.Receipt.RequestID
		if _, exists := m.readinessAttentionActions[actionKey]; exists {
			return receipt, ErrConflict
		}
		m.readinessAttentionActions[actionKey] = cloneReadiness(*action)
	}
	m.readinessAttention[incident.ID] = attention
	if action != nil {
		m.reconcileReadinessRemindersLocked(rule, now)
	}
	m.readinessPagerDutyAckEvents[key] = readinessPagerDutyAcknowledgementRecord{RuleID: ruleID, Input: e, Receipt: receipt}
	return cloneReadiness(receipt), nil
}
func (m *MemStore) ListMCPReadinessPagerDutyAcknowledgementEvents(_ context.Context, account, app, rule, before string, limit int) (api.MCPReadinessPagerDutyAcknowledgementEvents, error) {
	at, id, err := validateReadinessIncidentList("", before, limit)
	if err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.pagerDutyAcknowledgementRuleLocked(account, app, rule); err != nil {
		return api.MCPReadinessPagerDutyAcknowledgementEvents{}, err
	}
	out := api.MCPReadinessPagerDutyAcknowledgementEvents{Events: []api.MCPReadinessPagerDutyAcknowledgementEvent{}}
	for _, old := range m.readinessPagerDutyAckEvents {
		r := old.Receipt
		if old.RuleID == rule && (at.IsZero() || r.ReceivedAt.Before(at) || r.ReceivedAt.Equal(at) && r.ID < id) {
			out.Events = append(out.Events, cloneReadiness(r))
		}
	}
	sort.Slice(out.Events, func(a, b int) bool {
		if out.Events[a].ReceivedAt.Equal(out.Events[b].ReceivedAt) {
			return out.Events[a].ID > out.Events[b].ID
		}
		return out.Events[a].ReceivedAt.After(out.Events[b].ReceivedAt)
	})
	if len(out.Events) > limit {
		out.Events = out.Events[:limit]
		last := out.Events[limit-1]
		out.NextBefore = api.MCPReadinessIncidentCursor(last.ReceivedAt, last.ID)
	}
	return out, nil
}

func (m *MemStore) readinessPagerDutyAcknowledgementLocked(id, destination string) MCPReadinessPagerDutyAcknowledgementConfig {
	legacy := MCPReadinessPagerDutyAcknowledgementConfig{Destination: "primary", LegacyReceiver: true}
	for _, d := range []string{"primary", "fallback"} {
		if c, ok := m.readinessPagerDutyAckPolicies[id+":"+d]; ok && c.LegacyReceiver {
			legacy = c
			break
		}
	}
	if destination == "" {
		return legacy
	}
	if c, ok := m.readinessPagerDutyAckPolicies[id+":"+destination]; ok {
		return c
	}
	return MCPReadinessPagerDutyAcknowledgementConfig{Destination: destination, LegacyReceiver: legacy.Version == 0}
}
