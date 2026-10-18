package state

import (
	"context"
	"time"
)

func (m *MemStore) RecordMCPReadinessObservation(_ context.Context, o MCPReadinessObservation) (MCPReadinessTransition, error) {
	if err := validateReadinessObservation(o); err != nil {
		return MCPReadinessTransition{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[o.Rule.ID]
	if !ok {
		return MCPReadinessTransition{}, ErrNotFound
	}
	if !sameReadinessObservationRule(rule, o.Rule) {
		return MCPReadinessTransition{}, ErrConflict
	}
	if !o.At.After(m.readinessIncidentCursors[rule.ID]) {
		return MCPReadinessTransition{}, nil
	}
	windows := m.readinessMaintenanceWindowsLocked(rule.AccountID, rule.AppID)
	events := m.suppressReadinessQueueLocked(rule, windows, o.At)
	var active *readinessIncident
	for _, v := range m.readinessIncidents {
		if v.RuleID == rule.ID && v.ResolvedAt == nil {
			copy := v
			active = &copy
			break
		}
	}
	if active != nil && (active.AccountID != rule.AccountID || active.AppID != rule.AppID) {
		return MCPReadinessTransition{}, ErrConflict
	}
	if active != nil && readinessMaintenanceSince(rule, o.Payload, windows, m.readinessIncidentCursors[rule.ID], o.At) {
		active.MaintenancePending = true
	}
	event, incident, to, err := readinessMaintenanceTransition(rule, o, active, events, windows)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	m.readinessIncidentCursors[rule.ID] = o.At
	rule.State = to
	rule.LastEvaluatedAt = o.At
	rule.UpdatedAt = o.At
	out := MCPReadinessTransition{}
	if incident != nil {
		m.readinessIncidents[incident.ID] = *incident
	}
	if event != nil {
		if active == nil && o.Breached {
			m.readinessAttention[incident.ID] = readinessReminderDefaultsAttention(m.readinessReminderDefaults[rule.ID])
		}
		m.readinessIncidents[incident.ID] = *incident
		m.readinessIncidentSequence++
		event.Sequence = m.readinessIncidentSequence
		m.readinessIncidentEvents[event.ID] = *event
		out = MCPReadinessTransition{EventID: event.ID, IncidentID: incident.ID, Event: event.Kind}
		if o.Breached {
			rule.LastFiredAt = o.At
		}
	}
	reminder, err := m.recordReadinessAttentionLocked(rule, incident, o, event, windows)
	if err != nil {
		return MCPReadinessTransition{}, err
	}
	if reminder != nil {
		out = MCPReadinessTransition{EventID: reminder.ID, IncidentID: reminder.IncidentID, Event: reminder.Kind}
		rule.LastFiredAt = o.At
	}
	m.alertRules[rule.ID] = rule
	return out, nil
}
func (m *MemStore) ClaimMCPReadinessDelivery(_ context.Context, ruleID string, now time.Time) (MCPReadinessDeliveryClaim, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rule, ok := m.alertRules[ruleID]
	if !ok {
		return MCPReadinessDeliveryClaim{}, false, ErrNotFound
	}
	if !rule.Enabled || !sameReadinessObservationRule(rule, rule) {
		return MCPReadinessDeliveryClaim{}, false, nil
	}
	windows := m.readinessMaintenanceWindowsLocked(rule.AccountID, rule.AppID)
	m.suppressReadinessQueueLocked(rule, windows, now)
	m.reconcileReadinessRemindersLocked(rule, now)
	events, incidents := m.readinessOutstandingLocked(rule)
	policy := m.readinessDeliveryPolicyLocked(rule.ID)
	for _, c := range readinessDestinationPlan(rule, policy, events, incidents) {
		if !readyReadinessDestination(c, now) {
			continue
		}
		claim := claimReadinessDestination(rule, policy, &c, now)
		incident := incidents[c.Event.IncidentID]
		incident.Routing = c.Route
		m.readinessIncidents[incident.ID] = incident
		m.readinessIncidentEvents[c.Event.ID] = c.Event
		m.alertDeliveries[claim.Delivery.IdempotencyKey] = claim.Delivery
		return claim, true, nil
	}
	return MCPReadinessDeliveryClaim{}, false, nil
}
func (m *MemStore) readinessOutstandingLocked(rule AlertRule) ([]readinessIncidentEventRecord, map[string]readinessIncident) {
	events := []readinessIncidentEventRecord{}
	incidents := map[string]readinessIncident{}
	for _, e := range m.readinessIncidentEvents {
		i := m.readinessIncidents[e.IncidentID]
		if e.RuleID == rule.ID && i.AccountID == rule.AccountID && i.AppID == rule.AppID && !readinessNotificationTerminal(e.Status) {
			events = append(events, e)
			incidents[i.ID] = i
		}
	}
	return events, incidents
}

func (m *MemStore) CompleteMCPReadinessDelivery(ctx context.Context, eventID, token string, now time.Time) error {
	return m.completeReadinessDelivery(ctx, eventID, token, now, nil)
}
func (m *MemStore) completeReadinessDelivery(ctx context.Context, eventID, token string, now time.Time, result *MCPReadinessProviderResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	event, ok := m.readinessIncidentEvents[eventID]
	if !ok {
		return ErrNotFound
	}
	event = cloneReadiness(event)
	incident := m.readinessIncidents[event.IncidentID]
	prepareReadinessDestinations(&event, incident.Routing)
	destination, target, err := readinessDestinationForToken(event, token, now)
	if err != nil {
		return err
	}
	var delivery AlertDelivery
	var deliveryKey string
	for k, v := range m.alertDeliveries {
		if v.ID == target.DeliveryID {
			delivery = v
			deliveryKey = k
			break
		}
	}
	if deliveryKey == "" {
		return ErrNotFound
	}
	if result != nil {
		if target.Provider != "pagerduty" {
			return ErrConflict
		}
		delivery = applyReadinessProviderResult(delivery, *result, now)
		m.alertDeliveries[deliveryKey] = delivery
	}
	if delivery.Status == AlertDeliveryPending {
		delivery.Status = AlertDeliveryFailed
		delivery.LastError = "dispatch incomplete"
		m.alertDeliveries[deliveryKey] = delivery
	}
	readinessProviderUncertain(&incident.Routing, event, destination, result)
	finishReadinessDestination(&incident.Routing, &event, destination, delivery, now, m.alertRules[event.RuleID].CooldownMinutes)
	readinessProviderRetry(&event, destination, result)
	m.readinessIncidents[incident.ID] = incident
	event.LastStatusCode = delivery.LastStatusCode
	m.readinessIncidentEvents[event.ID] = event
	return nil
}
