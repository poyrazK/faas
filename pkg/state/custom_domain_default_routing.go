package state

import (
	"context"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrCustomDomainNotRoutable = errors.New("custom domain routes to another app or a suspended tenant")

type customDomainRoutingKey struct{}

// WithCustomDomainRouting carries the caller's current tenant-routing flag
// into canonical-domain selection. It changes eligibility, never ownership.
func WithCustomDomainRouting(ctx context.Context, tenantSurfaces bool) context.Context {
	return context.WithValue(ctx, customDomainRoutingKey{}, tenantSurfaces)
}

func customDomainTenantRouting(ctx context.Context) bool {
	if enabled, ok := ctx.Value(customDomainRoutingKey{}).(bool); ok {
		return enabled
	}
	return api.TenantSurfacesEnabled()
}

func (m *MemStore) customDomainDefaultRoutesToAppLocked(ctx context.Context, domain CustomDomain) bool {
	if !customDomainTenantRouting(ctx) {
		return true
	}
	for _, host := range m.tenantHostnames {
		if !strings.EqualFold(host.Hostname, domain.Domain) || !host.Verified() {
			continue
		}
		surface, ok := m.tenantSurfaces[host.SurfaceID]
		if !ok || surface.Status == SurfaceStatusDeleted {
			continue
		}
		tenant := m.platformTenants[m.platformTenantBySurface[surface.ID]]
		if tenant.Status == PlatformTenantSuspended || surface.Active() && surface.AppID != domain.AppID {
			return false
		}
	}
	return true
}
