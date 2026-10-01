package state

import (
	"context"
	"errors"
	"net"
	"strings"
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

type PlatformTenantDelegatedHostnameResult struct {
	Hostname TenantHostname
	Action   string
}

type PlatformTenantDelegatedHostnameQuotaError struct {
	Limit    int
	Observed int
}

func (e *PlatformTenantDelegatedHostnameQuotaError) Error() string {
	return "state: platform tenant delegated hostname limit exceeded"
}

func (e *PlatformTenantDelegatedHostnameQuotaError) Is(target error) bool {
	_, ok := target.(*PlatformTenantDelegatedHostnameQuotaError)
	return ok
}

var (
	ErrPlatformTenantHostnameDelegationDisabled = errors.New("state: platform tenant hostname delegation is disabled")
	ErrPlatformTenantHostnameSuffixNotAllowed   = errors.New("state: hostname is outside the platform tenant delegation policy")
	ErrPlatformTenantSuspended                  = errors.New("state: platform tenant is suspended")
)

// PlatformTenantDelegatedHostnameStore atomically checks the tenant policy and
// creates or replays a tenant-owned hostname intent.
type PlatformTenantDelegatedHostnameStore interface {
	CreatePlatformTenantDelegatedHostname(context.Context, string, string, string, string, string, api.Limits) (PlatformTenantDelegatedHostnameResult, error)
}

var (
	_ PlatformTenantHostnamePolicyStore    = (*PgStore)(nil)
	_ PlatformTenantHostnamePolicyStore    = (*MemStore)(nil)
	_ PlatformTenantDelegatedHostnameStore = (*PgStore)(nil)
	_ PlatformTenantDelegatedHostnameStore = (*MemStore)(nil)
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

func platformTenantHostnameAllowed(hostname string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if hostname == suffix || len(hostname) > len(suffix) && hostname[len(hostname)-len(suffix)-1] == '.' && hostname[len(hostname)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}

func (m *MemStore) CreatePlatformTenantDelegatedHostname(ctx context.Context, accountID, tenantID, surfaceID, hostname, challengeToken string, limits api.Limits) (PlatformTenantDelegatedHostnameResult, error) {
	hostname = surfaceHostnameCanonical(hostname)
	if !validPlatformTenantHostnamePolicy(accountID, tenantID, nil, 0) || !validPlatformTenantHostname(hostname) ||
		!validUUID(surfaceID) || challengeToken == "" || limits.TenantHostnamesPerSurface < 1 {
		return PlatformTenantDelegatedHostnameResult{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PlatformTenantDelegatedHostnameResult{}, err
	}
	tenant, ok := m.platformTenants[tenantID]
	if !ok || tenant.AccountID != accountID {
		return PlatformTenantDelegatedHostnameResult{}, ErrNotFound
	}
	if tenant.Status != PlatformTenantActive {
		return PlatformTenantDelegatedHostnameResult{}, ErrPlatformTenantSuspended
	}
	surface, ok := m.tenantSurfaces[surfaceID]
	if !ok || surface.AccountID != accountID || surface.Status == SurfaceStatusDeleted || m.platformTenantBySurface[surfaceID] != tenantID {
		return PlatformTenantDelegatedHostnameResult{}, ErrNotFound
	}
	if existing, ok := m.tenantHostnames[hostname]; ok {
		if existing.SurfaceID != surfaceID {
			return PlatformTenantDelegatedHostnameResult{}, ErrConflict
		}
		return PlatformTenantDelegatedHostnameResult{Hostname: existing, Action: "unchanged"}, nil
	}
	policy, ok := m.platformTenantHostnamePolicies[tenantID]
	if !ok || len(policy.AllowedSuffixes) == 0 || policy.MaxHostnames == 0 {
		return PlatformTenantDelegatedHostnameResult{}, ErrPlatformTenantHostnameDelegationDisabled
	}
	if !platformTenantHostnameAllowed(hostname, policy.AllowedSuffixes) {
		return PlatformTenantDelegatedHostnameResult{}, ErrPlatformTenantHostnameSuffixNotAllowed
	}
	if existing, ok := m.tenantHostnames[strings.ToLower(hostname)]; ok {
		if existing.SurfaceID != surfaceID {
			return PlatformTenantDelegatedHostnameResult{}, ErrConflict
		}
		return PlatformTenantDelegatedHostnameResult{Hostname: existing, Action: "unchanged"}, nil
	}
	seen := make(map[string]struct{})
	tenantObserved, surfaceVerified := 0, 0
	for _, existing := range m.tenantHostnames {
		if _, duplicate := seen[existing.ID]; duplicate {
			continue
		}
		seen[existing.ID] = struct{}{}
		linked := m.platformTenantBySurface[existing.SurfaceID]
		if linked != tenantID {
			continue
		}
		if parent := m.tenantSurfaces[existing.SurfaceID]; parent.Status != SurfaceStatusDeleted {
			tenantObserved++
		}
		if existing.SurfaceID == surfaceID && existing.Verified() {
			surfaceVerified++
		}
	}
	if tenantObserved >= policy.MaxHostnames {
		return PlatformTenantDelegatedHostnameResult{}, &PlatformTenantDelegatedHostnameQuotaError{Limit: policy.MaxHostnames, Observed: tenantObserved}
	}
	if surfaceVerified >= limits.TenantHostnamesPerSurface {
		return PlatformTenantDelegatedHostnameResult{}, &TenantHostnameQuotaError{Limit: limits.TenantHostnamesPerSurface,
			Observed: surfaceVerified, SurfaceID: surfaceID}
	}
	now := time.Now().UTC()
	created := TenantHostname{ID: uuid.NewString(), SurfaceID: surfaceID, Hostname: hostname,
		ChallengeToken: challengeToken, CreatedAt: now}
	if err := m.checkMemTrafficTenantBindingLocked(ctx, accountID, []string{hostname}, memTrafficPolicyChange{TenantHostnames: map[string]TenantHostname{created.ID: created}}); err != nil {
		return PlatformTenantDelegatedHostnameResult{}, err
	}
	m.tenantHostnames[hostname], m.tenantHostnames[strings.ToLower(hostname)] = created, created
	return PlatformTenantDelegatedHostnameResult{Hostname: created, Action: "created"}, nil
}

func surfaceHostnameCanonical(hostname string) string {
	return strings.ToLower(strings.TrimSpace(hostname))
}
