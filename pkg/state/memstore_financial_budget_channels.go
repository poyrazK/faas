package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// SetFinancialBudgetChannels mirrors PgStore, keeping only the policy
// account's channels.
func (m *MemStore) SetFinancialBudgetChannels(_ context.Context, account, policy string, channelIDs []string) error {
	if !validFinancialBudgetChannelIDs(account, policy, channelIDs) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.financialBudgets[policy]
	if !ok || p.AccountID != account || p.DeletedAt != nil {
		return ErrNotFound
	}
	owned := map[string]bool{}
	for _, c := range m.notificationChannels[account] {
		owned[c.ID] = true
	}
	kept := []string{}
	for _, id := range channelIDs {
		if owned[id] {
			kept = append(kept, id)
			owned[id] = false
		}
	}
	if m.financialBudgetChannels == nil {
		m.financialBudgetChannels = map[string][]string{}
	}
	m.financialBudgetChannels[policy] = kept
	return nil
}

// ListFinancialBudgetChannels mirrors PgStore; a deleted channel drops out.
func (m *MemStore) ListFinancialBudgetChannels(_ context.Context, account, policy string) ([]NotificationChannel, error) {
	if !validFinancialBudgetChannelIDs(account, policy, nil) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.financialBudgetChannelsLocked(account, policy), nil
}

func (m *MemStore) financialBudgetChannelsLocked(account, policy string) []NotificationChannel {
	bound := map[string]bool{}
	for _, id := range m.financialBudgetChannels[policy] {
		bound[id] = true
	}
	out := []NotificationChannel{}
	for _, c := range m.notificationChannels[account] {
		if bound[c.ID] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name || out[i].Name == out[j].Name && out[i].ID < out[j].ID
	})
	return out
}

func financialBudgetChannelDeliveryKey(alertID, channelID string) string {
	return alertID + "\n" + channelID
}

// enqueueFinancialBudgetAlertChannelsLocked mirrors the Postgres enqueue in
// RecordFinancialBudgetEvaluation.
func (m *MemStore) enqueueFinancialBudgetAlertChannelsLocked(alert FinancialBudgetAlertDelivery, now time.Time) {
	for _, c := range m.financialBudgetChannelsLocked(alert.AccountID, alert.PolicyID) {
		key := financialBudgetChannelDeliveryKey(alert.ID, c.ID)
		if _, exists := m.financialBudgetChannelDeliveries[key]; exists {
			continue
		}
		if m.financialBudgetChannelDeliveries == nil {
			m.financialBudgetChannelDeliveries = map[string]FinancialBudgetAlertChannelDelivery{}
		}
		d := FinancialBudgetAlertChannelDelivery{State: "pending", NextAttemptAt: now.UTC()}
		d.Alert.ID, d.Alert.AccountID, d.Alert.PolicyID, d.Channel.ID = alert.ID, alert.AccountID, alert.PolicyID, c.ID
		m.financialBudgetChannelDeliveries[key] = d
	}
}

func (m *MemStore) ClaimFinancialBudgetAlertChannel(_ context.Context, now time.Time) (FinancialBudgetAlertChannelDelivery, error) {
	if now.IsZero() {
		return FinancialBudgetAlertChannelDelivery{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var next FinancialBudgetAlertChannelDelivery
	var nextKey string
	for key, d := range m.financialBudgetChannelDeliveries {
		alert, ok := m.financialBudgetAlerts[d.Alert.ID]
		p := m.financialBudgets[d.Alert.PolicyID]
		channel, owned := m.notificationChannelLocked(d.Alert.AccountID, d.Channel.ID)
		if !ok || !owned || !slices.Contains(m.financialBudgetChannels[d.Alert.PolicyID], d.Channel.ID) || p.DeletedAt != nil || p.Revision != alert.PolicyRevision || !p.Spec.Enabled || !financial.Monitored(p.Spec) ||
			d.State == "delivered" || d.State == "abandoned" || d.NextAttemptAt.After(now) {
			continue
		}
		if nextKey == "" || d.NextAttemptAt.Before(next.NextAttemptAt) || d.NextAttemptAt.Equal(next.NextAttemptAt) && key < nextKey {
			alert.LeaseToken, alert.Spec = "", p.Spec
			d.Alert, d.Channel = alert, channel
			next, nextKey = d, key
		}
	}
	if nextKey == "" {
		return next, ErrNotFound
	}
	next.State, next.LeaseToken = "sending", uuid.NewString()
	next.Attempts = min(next.Attempts+1, api.FinancialBudgetAlertAttemptsMax)
	next.NextAttemptAt = now.UTC().Add(api.FinancialBudgetAlertLease)
	m.financialBudgetChannelDeliveries[nextKey] = next
	return cloneFinancialBudgetAlertChannelDelivery(next), nil
}

func (m *MemStore) notificationChannelLocked(account, id string) (NotificationChannel, bool) {
	for _, c := range m.notificationChannels[account] {
		if c.ID == id {
			return c, true
		}
	}
	return NotificationChannel{}, false
}

func (m *MemStore) SettleFinancialBudgetAlertChannel(_ context.Context, alertID, channelID, token string, now time.Time, outcome string) error {
	if uuid.Validate(alertID) != nil || uuid.Validate(channelID) != nil || uuid.Validate(token) != nil || now.IsZero() || !validFinancialBudgetChannelOutcome(outcome) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := financialBudgetChannelDeliveryKey(alertID, channelID)
	d, ok := m.financialBudgetChannelDeliveries[key]
	if !ok || d.State != "sending" || d.LeaseToken != token || !now.Before(d.NextAttemptAt) {
		return ErrConflict
	}
	d.LeaseToken = ""
	d.State, d.LastError, d.NextAttemptAt, d.DeliveredAt = financialBudgetChannelSettlement(d.Attempts, now, outcome)
	m.financialBudgetChannelDeliveries[key] = d
	return nil
}
