package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIAccessTokenStore = (*MemStore)(nil)

func (m *MemStore) CreateOutboundAIAccessToken(_ context.Context, token OutboundAIAccessToken) (OutboundAIAccessToken, error) {
	if len(token.TokenHash) != 32 || len(token.IntegrationIDs) > api.OutboundAIAccessTokenIntegrationsMax {
		return OutboundAIAccessToken{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIAccessTokens == nil {
		m.outboundAIAccessTokens = make(map[string]OutboundAIAccessToken)
	}
	now := time.Now().UTC()
	live := 0
	for _, existing := range m.outboundAIAccessTokens {
		if existing.AppID != token.AppID || existing.RevokedAt != nil {
			continue
		}
		if existing.Name == token.Name {
			return OutboundAIAccessToken{}, ErrConflict
		}
		if existing.ExpiresAt.After(now) {
			live++
		}
	}
	if live >= api.OutboundAIAccessTokensPerApp {
		return OutboundAIAccessToken{}, ErrOutboundAIAccessTokenLimit
	}
	for _, id := range token.IntegrationIDs {
		offer, ok := m.outboundIntegrationOffers[id]
		_, bound := m.outboundAppBindings[token.AppID+"|"+id]
		if !ok || offer.AccountID != token.AccountID || !offer.Enabled || offer.AIProvider == "" || offer.OwnerKind != "customer" || !bound {
			return OutboundAIAccessToken{}, ErrInvalidArgument
		}
	}
	token.CreatedAt = now
	token.IntegrationIDs = append([]string{}, token.IntegrationIDs...)
	m.outboundAIAccessTokens[token.ID] = token
	token.TokenHash = nil
	return token, nil
}

func (m *MemStore) ListOutboundAIAccessTokens(_ context.Context, accountID, appID string) ([]OutboundAIAccessToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundAIAccessToken, 0)
	for _, token := range m.outboundAIAccessTokens {
		if token.AccountID == accountID && token.AppID == appID {
			token.TokenHash = nil
			token.IntegrationIDs = append([]string{}, token.IntegrationIDs...)
			out = append(out, token)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *MemStore) RevokeOutboundAIAccessToken(_ context.Context, accountID, appID, tokenID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	token, ok := m.outboundAIAccessTokens[tokenID]
	if !ok || token.AccountID != accountID || token.AppID != appID || token.RevokedAt != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	token.RevokedAt = &now
	m.outboundAIAccessTokens[tokenID] = token
	return nil
}
