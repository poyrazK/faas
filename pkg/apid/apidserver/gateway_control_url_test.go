package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// On a split control plane apid's loopback gateway control URL refused every
// MCP replica probe (Hunt 11 H11-31, Hunt 12 H12-9). With
// FAAS_GATEWAYD_CONTROL_FROM_REGISTRY apid takes the private control port of
// the active compute nodes in turn (ADR-20261022004500).
func TestGatewayControlURLFromRegistry(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	addNode := func(name, gateway string, active bool) {
		t.Helper()
		target := gateway
		node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{
			Name: name, TargetURL: "tcp://" + name + ":50051", GatewayTargetURL: &target,
			VPCPUs: 4, MemMB: 8192, MaxConcurrency: 16, AdmissionCeilingMB: 4096,
		})
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		if !active {
			if err := store.SetComputeNodeActive(ctx, node.ID, false); err != nil {
				t.Fatalf("deactivate %s: %v", name, err)
			}
		}
	}
	addNode("fsn-3", "tcp://10.10.0.3:8080", true)
	addNode("fsn-2", "tcp://10.10.0.2:8080", true)
	addNode("fsn-old", "tcp://10.10.0.9:8080", false)

	tests := []struct {
		name     string
		registry bool
		want     []string
	}{
		{"loopback default without the switch", false, []string{"http://127.0.0.1:9090", "http://127.0.0.1:9090"}},
		{"active nodes in turn with the switch", true, []string{"http://10.10.0.2:9097", "http://10.10.0.3:9097", "http://10.10.0.2:9097"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := apidserver.NewServer(store, nil, "gregale.dev", nil)
			srv.WithGatewaydControlURL("http://127.0.0.1:9090")
			srv.WithGatewaydControlFromRegistry(tt.registry)
			for i, want := range tt.want {
				if got := srv.GatewayControlURL(ctx); got != want {
					t.Fatalf("call %d: GatewayControlURL = %q, want %q", i, got, want)
				}
			}
		})
	}

	empty := apidserver.NewServer(state.NewMemStore(), nil, "gregale.dev", nil)
	empty.WithGatewaydControlFromRegistry(true)
	if got := empty.GatewayControlURL(ctx); got != "" {
		t.Fatalf("no compute nodes: GatewayControlURL = %q, want unavailable", got)
	}
}
