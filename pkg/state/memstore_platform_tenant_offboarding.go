package state

import (
	"context"
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
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return api.PlatformTenantOffboardingPlanResponse{}, ErrNotFound
	}
	snapshot := platformTenantOffboardingSnapshot{AccountID: accountID, TenantID: tenantID, Status: tenant.Status}
	now := time.Now().UTC()
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
