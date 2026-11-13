package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) memDomainPlanTargetLocked(target api.DomainBindingTarget) (domainPlanTarget, error) {
	a, ok := m.apps[target.AppID]
	if !ok {
		return domainPlanTarget{}, ErrNotFound
	}
	t := domainPlanTarget{App: a.Slug, AppID: a.ID, AccountID: a.AccountID, OrgID: a.OrgID, Status: string(a.Status), Visibility: string(a.Visibility), Preview: a.PreviewOfSlug, WorkloadClass: string(a.WorkloadClass), ExecutionMode: string(a.Manifest.ExecutionMode), ProjectID: a.ProjectID, Environment: target.Environment, EnvironmentID: target.EnvironmentID}
	scope := DefaultEnvScope
	if target.EnvironmentID != "" {
		found := false
		for _, e := range m.projectEnvironments {
			if e.ID == target.EnvironmentID && e.ProjectID == a.ProjectID && e.AccountID == a.AccountID && e.Slug == target.Environment {
				found = true
			}
		}
		if !found {
			return t, ErrNotFound
		}
		scope = target.Environment
		r := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(a.ProjectID, scope)]]
		if r.Active {
			t.ReleaseID = r.ID
		}
	}
	for _, d := range m.deployments {
		if d.AppID != a.ID || d.Status != DeployLive || d.EnvironmentWorkloadHeld() || d.DeletedAt != nil {
			continue
		}
		selected := t.ReleaseID == "" && normalizedDeploymentScope(d.Scope) == scope && d.TrafficPercent > 0
		if t.ReleaseID != "" {
			for _, member := range m.projectReleaseSets[t.ReleaseID].Members {
				if member.AppID == a.ID && member.DeploymentID == d.ID {
					selected = true
				}
			}
		}
		if selected {
			t.Deployments = append(t.Deployments, domainPlanDeployment{ID: d.ID, Traffic: d.TrafficPercent})
		}
	}
	sort.Slice(t.Deployments, func(i, j int) bool { return t.Deployments[i].ID < t.Deployments[j].ID })
	return t, nil
}

func (m *MemStore) readMemDomainPlanLocked(ctx context.Context, in DomainPlanInput) (api.DomainPlan, []domainPlanClaim, error) {
	var targets []domainPlanTarget
	var claims []domainPlanClaim
	for i, spec := range in.Manifest.Domains {
		t, err := m.memDomainPlanTargetLocked(in.Targets[i])
		if err != nil {
			return api.DomainPlan{}, nil, err
		}
		targets = append(targets, t)
		c := domainPlanClaim{Claim: m.domains[spec.Domain]}
		if customDomainTenantRouting(ctx) {
			for _, host := range m.tenantHostnames {
				if host.Hostname != spec.Domain || !host.Verified() {
					continue
				}
				surface := m.tenantSurfaces[host.SurfaceID]
				tenant := m.platformTenants[m.platformTenantBySurface[surface.ID]]
				if surface.Status != SurfaceStatusDeleted && (surface.Active() || tenant.Status == PlatformTenantSuspended) {
					c.TenantOverride = true
				}
			}
		}
		if a, ok := m.apps[c.Claim.AppID]; ok {
			c.AccountID, c.OrgID = a.AccountID, a.OrgID
			c.Before = api.DomainBindingTarget{App: a.Slug, AppID: a.ID, EnvironmentID: c.Claim.EnvironmentID}
			for _, e := range m.projectEnvironments {
				if e.ID == c.Claim.EnvironmentID {
					c.Before.Environment = e.Slug
				}
			}
			c.Default = m.defaultDomains[a.ID] == spec.Domain
		}
		claims = append(claims, c)
	}
	p, err := buildDomainBindingPlan(in, targets, claims)
	if err != nil {
		return p, nil, err
	}
	subject := m.appBillingSubjectLocked(in.AccountID, in.Targets[0].AppID)
	pending := make(map[string]int)
	for _, d := range m.domains {
		if d.Verified() || !d.ClaimLive(time.Now()) {
			continue
		}
		a := m.apps[d.AppID]
		if a.Status != AppDeleted && m.countsTowardQuotaSubjectLocked(a.AccountID, a.ID, subject) {
			pending[a.ID]++
		}
	}
	return p, claims, checkDomainPlanBudget(in, p, pending)
}

func (m *MemStore) PlanDomainBindings(ctx context.Context, in DomainPlanInput) (api.DomainPlan, error) {
	if err := validateDomainPlanInput(in); err != nil {
		return api.DomainPlan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.DomainPlan{}, err
	}
	p, _, err := m.readMemDomainPlanLocked(ctx, in)
	return p, err
}

func (m *MemStore) ApplyDomainBindings(ctx context.Context, in DomainPlanInput, expected string) (api.DomainPlan, []CustomDomain, error) {
	if err := validateDomainPlanInput(in); err != nil {
		return api.DomainPlan{}, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.DomainPlan{}, nil, err
	}
	p, claims, err := m.readMemDomainPlanLocked(ctx, in)
	if err != nil {
		return p, nil, err
	}
	if p.SHA256 != expected {
		return p, nil, fmt.Errorf("%w: reviewed domain plan is stale; generate and review a new plan", ErrConflict)
	}
	ctx = WithPublicCustomDomainClaim(ctx)
	proposed := make(map[string]CustomDomain, len(p.Changes))
	var hosts []string
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, c := range p.Changes {
		hosts = append(hosts, c.Domain)
		if c.Action == "noop" {
			continue
		}
		if err := m.checkPublicCustomDomainTenantOverlapLocked(ctx, c.Domain); err != nil {
			return p, nil, err
		}
		wildcard, err := ForeignVerifiedWildcard(ctx, lockedCustomDomainReader{m}, c.Domain, m.apps[c.After.AppID])
		if err != nil {
			return p, nil, err
		}
		if wildcard != "" {
			return p, nil, &CustomDomainWildcardConflictError{Domain: c.Domain, Wildcard: wildcard}
		}
		d := claims[i].Claim
		if c.Action == "create" {
			d = newDomainPlanClaim(c, now, uuid.NewString())
		} else {
			d.AppID, d.EnvironmentID, d.RedirectAppID = c.After.AppID, c.After.EnvironmentID, ""
			d.DNSLastCheckedAt, d.VerificationNextCheckAt = now, now
		}
		d.BindingVersion = uuid.NewString()
		proposed[c.Domain] = d
	}
	if err := m.checkMemTrafficTenantBindingLocked(ctx, in.AccountID, hosts, memTrafficPolicyChange{Domains: proposed}); err != nil {
		return p, nil, err
	}
	// All validation precedes mutation, including the whole-set routing guard.
	for name, d := range proposed {
		m.domains[name] = d
		delete(m.doctorObs, name)
		for app, domain := range m.defaultDomains {
			if domain == name {
				delete(m.defaultDomains, app)
			}
		}
	}
	m.invalidateChangedLifecycleSuccessorsLocked()
	result := make([]CustomDomain, 0, len(p.Changes))
	for _, c := range p.Changes {
		result = append(result, m.domains[c.Domain])
	}
	return p, result, nil
}

var _ DomainPlanStore = (*MemStore)(nil)
