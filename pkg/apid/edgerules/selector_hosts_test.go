package edgerules

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func companionDeployment(t *testing.T, traffic int, specs ...companionRoute) state.Deployment {
	t.Helper()
	raw, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	return state.Deployment{TrafficPercent: traffic, Sidecars: raw}
}

// The edge-rules page lists the selector hosts the gateway routes, so a
// customer sees that companions with a port are publicly reachable.
func TestSelectorHostsMirrorGatewayRoutes(t *testing.T) {
	db := companionRoute{Name: "db-proxy", Type: api.SidecarTypeSidecar, Port: 5432}
	metrics := companionRoute{Name: "metrics", Type: api.SidecarTypeSidecar, Port: 9090}
	worker := companionRoute{Name: "worker", Type: api.SidecarTypeSidecar}
	setup := companionRoute{Name: "migrate", Type: "init", Port: 8000}
	cases := []struct {
		name  string
		live  []state.Deployment
		ports []api.WorkloadPort
		want  []SelectorHost
	}{
		{
			name: "companions with a port, sorted; portless and init helpers skipped",
			live: []state.Deployment{companionDeployment(t, 100, metrics, worker, setup, db)},
			want: []SelectorHost{
				{Host: "shop--db-proxy.gregale.dev", Name: "db-proxy"},
				{Host: "shop--metrics.gregale.dev", Name: "metrics"},
			},
		},
		{
			name: "a rollout keeps only companions both traffic-bearing deployments agree on",
			live: []state.Deployment{
				companionDeployment(t, 90, db, metrics),
				companionDeployment(t, 10, db, companionRoute{Name: "metrics", Type: api.SidecarTypeSidecar, Port: 9100}),
				companionDeployment(t, 0, worker),
			},
			want: []SelectorHost{{Host: "shop--db-proxy.gregale.dev", Name: "db-proxy"}},
		},
		{
			name: "public TCP listeners; internal and UDP listeners are not public",
			ports: []api.WorkloadPort{
				{Name: "Admin", Port: 8443},
				{Port: 7000, Protocol: api.WorkloadPortTCP},
				{Name: "cache", Port: 6379, Internal: true},
				{Name: "dns", Port: 53, Protocol: api.WorkloadPortUDP},
			},
			want: []SelectorHost{
				{Host: "shop--port-admin.gregale.dev", Name: "admin", PublicPort: true},
				{Host: "shop--port-tcp-7000.gregale.dev", Name: "tcp-7000", PublicPort: true},
			},
		},
		{name: "no live traffic, no companion hosts", live: []state.Deployment{companionDeployment(t, 0, db)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SelectorHosts("shop.gregale.dev", tc.live, tc.ports); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SelectorHosts = %+v, want %+v", got, tc.want)
			}
		})
	}
}
