package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreatePlatformTenantAccessToken(_ context.Context, in PlatformTenantAccessTokenInput) (PlatformTenantAccessToken, error) {
	if err := validatePlatformTenantAccessTokenInput(in); err != nil {
		return PlatformTenantAccessToken{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[in.TenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return PlatformTenantAccessToken{}, ErrNotFound
	}
	name := strings.ToLower(in.Name)
	active := 0
	now := time.Now().UTC()
	for _, token := range m.platformTenantAccessTokens {
		if token.AccountID != in.AccountID || token.TenantID != in.TenantID {
			continue
		}
		if token.RevokedAt == nil && now.Before(token.ExpiresAt) {
			active++
			if strings.ToLower(token.Name) == name {
				return PlatformTenantAccessToken{}, ErrConflict
			}
		}
	}
	if active >= PlatformTenantAccessTokenLimit {
		return PlatformTenantAccessToken{}, &PlatformTenantAccessTokenQuotaError{Limit: PlatformTenantAccessTokenLimit, Observed: active}
	}
	if _, exists := m.platformTenantAccessTokenByHash[string(in.TokenHash)]; exists {
		return PlatformTenantAccessToken{}, ErrConflict
	}
	token := PlatformTenantAccessToken{ID: uuid.NewString(), AccountID: in.AccountID, TenantID: in.TenantID,
		Name: in.Name, Prefix: in.Prefix, TokenHash: append([]byte(nil), in.TokenHash...),
		Scopes: append([]string(nil), in.Scopes...), CreatedAt: now, ExpiresAt: in.ExpiresAt.UTC()}
	m.platformTenantAccessTokens[token.ID] = token
	m.platformTenantAccessTokenByHash[string(token.TokenHash)] = token.ID
	return clonePlatformTenantAccessToken(token), nil
}

func (m *MemStore) ListPlatformTenantAccessTokens(_ context.Context, accountID, tenantID string) ([]PlatformTenantAccessToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := make([]PlatformTenantAccessToken, 0)
	for _, token := range m.platformTenantAccessTokens {
		if token.AccountID == accountID && token.TenantID == tenantID {
			out = append(out, clonePlatformTenantAccessToken(token))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) RevokePlatformTenantAccessToken(_ context.Context, accountID, tenantID, tokenID string) (PlatformTenantAccessToken, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantAccessToken{}, false, ErrNotFound
	}
	token, ok := m.platformTenantAccessTokens[tokenID]
	if !ok || token.AccountID != accountID || token.TenantID != tenantID {
		return PlatformTenantAccessToken{}, false, ErrNotFound
	}
	if token.RevokedAt != nil {
		return clonePlatformTenantAccessToken(token), false, nil
	}
	now := time.Now().UTC()
	token.RevokedAt = &now
	m.platformTenantAccessTokens[token.ID] = token
	return clonePlatformTenantAccessToken(token), true, nil
}

func (m *MemStore) AuthenticatePlatformTenantAccessToken(_ context.Context, hash []byte) (Account, PlatformTenantAccessToken, error) {
	if len(hash) != 32 {
		return Account{}, PlatformTenantAccessToken{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.platformTenantAccessTokenByHash[string(hash)]
	token, ok := m.platformTenantAccessTokens[id]
	if !ok || token.RevokedAt != nil || !time.Now().UTC().Before(token.ExpiresAt) {
		return Account{}, PlatformTenantAccessToken{}, ErrNotFound
	}
	tenant, ok := m.platformTenants[token.TenantID]
	if !ok || tenant.AccountID != token.AccountID {
		return Account{}, PlatformTenantAccessToken{}, ErrNotFound
	}
	account, ok := m.accounts[token.AccountID]
	if !ok {
		return Account{}, PlatformTenantAccessToken{}, ErrNotFound
	}
	now := time.Now().UTC()
	token.LastUsedAt = &now
	m.platformTenantAccessTokens[token.ID] = token
	return account, clonePlatformTenantAccessToken(token), nil
}
