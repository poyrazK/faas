// adr: 570
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func visitMemTrafficTenantHostnames(ctx context.Context, rows map[string]TenantHostname, proposed map[string]TenantHostname, visit func(TenantHostname) error) error {
	seen := make(map[string]bool)
	for _, host := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := trafficTenantProposalKey(host)
		if seen[key] {
			continue
		}
		seen[key] = true
		if next, found := proposed[key]; found {
			host = next
		}
		if host.Hostname != "" {
			if err := visit(host); err != nil {
				return err
			}
		}
	}
	for id, host := range proposed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !seen[id] && host.Hostname != "" {
			if err := visit(host); err != nil {
				return err
			}
		}
	}
	return nil
}

func trafficTenantProposalKey(host TenantHostname) string {
	if host.ID != "" {
		return host.ID
	}
	return "hostname:" + strings.ToLower(host.Hostname)
}

func (m *MemStore) deleteTrafficTenantHostnameLocked(ctx context.Context, hostname, expectedSurface string) error {
	host, found := m.tenantHostnames[strings.ToLower(hostname)]
	if !found {
		host, found = m.tenantHostnames[hostname]
	}
	if !found || expectedSurface != "" && host.SurfaceID != expectedSurface {
		return ErrNotFound
	}
	account := m.tenantSurfaces[host.SurfaceID].AccountID
	if err := m.checkMemTrafficTenantBindingLocked(ctx, account, []string{hostname}, memTrafficPolicyChange{TenantHostnames: map[string]TenantHostname{trafficTenantProposalKey(host): {}}}); err != nil {
		return err
	}
	for key, current := range m.tenantHostnames {
		if current.ID == host.ID && strings.EqualFold(current.Hostname, host.Hostname) {
			delete(m.tenantHostnames, key)
		}
	}
	return nil
}

func (m *MemStore) DeleteTenantHostnameForSurface(ctx context.Context, hostname, surface string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteTrafficTenantHostnameLocked(ctx, hostname, surface)
}

func (m *MemStore) memTrafficBindingClaimsLocked(ctx context.Context, change memTrafficPolicyChange) (trafficBindingClaims, error) {
	var result trafficBindingClaims
	var err error
	result.Domains, err = m.memTrafficDomainClaimsLocked(ctx, change)
	if err != nil {
		return result, err
	}
	err = visitMemTrafficTenantHostnames(ctx, m.tenantHostnames, change.TenantHostnames, func(host TenantHostname) error {
		surface, found := change.TenantSurfaces[host.SurfaceID]
		if !found {
			surface = m.tenantSurfaces[host.SurfaceID]
		}
		app, found := change.Apps[surface.AppID]
		if !found {
			app = m.apps[surface.AppID]
		}
		tenantID, found := change.TenantSurfaceLinks[surface.ID]
		if !found {
			tenantID = m.platformTenantBySurface[surface.ID]
		}
		tenant, found := change.PlatformTenants[tenantID]
		if !found {
			tenant = m.platformTenants[tenantID]
		}
		claim := trafficTenantClaim{trafficHostTenant: trafficHostTenant{Host: strings.ToLower(host.Hostname), ID: host.ID, Surface: host.SurfaceID, App: surface.AppID, PlatformTenant: tenantID},
			Account: surface.AccountID, AppAccount: app.AccountID, Status: surface.Status, Verified: host.Verified(),
			Public:    app.ID != "" && app.AccountID == surface.AccountID && app.Status != AppDeleted && api.NormalizeAppVisibility(app.Visibility) != api.AppVisibilityInternal,
			Suspended: tenant.Status == PlatformTenantSuspended}
		if claim.eligible() && (claim.ID == "" || claim.Surface == "" || claim.App == "") {
			return analysisLimit("tenant_identity", "bindings", 0, 1)
		}
		result.Tenants = append(result.Tenants, claim)
		return checkMemTrafficAnalysisInputs(len(result.Domains) + len(result.Tenants))
	})
	if err != nil {
		return result, err
	}
	result.Aliases, result.Primaries, err = m.memTrafficNamedClaimsLocked(ctx, change)
	if err != nil {
		return result, err
	}
	if err := checkMemTrafficAnalysisInputs(len(result.Domains) + len(result.Tenants) + len(result.Aliases) + len(result.Primaries)); err != nil {
		return result, err
	}
	sort.Slice(result.Tenants, func(i, j int) bool { return result.Tenants[i].Host < result.Tenants[j].Host })
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, fmt.Errorf("state: encode tenant binding metadata: %w", err)
	}
	if size := int64(len(encoded)); size > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return result, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, size)
	}
	return result, nil
}

