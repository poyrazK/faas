package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PlatformTenantConsumerProvisioningPolicy bounds how many customer
// identities a downstream platform tenant may create for itself.
type PlatformTenantConsumerProvisioningPolicy struct {
	TenantID     string
	Enabled      bool
	MaxConsumers int
	UpdatedAt    time.Time
}

// PlatformTenantConsumerProvisioningPolicyStore is kept separate from Store
// so existing narrow test doubles do not need policy management methods.
type PlatformTenantConsumerProvisioningPolicyStore interface {
	GetPlatformTenantConsumerProvisioningPolicy(context.Context, string, string) (PlatformTenantConsumerProvisioningPolicy, error)
	SetPlatformTenantConsumerProvisioningPolicy(context.Context, string, string, bool, int) (PlatformTenantConsumerProvisioningPolicy, error)
}

var (
	_ PlatformTenantConsumerProvisioningPolicyStore = (*PgStore)(nil)
	_ PlatformTenantConsumerProvisioningPolicyStore = (*MemStore)(nil)
)

func validPlatformTenantConsumerProvisioningPolicy(accountID, tenantID string, enabled bool, maxConsumers int) bool {
	if _, err := uuid.Parse(accountID); err != nil {
		return false
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return false
	}
	if enabled {
		return maxConsumers >= 1 && maxConsumers <= api.MaxPlatformTenantSelfServiceConsumers
	}
	return maxConsumers == 0
}

func (m *MemStore) GetPlatformTenantConsumerProvisioningPolicy(_ context.Context, accountID, tenantID string) (PlatformTenantConsumerProvisioningPolicy, error) {
	if !validPlatformTenantConsumerProvisioningPolicy(accountID, tenantID, false, 0) {
		return PlatformTenantConsumerProvisioningPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantConsumerProvisioningPolicy{}, ErrNotFound
	}
	policy, ok := m.platformTenantConsumerPolicies[tenantID]
	if !ok {
		return PlatformTenantConsumerProvisioningPolicy{TenantID: tenantID}, nil
	}
	return policy, nil
}

func (m *MemStore) SetPlatformTenantConsumerProvisioningPolicy(_ context.Context, accountID, tenantID string, enabled bool, maxConsumers int) (PlatformTenantConsumerProvisioningPolicy, error) {
	if !validPlatformTenantConsumerProvisioningPolicy(accountID, tenantID, enabled, maxConsumers) {
		return PlatformTenantConsumerProvisioningPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantConsumerProvisioningPolicy{}, ErrNotFound
	}
	prior, exists := m.platformTenantConsumerPolicies[tenantID]
	if !exists || prior.Enabled != enabled || prior.MaxConsumers != maxConsumers {
		prior = PlatformTenantConsumerProvisioningPolicy{TenantID: tenantID, Enabled: enabled,
			MaxConsumers: maxConsumers, UpdatedAt: time.Now().UTC()}
		m.platformTenantConsumerPolicies[tenantID] = prior
	}
	return prior, nil
}
