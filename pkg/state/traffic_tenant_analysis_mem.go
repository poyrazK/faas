// adr: 375
package state

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) appendMemTrafficTenantHostsLocked(ctx context.Context, account string, change memTrafficPolicyChange, view *trafficHostAnalysis, baseInputs int) error {
	seen := make(map[string]bool)
	visit := func(host TenantHostname) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if host.ID != "" && seen[host.ID] {
			return nil
		}
		if host.ID != "" {
			seen[host.ID] = true
		}
		if proposed, found := change.TenantHostnames[host.ID]; found {
			host = proposed
		}
		return m.appendMemTrafficTenantHostLocked(host, account, change, view, baseInputs)
	}
	for _, host := range m.tenantHostnames {
		if err := visit(host); err != nil {
			return err
		}
	}
	for _, host := range change.TenantHostnames {
		if err := visit(host); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) appendMemTrafficTenantHostLocked(host TenantHostname, account string, change memTrafficPolicyChange, view *trafficHostAnalysis, baseInputs int) error {
	if !host.Verified() {
		return nil
	}
	surface, found := change.TenantSurfaces[host.SurfaceID]
	if !found {
		surface = m.tenantSurfaces[host.SurfaceID]
	}
	app, found := change.Apps[surface.AppID]
	if !found {
		app = m.apps[surface.AppID]
	}
	if surface.AccountID != account || !surface.Active() || app.AccountID != surface.AccountID || app.Status == AppDeleted || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
		return nil
	}
	tenantID, found := change.TenantSurfaceLinks[surface.ID]
	if !found {
		tenantID = m.platformTenantBySurface[surface.ID]
	}
	tenant, found := change.PlatformTenants[tenantID]
	if !found {
		tenant = m.platformTenants[tenantID]
	}
	if tenant.Status == PlatformTenantSuspended {
		return nil
	}
	if host.ID == "" || surface.ID == "" || app.ID == "" {
		return analysisLimit("tenant_identity", "bindings", 0, 1)
	}
	view.Tenants = append(view.Tenants, trafficHostTenant{Host: strings.ToLower(host.Hostname), App: app.ID, Surface: surface.ID, ID: host.ID, PlatformTenant: tenantID})
	return checkMemTrafficAnalysisInputs(baseInputs + len(view.Tenants))
}

func (m *MemStore) MarkTenantHostnameVerifiedIfChallenge(ctx context.Context, hostname, token string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.markTrafficTenantHostnameVerifiedLocked(ctx, hostname, token, true)
}

func (m *MemStore) markTrafficTenantHostnameVerifiedLocked(ctx context.Context, hostname, token string, challengeBound bool) (bool, error) {
	host, found := m.tenantHostnames[strings.ToLower(hostname)]
	if !found || challengeBound && (host.ChallengeToken != token || host.Verified()) {
		if challengeBound {
			return false, ctx.Err()
		}
		return false, ErrNotFound
	}
	surface, found := m.tenantSurfaces[host.SurfaceID]
	if !found {
		return false, ErrNotFound
	}
	host.VerifiedAt = time.Now().UTC()
	host.LastCheckAt, host.LastError = host.VerifiedAt, ""
	if err := m.validateMemTrafficPolicyChangeLocked(ctx, surface.AccountID, memTrafficPolicyChange{TenantHostnames: map[string]TenantHostname{host.ID: host}}); err != nil {
		return false, err
	}
	// Keep both case aliases coherent; policy metadata deduplicates their ID.
	for key, current := range m.tenantHostnames {
		if current.ID == host.ID {
			m.tenantHostnames[key] = host
		}
	}
	return true, nil
}