func (m *MemStore) checkMemTrafficTenantBindingLocked(ctx context.Context, account string, requested []string, change memTrafficPolicyChange) error {
	return m.checkMemTrafficBindingLocked(ctx, account, requested, "", change)
}

func (m *MemStore) checkMemTrafficBindingLocked(ctx context.Context, account string, requested []string, appID string, change memTrafficPolicyChange) error {
	return boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		before, err := m.memTrafficBindingClaimsLocked(bounded, memTrafficPolicyChange{})
		if err != nil {
			return err
		}
		after, err := m.memTrafficBindingClaimsLocked(bounded, change)
		if err != nil {
			return err
		}
		hosts := trafficBindingAffectedHosts(before, account, requested, appID)
		owners, err := trafficTenantOverlappingOwners(bounded, before, hosts, account)
		if err != nil {
			return err
		}
		for _, rule := range m.edgeRules {
			if err := bounded.Err(); err != nil {
				return err
			}
			if rule.Enabled && rule.Kind == EdgeRuleKindRoute {
				owners[rule.AccountID] = true
			}
		}
		delete(owners, "")
		if len(owners) > api.TrafficPolicyMaxAnalysisInputs {
			return analysisLimit("inputs", "owners", api.TrafficPolicyMaxAnalysisInputs, int64(len(owners)))
		}
		accounts := make([]string, 0, len(owners))
		for owner := range owners {
			accounts = append(accounts, owner)
		}
		sort.Strings(accounts)
		globalBefore, err := m.readMemTrafficHostAnalysisLocked(bounded, "", memTrafficPolicyChange{GlobalRoutes: true})
		if err != nil {
			return err
		}
		globalChange := change
		globalChange.GlobalRoutes = true
		globalAfter, err := m.readMemTrafficHostAnalysisLocked(bounded, "", globalChange)
		if err != nil {
			return err
		}
		if err := globalTrafficPolicyError(checkTrafficHostAnalysis(bounded, globalBefore, globalAfter)); err != nil {
			return err
		}
		for _, owner := range accounts {
			prior, err := m.readMemTrafficHostAnalysisLocked(bounded, owner, memTrafficPolicyChange{})
			if err != nil {
				return err
			}
			next, err := m.readMemTrafficHostAnalysisLocked(bounded, owner, change)
			if err != nil {
				return err
			}
			if err := checkTrafficTenantBindingOwner(bounded, trafficPrimaryOwnerView(prior, before.Aliases), trafficPrimaryOwnerView(next, after.Aliases), before.Domains, after.Domains, before.Tenants, after.Tenants, owner, globalBefore, globalAfter); err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *MemStore) DeleteTenantSurfaceWithHostnames(ctx context.Context, surfaceID, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	surface, found := m.tenantSurfaces[surfaceID]
	if !found || surface.AccountID != accountID || surface.Status == SurfaceStatusDeleted {
		return ErrNotFound
	}
	surface.Status = SurfaceStatusDeleted
	surface.UpdatedAt = nextTenantSurfaceTime(surface.UpdatedAt)
	change := memTrafficPolicyChange{TenantSurfaces: map[string]TenantSurface{surfaceID: surface}, TenantHostnames: make(map[string]TenantHostname)}
	if err := visitMemTrafficTenantHostnames(ctx, m.tenantHostnames, nil, func(host TenantHostname) error {
		if host.SurfaceID == surfaceID {
			change.TenantHostnames[trafficTenantProposalKey(host)] = TenantHostname{}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := m.checkMemTrafficTenantBindingLocked(ctx, accountID, nil, change); err != nil {
		return err
	}
	m.tenantSurfaces[surfaceID] = surface
	for key, host := range m.tenantHostnames {
		if host.SurfaceID == surfaceID {
			delete(m.tenantHostnames, key)
		}
	}
	return nil
}
