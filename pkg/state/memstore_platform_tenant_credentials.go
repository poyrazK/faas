package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) ApplyPlatformTenantCredentials(_ context.Context, in ApplyPlatformTenantCredentialsParams) (ApplyPlatformTenantCredentialsResult, error) {
	if err := validatePlatformTenantCredentials(in); err != nil {
		return ApplyPlatformTenantCredentialsResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[in.AccountID]; !ok {
		return ApplyPlatformTenantCredentialsResult{}, ErrNotFound
	}
	result, err := m.planPlatformTenantCredentials(in)
	if err != nil || in.DryRun {
		return result, err
	}
	now := time.Now().UTC()
	for i := range result.Keys {
		item := &result.Keys[i]
		switch item.Action {
		case "create":
			item.Key.ID = uuid.NewString()
			item.Key.CreatedAt = now
			m.consumerKeys[item.Key.ID] = item.Key
		case "revoke":
			item.Key.RevokedAt = &now
			m.consumerKeys[item.Key.ID] = item.Key
		}
	}
	return result, nil
}

func (m *MemStore) planPlatformTenantCredentials(in ApplyPlatformTenantCredentialsParams) (ApplyPlatformTenantCredentialsResult, error) {
	result := ApplyPlatformTenantCredentialsResult{TenantID: in.TenantID, DryRun: in.DryRun,
		Keys: make([]PlatformTenantCredentialResult, 0, len(in.Keys)+len(in.RevokeKeyIDs))}
	tenant, ok := m.platformTenants[in.TenantID]
	if !ok || tenant.AccountID != in.AccountID {
		return result, ErrNotFound
	}
	if len(in.Keys) > 0 && tenant.Status != PlatformTenantActive {
		return result, ErrConflict
	}
	accountCount := 0
	appCounts := make(map[string]int)
	for _, key := range m.consumerKeys {
		if key.AccountID == in.AccountID && key.RevokedAt == nil {
			accountCount++
			appCounts[key.AppID]++
		}
	}
	revoking := make(map[string]bool, len(in.RevokeKeyIDs))
	for _, id := range in.RevokeKeyIDs {
		key, ok := m.consumerKeys[id]
		if !ok || key.AccountID != in.AccountID || m.platformTenantByConsumer[key.ConsumerID] != in.TenantID {
			return result, ErrNotFound
		}
		revoking[id] = true
		item := PlatformTenantCredentialResult{Key: key, Action: "unchanged"}
		if key.RevokedAt == nil {
			item.Action = "revoke"
			accountCount--
			appCounts[key.AppID]--
		}
		result.Keys = append(result.Keys, item)
	}
	seenNames := make(map[string]bool, len(in.Keys))
	for _, wanted := range in.Keys {
		consumer, ok := m.apiConsumers[wanted.ConsumerID]
		if !ok || consumer.AccountID != in.AccountID {
			return result, ErrNotFound
		}
		if m.platformTenantByConsumer[consumer.ID] != in.TenantID || !consumer.Active() {
			return result, ErrConflict
		}
		nameKey := consumer.AppID + "\x00" + wanted.Name
		if seenNames[nameKey] {
			return result, ErrConflict
		}
		seenNames[nameKey] = true
		var current ConsumerKey
		for _, key := range m.consumerKeys {
			if key.AccountID == in.AccountID && key.AppID == consumer.AppID && key.Name == wanted.Name {
				current = key
			}
			if key.AppID == consumer.AppID && key.Prefix == wanted.Prefix && key.Name != wanted.Name {
				return result, ErrConflict
			}
		}
		if current.ID != "" {
			if !sameCredentialIntent(current, wanted) || revoking[current.ID] {
				return result, ErrConflict
			}
			result.Keys = append(result.Keys, PlatformTenantCredentialResult{Key: current, Action: "unchanged"})
			continue
		}
		if wanted.ExpiresAt != nil && !wanted.ExpiresAt.After(time.Now()) {
			return result, ErrInvalidArgument
		}
		key := ConsumerKey{AccountID: in.AccountID, AppID: consumer.AppID, ConsumerID: consumer.ID,
			Name: wanted.Name, Prefix: wanted.Prefix, Hash: append([]byte(nil), wanted.Hash...),
			Scopes: append([]string(nil), wanted.Scopes...), ExpiresAt: wanted.ExpiresAt}
		accountCount++
		appCounts[consumer.AppID]++
		result.Keys = append(result.Keys, PlatformTenantCredentialResult{Key: key, Action: "create"})
	}
	hasCreate := false
	createdByApp := make(map[string]bool)
	for _, item := range result.Keys {
		if item.Action == "create" {
			hasCreate = true
			createdByApp[item.Key.AppID] = true
		}
	}
	if hasCreate && accountCount > in.AccountLimit {
		return result, &PlatformTenantCredentialQuotaError{Scope: "account", Limit: in.AccountLimit, Observed: accountCount - 1}
	}
	for appID, count := range appCounts {
		if createdByApp[appID] && count > in.AppLimit {
			return result, &PlatformTenantCredentialQuotaError{Scope: "app", Limit: in.AppLimit, Observed: count - 1}
		}
	}
	return result, nil
}

func (m *MemStore) ListPlatformTenantCredentials(_ context.Context, accountID, tenantID string, limit, offset int) ([]ConsumerKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return nil, ErrNotFound
	}
	out := make([]ConsumerKey, 0)
	for _, key := range m.consumerKeys {
		if key.AccountID == accountID && m.platformTenantByConsumer[key.ConsumerID] == tenantID {
			out = append(out, key)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if offset >= len(out) {
		return []ConsumerKey{}, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
