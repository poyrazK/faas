package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIControlStore = (*MemStore)(nil)

// SetOutboundBindingAIGuardrails mirrors the PgStore update.
func (m *MemStore) SetOutboundBindingAIGuardrails(_ context.Context, accountID, appID, integrationID string, guard OutboundAIGuardrails) error {
	if !guard.valid() {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[integrationID]
	if !ok || offer.AccountID != accountID || offer.AIProvider == "" {
		return ErrNotFound
	}
	key := appID + "|" + integrationID
	binding, ok := m.outboundAppBindings[key]
	if !ok || binding.AccountID != accountID {
		return ErrNotFound
	}
	binding.BindingAIRequestsPerMinute = copyInt64Ptr(guard.RequestsPerMinute)
	binding.BindingAITokensPerMinute = copyInt64Ptr(guard.TokensPerMinute)
	binding.BindingAIMaxOutputTokens = copyInt64Ptr(guard.MaxOutputTokens)
	binding.BindingAIAllowedModels = nil
	if guard.AllowedModels != nil {
		binding.BindingAIAllowedModels = append([]string{}, guard.AllowedModels...)
	}
	m.outboundAppBindings[key] = binding
	return nil
}

func (m *MemStore) memOutboundIntegrationOwned(accountID, integrationID string) bool {
	offer, ok := m.outboundIntegrationOffers[integrationID]
	return ok && offer.AccountID == accountID
}

func (m *MemStore) ListOutboundCredentialKeys(_ context.Context, accountID, integrationID string) ([]OutboundCredentialKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, ErrNotFound
	}
	keys := make([]OutboundCredentialKey, 0, len(m.outboundCredentialKeys[integrationID]))
	for _, key := range m.outboundCredentialKeys[integrationID] {
		key.AuthorizationSealed = nil
		keys = append(keys, key)
	}
	return keys, nil
}

func (m *MemStore) CreateOutboundCredentialKey(_ context.Context, key OutboundCredentialKey) (OutboundCredentialKey, error) {
	if !api.ValidOutboundCredentialKeyLabel(key.Label) || len(key.AuthorizationSealed) == 0 {
		return OutboundCredentialKey{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	offer, ok := m.outboundIntegrationOffers[key.IntegrationID]
	if !ok || offer.AccountID != key.AccountID || offer.CredentialSource != "customer_sealed" || !offer.Enabled {
		return OutboundCredentialKey{}, ErrNotFound
	}
	existing := m.outboundCredentialKeys[key.IntegrationID]
	if len(existing) >= api.OutboundIntegrationCredentialKeysMax {
		return OutboundCredentialKey{}, ErrOutboundCredentialKeyLimit
	}
	for _, other := range existing {
		if other.Label == key.Label {
			return OutboundCredentialKey{}, ErrConflict
		}
	}
	if m.outboundCredentialKeys == nil {
		m.outboundCredentialKeys = make(map[string][]OutboundCredentialKey)
	}
	key.CreatedAt = time.Now().UTC()
	key.AuthorizationSealed = append([]byte(nil), key.AuthorizationSealed...)
	m.outboundCredentialKeys[key.IntegrationID] = append(existing, key)
	key.AuthorizationSealed = nil
	return key, nil
}

func (m *MemStore) DeleteOutboundCredentialKey(_ context.Context, accountID, integrationID, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return ErrNotFound
	}
	keys := m.outboundCredentialKeys[integrationID]
	for i, key := range keys {
		if key.ID == keyID {
			m.outboundCredentialKeys[integrationID] = append(keys[:i:i], keys[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemStore) ListOutboundAIBudgetAlerts(_ context.Context, accountID, integrationID string, limit int) ([]OutboundAIBudgetAlert, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, ErrNotFound
	}
	alerts := append([]OutboundAIBudgetAlert{}, m.outboundAIBudgetAlerts[integrationID]...)
	sort.SliceStable(alerts, func(i, j int) bool { return alerts[i].CrossedAt.After(alerts[j].CrossedAt) })
	if page := boundedPage(limit, api.OutboundAIBudgetAlertsPageMax); len(alerts) > page {
		alerts = alerts[:page]
	}
	return alerts, nil
}

func (m *MemStore) ListOutboundAIRequestLog(_ context.Context, accountID, integrationID string, before *time.Time, limit int) ([]OutboundAIRequestLogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return nil, ErrNotFound
	}
	entries := make([]OutboundAIRequestLogEntry, 0)
	for _, entry := range m.outboundAIRequestLog[integrationID] {
		if before == nil || entry.CreatedAt.Before(*before) {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	if page := boundedPage(limit, api.OutboundAIRequestLogPageMax); len(entries) > page {
		entries = entries[:page]
	}
	return entries, nil
}

// SeedOutboundAIBudgetAlert and SeedOutboundAIRequestLog stand in for
// outboundd's writes in tests.
func (m *MemStore) SeedOutboundAIBudgetAlert(alert OutboundAIBudgetAlert) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIBudgetAlerts == nil {
		m.outboundAIBudgetAlerts = make(map[string][]OutboundAIBudgetAlert)
	}
	m.outboundAIBudgetAlerts[alert.IntegrationID] = append(m.outboundAIBudgetAlerts[alert.IntegrationID], alert)
}

func (m *MemStore) SeedOutboundAIRequestLog(integrationID string, entries ...OutboundAIRequestLogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIRequestLog == nil {
		m.outboundAIRequestLog = make(map[string][]OutboundAIRequestLogEntry)
	}
	m.outboundAIRequestLog[integrationID] = append(m.outboundAIRequestLog[integrationID], entries...)
}

var _ OutboundAIRequestCallerStore = (*MemStore)(nil)

// OutboundAIRequestCaller mirrors the PgStore read of a logged call's caller.
func (m *MemStore) OutboundAIRequestCaller(_ context.Context, accountID, integrationID, logID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return "", ErrNotFound
	}
	for _, entry := range m.outboundAIRequestLog[integrationID] {
		if entry.ID == logID {
			return entry.Caller, nil
		}
	}
	return "", ErrNotFound
}

var _ OutboundAIContentStore = (*MemStore)(nil)

// GetOutboundAIRequestContent mirrors the PgStore read of a captured call
// (ADR-996).
func (m *MemStore) GetOutboundAIRequestContent(_ context.Context, accountID, integrationID, logID string) (OutboundAIRequestContent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.memOutboundIntegrationOwned(accountID, integrationID) {
		return OutboundAIRequestContent{}, ErrNotFound
	}
	content, ok := m.outboundAIRequestContent[integrationID+"/"+logID]
	if !ok {
		return OutboundAIRequestContent{}, ErrNotFound
	}
	return content, nil
}

// SeedOutboundAIRequestContent stands in for outboundd's capture in tests
// and marks the logged call as captured.
func (m *MemStore) SeedOutboundAIRequestContent(integrationID string, content OutboundAIRequestContent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIRequestContent == nil {
		m.outboundAIRequestContent = make(map[string]OutboundAIRequestContent)
	}
	m.outboundAIRequestContent[integrationID+"/"+content.LogID] = content
	for i, entry := range m.outboundAIRequestLog[integrationID] {
		if entry.ID == content.LogID {
			m.outboundAIRequestLog[integrationID][i].ContentCaptured = true
		}
	}
}
