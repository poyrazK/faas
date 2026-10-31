// adr: 176
package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The dashboard lists public-port hostnames from api.PublicPortSelectorKey;
// every key it produces must be one the gateway routes, and no other.
func TestPublicPortSelectorKeyMatchesGatewayRouting(t *testing.T) {
	ports := []api.WorkloadPort{
		{Name: "Admin", Port: 8443},
		{Port: 7000},
		{Port: 7001, Protocol: "TCP"},
		{Name: "cache", Port: 6379, Internal: true},
		{Name: "dns", Port: 53, Protocol: api.WorkloadPortUDP},
		{Name: "bad", Port: 70000},
	}
	app := App{Ports: PublicPortsFromWorkloadPorts(ports)}
	for _, p := range ports {
		key, ok := api.PublicPortSelectorKey(p)
		if !ok {
			continue
		}
		port, routed := PublicPortSelectorForApp(app, PublicPortSelectorPrefix+key)
		if !routed || port != p.Port {
			t.Fatalf("listener %+v: key %q routes to %d (routed=%v), want %d", p, key, port, routed, p.Port)
		}
	}
	for _, p := range ports {
		if _, ok := api.PublicPortSelectorKey(p); ok {
			continue
		}
		name := p.Name
		if name == "" {
			continue
		}
		if _, routed := PublicPortSelectorForApp(app, PublicPortSelectorPrefix+name); routed {
			t.Fatalf("listener %+v has no public key but the gateway routes it", p)
		}
	}
}
