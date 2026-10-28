package gateway

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// routeRecheckMemo keeps the answers of the registry rechecks a warm
// custom-domain route makes (is the domain still verified for this app; does
// a platform tenant surface own the host, kept only when none does, so a
// tenant suspension still applies at once) for
// api.GatewayRouteRecheckInterval.
// Run on every request, those rechecks cost one to three control-plane reads
// per custom-domain request. Route flushes (domain_changed, app_changed) drop
// the memo with the route cache, and failed lookups are never kept, so a
// registry error still fails the request the way it did.
type routeRecheckMemo struct {
	mu      sync.Mutex
	now     func() time.Time
	domains map[string]domainRecheck
	tenants map[string]tenantRecheck
}

type domainRecheck struct {
	appID     string
	active    bool
	checkedAt time.Time
}

type tenantRecheck struct {
	binding   PlatformTenantHostBinding
	found     bool
	checkedAt time.Time
}

func newRouteRecheckMemo() *routeRecheckMemo {
	return &routeRecheckMemo{now: time.Now, domains: map[string]domainRecheck{}, tenants: map[string]tenantRecheck{}}
}

func (m *routeRecheckMemo) fresh(at time.Time) bool {
	return m.now().Sub(at) < api.GatewayRouteRecheckInterval
}

func (m *routeRecheckMemo) domain(host, appID string) (active, ok bool) {
	if m == nil {
		return false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.domains[host]
	if !ok || d.appID != appID || !m.fresh(d.checkedAt) {
		return false, false
	}
	return d.active, true
}

func (m *routeRecheckMemo) rememberDomain(host, appID string, active bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.domains) >= RouteCacheCap {
		clear(m.domains)
	}
	m.domains[host] = domainRecheck{appID: appID, active: active, checkedAt: m.now()}
}

func (m *routeRecheckMemo) tenant(host string) (PlatformTenantHostBinding, bool, bool) {
	if m == nil {
		return PlatformTenantHostBinding{}, false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tenants[host]
	if !ok || !m.fresh(t.checkedAt) {
		return PlatformTenantHostBinding{}, false, false
	}
	return t.binding, t.found, true
}

func (m *routeRecheckMemo) rememberTenant(host string, binding PlatformTenantHostBinding, found bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tenants) >= RouteCacheCap {
		clear(m.tenants)
	}
	m.tenants[host] = tenantRecheck{binding: binding, found: found, checkedAt: m.now()}
}

func (m *routeRecheckMemo) forget(host string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.domains, host)
	delete(m.tenants, host)
}

func (m *routeRecheckMemo) reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.domains)
	clear(m.tenants)
}
