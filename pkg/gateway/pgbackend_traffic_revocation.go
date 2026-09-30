// adr: 375
package gateway

import "github.com/onebox-faas/faas/pkg/trafficrevocation"

func (b *PGBackend) WithTrafficRevocations(registry *trafficrevocation.Registry) *PGBackend {
	b.trafficRevocations.Store(registry)
	return b
}

// Notifications are wake-ups only; the registry re-reads authoritative rows.
func (b *PGBackend) RequestTrafficRevocationRefresh() {
	if registry := b.trafficRevocations.Load(); registry != nil {
		registry.RequestRefresh()
	}
}
