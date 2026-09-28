package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) PlanPlatformTenantOffboarding(_ context.Context, accountID, tenantID string) (api.PlatformTenantOffboardingPlanResponse, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return api.PlatformTenantOffboardingPlanResponse{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.planPlatformTenantOffboardingLocked(accountID, tenantID, m.clock().UTC())
}

func (m *MemStore) planPlatformTenantOffboardingLocked(accountID, tenantID string, now time.Time) (api.PlatformTenantOffboardingPlanResponse, error) {
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return api.PlatformTenantOffboardingPlanResponse{}, ErrNotFound
	}
	snapshot := platformTenantOffboardingSnapshot{AccountID: accountID, TenantID: tenantID, Status: tenant.Status}
	for consumerID, linkedTenantID := range m.platformTenantByConsumer {
		if linkedTenantID != tenantID {
			continue
		}
		consumer, ok := m.apiConsumers[consumerID]
		if !ok || consumer.AccountID != accountID {
			continue
		}
		snapshot.Consumers = append(snapshot.Consumers, platformTenantOffboardingConsumer{ID: consumer.ID,
			Active: consumer.Active(), Managed: consumer.PlatformTenantManaged})
	}
	for surfaceID, linkedTenantID := range m.platformTenantBySurface {
		if linkedTenantID != tenantID {
			continue
		}
		surface, ok := m.tenantSurfaces[surfaceID]
		if !ok || surface.AccountID != accountID || surface.Status == SurfaceStatusDeleted {
			continue
		}
		snapshot.Surfaces = append(snapshot.Surfaces, platformTenantOffboardingSurface{ID: surface.ID,
			Status: surface.Status, Managed: surface.PlatformTenantManaged})
		for _, hostname := range m.tenantHostnames {
			if hostname.SurfaceID == surfaceID {
				snapshot.Hostnames = append(snapshot.Hostnames, platformTenantOffboardingHostname{ID: hostname.ID,
					SurfaceID: surfaceID, Hostname: hostname.Hostname, Managed: hostname.PlatformTenantManaged})
			}
		}
	}
	for _, key := range m.consumerKeys {
		if key.AccountID != accountID || m.platformTenantByConsumer[key.ConsumerID] != tenantID {
			continue
		}
		active := activePlatformTenantCredentialKey(key, now)
		expiresAt := ""
		if key.ExpiresAt != nil {
			expiresAt = key.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		snapshot.ConsumerKeys = append(snapshot.ConsumerKeys, platformTenantOffboardingCredential{ID: key.ID,
			ConsumerID: key.ConsumerID, AppID: key.AppID, Active: active, Revoked: key.RevokedAt != nil, ExpiresAt: expiresAt})
	}
	for _, token := range m.platformTenantAccessTokens {
		if token.AccountID != accountID || token.TenantID != tenantID {
			continue
		}
		snapshot.AccessTokens = append(snapshot.AccessTokens, platformTenantOffboardingCredential{ID: token.ID,
			Active: token.RevokedAt == nil && token.ExpiresAt.After(now), Revoked: token.RevokedAt != nil,
			ExpiresAt: token.ExpiresAt.UTC().Format(time.RFC3339Nano)})
	}
	if policy, ok := m.platformTenantCredentialPolicies[tenantID]; ok {
		snapshot.CredentialScopes = append([]string{}, policy.AllowedScopes...)
		snapshot.MaxKeysPerConsumer = policy.MaxKeysPerConsumer
	}
	if policy, ok := m.platformTenantConsumerPolicies[tenantID]; ok {
		snapshot.CustomerProvisioningEnabled = policy.Enabled
		snapshot.MaxConsumers = policy.MaxConsumers
	}
	if policy, ok := m.platformTenantHostnamePolicies[tenantID]; ok {
		snapshot.AllowedHostnameSuffixes = append([]string{}, policy.AllowedSuffixes...)
		snapshot.MaxHostnames = policy.MaxHostnames
	}
	return buildPlatformTenantOffboardingPlan(snapshot)
}

func (m *MemStore) ApplyPlatformTenantOffboarding(_ context.Context, accountID, tenantID, expectedPlanHash string) (api.PlatformTenantOffboardingApplyResponse, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrNotFound
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrNotFound
	}
	if !validPlatformTenantPlanHash(expectedPlanHash) {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock().UTC()
	plan, err := m.planPlatformTenantOffboardingLocked(accountID, tenantID, now)
	if err != nil {
		return api.PlatformTenantOffboardingApplyResponse{}, err
	}
	if !platformTenantPlanHashMatches(expectedPlanHash, plan.PlanHash) {
		return api.PlatformTenantOffboardingApplyResponse{}, ErrPlatformTenantPlanStale
	}
	tenant := m.platformTenants[tenantID]
	tenant.Status = PlatformTenantSuspended
	tenant.UpdatedAt = now
	m.platformTenants[tenantID] = tenant
	for id, key := range m.consumerKeys {
		if key.AccountID != accountID || m.platformTenantByConsumer[key.ConsumerID] != tenantID {
			continue
		}
		if activePlatformTenantCredentialKey(key, now) {
			revokedAt := now
			key.RevokedAt = &revokedAt
			m.consumerKeys[id] = key
		}
	}
	for id, token := range m.platformTenantAccessTokens {
		if token.AccountID != accountID || token.TenantID != tenantID || token.RevokedAt != nil || !token.ExpiresAt.After(now) {
			continue
		}
		revokedAt := now
		token.RevokedAt = &revokedAt
		m.platformTenantAccessTokens[id] = token
	}
	if policy, ok := m.platformTenantCredentialPolicies[tenantID]; ok {
		policy.AllowedScopes = []string{}
		policy.MaxKeysPerConsumer = 0
		policy.UpdatedAt = now
		m.platformTenantCredentialPolicies[tenantID] = policy
	}
	if policy, ok := m.platformTenantConsumerPolicies[tenantID]; ok {
		policy.Enabled = false
		policy.MaxConsumers = 0
		policy.UpdatedAt = now
		m.platformTenantConsumerPolicies[tenantID] = policy
	}
	if policy, ok := m.platformTenantHostnamePolicies[tenantID]; ok {
		policy.AllowedSuffixes = []string{}
		policy.MaxHostnames = 0
		policy.UpdatedAt = now
		m.platformTenantHostnamePolicies[tenantID] = policy
	}
	for id, consumer := range m.apiConsumers {
		if m.platformTenantByConsumer[id] != tenantID || !consumer.PlatformTenantManaged {
			continue
		}
		consumer.PlatformTenantID = ""
		consumer.UpdatedAt = now
		m.apiConsumers[id] = consumer
		delete(m.platformTenantByConsumer, id)
	}
	for key, hostname := range m.tenantHostnames {
		if m.platformTenantBySurface[hostname.SurfaceID] == tenantID && hostname.PlatformTenantManaged {
			delete(m.tenantHostnames, key)
		}
	}
	for surfaceID, linkedTenantID := range m.platformTenantBySurface {
		if linkedTenantID == tenantID {
			if surface, ok := m.tenantSurfaces[surfaceID]; ok && surface.PlatformTenantManaged {
				delete(m.platformTenantBySurface, surfaceID)
			}
		}
	}
	response := api.PlatformTenantOffboardingApplyResponse{TenantID: tenantID, ReceiptID: uuid.NewString(),
		PlanHash: plan.PlanHash, AppliedAt: now, Applied: true, Actions: plan.Actions}
	receipt := api.PlatformTenantOffboardingReceiptResponse{TenantID: tenantID, ReceiptID: response.ReceiptID,
		PlanHash: response.PlanHash, AppliedAt: response.AppliedAt, Actions: response.Actions}
	m.platformTenantOffboardingReceipts[receipt.ReceiptID] = receipt
	return response, nil
}

func (m *MemStore) ListPlatformTenantOffboardingReceipts(_ context.Context, accountID, tenantID string, pageSize int, pageToken string) ([]api.PlatformTenantOffboardingReceiptSummary, string, error) {
	if pageSize < 1 || pageSize > 100 {
		return nil, "", ErrInvalidArgument
	}
	var tokenTime time.Time
	var tokenID string
	if pageToken != "" {
		var valid bool
		tokenTime, tokenID, valid = decodePageToken(pageToken)
		if _, err := uuid.Parse(tokenID); !valid || err != nil {
			return nil, "", ErrInvalidArgument
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return nil, "", ErrNotFound
	}
	receipts := make([]api.PlatformTenantOffboardingReceiptResponse, 0)
	for _, receipt := range m.platformTenantOffboardingReceipts {
		if receipt.TenantID == tenantID {
			receipts = append(receipts, receipt)
		}
	}
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].AppliedAt.Equal(receipts[j].AppliedAt) {
			return receipts[i].ReceiptID > receipts[j].ReceiptID
		}
		return receipts[i].AppliedAt.After(receipts[j].AppliedAt)
	})
	if pageToken != "" {
		filtered := receipts[:0]
		for _, receipt := range receipts {
			if receipt.AppliedAt.Before(tokenTime) || (receipt.AppliedAt.Equal(tokenTime) && receipt.ReceiptID < tokenID) {
				filtered = append(filtered, receipt)
			}
		}
		receipts = filtered
	}
	var nextToken string
	if len(receipts) > pageSize {
		last := receipts[pageSize-1]
		nextToken = encodePageToken(last.AppliedAt, last.ReceiptID)
		receipts = receipts[:pageSize]
	}
	out := make([]api.PlatformTenantOffboardingReceiptSummary, 0, len(receipts))
	for _, receipt := range receipts {
		out = append(out, api.PlatformTenantOffboardingReceiptSummary{ReceiptID: receipt.ReceiptID,
			PlanHash: receipt.PlanHash, AppliedAt: receipt.AppliedAt})
	}
	return out, nextToken, nil
}

func (m *MemStore) GetPlatformTenantOffboardingReceipt(_ context.Context, accountID, tenantID, receiptID string) (api.PlatformTenantOffboardingReceiptResponse, error) {
	if _, err := uuid.Parse(receiptID); err != nil {
		return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
	}
	receipt, ok := m.platformTenantOffboardingReceipts[receiptID]
	if !ok || receipt.TenantID != tenantID {
		return api.PlatformTenantOffboardingReceiptResponse{}, ErrNotFound
	}
	return receipt, nil
}
