package state

import (
	"context"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	MaxPlatformTenantHostnameSuffixes = api.MaxPlatformTenantHostnameSuffixes
	MaxPlatformTenantDelegatedHosts   = api.MaxPlatformTenantDelegatedHosts
)

// PlatformTenantHostnamePolicy limits the hostnames a downstream tenant may
// add through a tenant-bound access token. Empty suffixes and a zero limit
// leave self-service disabled.
type PlatformTenantHostnamePolicy struct {
	TenantID        string
	AllowedSuffixes []string
	MaxHostnames    int
	UpdatedAt       time.Time
}

// PlatformTenantHostnamePolicyStore is separate from Store so existing narrow
// test doubles do not need to implement this policy surface.
type PlatformTenantHostnamePolicyStore interface {
	GetPlatformTenantHostnamePolicy(context.Context, string, string) (PlatformTenantHostnamePolicy, error)
	SetPlatformTenantHostnamePolicy(context.Context, string, string, []string, int) (PlatformTenantHostnamePolicy, error)
}

var (
	_ PlatformTenantHostnamePolicyStore = (*PgStore)(nil)
	_ PlatformTenantHostnamePolicyStore = (*MemStore)(nil)
)

func validPlatformTenantHostnamePolicy(accountID, tenantID string, suffixes []string, maxHostnames int) bool {
	if _, err := uuid.Parse(accountID); err != nil {
		return false
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return false
	}
	if maxHostnames < 0 || maxHostnames > MaxPlatformTenantDelegatedHosts || len(suffixes) > MaxPlatformTenantHostnameSuffixes {
		return false
	}
	if (len(suffixes) == 0) != (maxHostnames == 0) {
		return false
	}
	seen := make(map[string]struct{}, len(suffixes))
	for _, suffix := range suffixes {
		if !validPlatformTenantHostname(suffix) || net.ParseIP(suffix) != nil {
			return false
		}
		if _, exists := seen[suffix]; exists {
			return false
		}
		seen[suffix] = struct{}{}
	}
	return true
}

func (m *MemStore) GetPlatformTenantHostnamePolicy(_ context.Context, accountID, tenantID string) (PlatformTenantHostnamePolicy, error) {
	if !validPlatformTenantHostnamePolicy(accountID, tenantID, nil, 0) {
		return PlatformTenantHostnamePolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantHostnamePolicy{}, ErrNotFound
	}
	policy, ok := m.platformTenantHostnamePolicies[tenantID]
	if !ok {
		return PlatformTenantHostnamePolicy{TenantID: tenantID, AllowedSuffixes: []string{}}, nil
	}
	policy.AllowedSuffixes = append([]string{}, policy.AllowedSuffixes...)
	return policy, nil
}

func (m *MemStore) SetPlatformTenantHostnamePolicy(_ context.Context, accountID, tenantID string, suffixes []string, maxHostnames int) (PlatformTenantHostnamePolicy, error) {
	if !validPlatformTenantHostnamePolicy(accountID, tenantID, suffixes, maxHostnames) {
		return PlatformTenantHostnamePolicy{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantHostnamePolicy{}, ErrNotFound
	}
	prior, exists := m.platformTenantHostnamePolicies[tenantID]
	if !exists || prior.MaxHostnames != maxHostnames || !samePlatformTenantStrings(prior.AllowedSuffixes, suffixes) {
		prior = PlatformTenantHostnamePolicy{TenantID: tenantID, AllowedSuffixes: append([]string{}, suffixes...),
			MaxHostnames: maxHostnames, UpdatedAt: time.Now().UTC()}
		m.platformTenantHostnamePolicies[tenantID] = prior
	}
	prior.AllowedSuffixes = append([]string{}, prior.AllowedSuffixes...)
	return prior, nil
}

func samePlatformTenantStrings(a, b []string) bool {
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
