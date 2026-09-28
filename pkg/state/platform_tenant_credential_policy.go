package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PlatformTenantCredentialPolicy bounds credentials a platform tenant may
// issue for its linked consumers. An empty scope set disables delegated issue.
type PlatformTenantCredentialPolicy struct {
	TenantID           string
	AllowedScopes      []string
	MaxKeysPerConsumer int
	UpdatedAt          time.Time
}

// PlatformTenantCredentialPolicyStore remains separate from Store so narrow
// test doubles need not implement credential delegation controls.
type PlatformTenantCredentialPolicyStore interface {
	GetPlatformTenantCredentialPolicy(context.Context, string, string) (PlatformTenantCredentialPolicy, error)
	SetPlatformTenantCredentialPolicy(context.Context, string, string, []string, int) (PlatformTenantCredentialPolicy, error)
}

var (
	_ PlatformTenantCredentialPolicyStore = (*PgStore)(nil)
	_ PlatformTenantCredentialPolicyStore = (*MemStore)(nil)
)

func validPlatformTenantCredentialPolicy(accountID, tenantID string, scopes []string, maxKeysPerConsumer int) bool {
	if _, err := uuid.Parse(accountID); err != nil {
		return false
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return false
	}
	if len(scopes) > api.MaxPlatformTenantCredentialScopes || maxKeysPerConsumer < 0 ||
		maxKeysPerConsumer > api.MaxPlatformTenantKeysPerConsumer || (len(scopes) == 0) != (maxKeysPerConsumer == 0) {
		return false
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if scope != "read" && scope != "write" && scope != "admin" {
			return false
		}
		if _, duplicate := seen[scope]; duplicate {
			return false
		}
		seen[scope] = struct{}{}
	}
	return true
}

func clonePlatformTenantCredentialPolicy(policy PlatformTenantCredentialPolicy) PlatformTenantCredentialPolicy {
	policy.AllowedScopes = append([]string{}, policy.AllowedScopes...)
	return policy
}

func samePlatformTenantCredentialScopes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *MemStore) GetPlatformTenantCredentialPolicy(_ context.Context, accountID, tenantID string) (PlatformTenantCredentialPolicy, error) {
	if !validPlatformTenantCredentialPolicy(accountID, tenantID, nil, 0) {
		return PlatformTenantCredentialPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantCredentialPolicy{}, ErrNotFound
	}
	policy, ok := m.platformTenantCredentialPolicies[tenantID]
	if !ok {
		return PlatformTenantCredentialPolicy{TenantID: tenantID, AllowedScopes: []string{}}, nil
	}
	return clonePlatformTenantCredentialPolicy(policy), nil
}

func (m *MemStore) SetPlatformTenantCredentialPolicy(_ context.Context, accountID, tenantID string, scopes []string, maxKeysPerConsumer int) (PlatformTenantCredentialPolicy, error) {
	if !validPlatformTenantCredentialPolicy(accountID, tenantID, scopes, maxKeysPerConsumer) {
		return PlatformTenantCredentialPolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantCredentialPolicy{}, ErrNotFound
	}
	prior, exists := m.platformTenantCredentialPolicies[tenantID]
	if !exists || prior.MaxKeysPerConsumer != maxKeysPerConsumer || !samePlatformTenantCredentialScopes(prior.AllowedScopes, scopes) {
		prior = PlatformTenantCredentialPolicy{TenantID: tenantID, AllowedScopes: append([]string{}, scopes...),
			MaxKeysPerConsumer: maxKeysPerConsumer, UpdatedAt: time.Now().UTC()}
		m.platformTenantCredentialPolicies[tenantID] = prior
	}
	return clonePlatformTenantCredentialPolicy(prior), nil
}
