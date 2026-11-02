// adr: 20261031122757 — the object gateway on compute nodes manifest contract.
package manifest

import (
	"strings"
	"testing"
)

func TestObjectStorageComputeNodesValidate(t *testing.T) {
	managed := ObjectStorage{Gateway: "managed", ConfigSrc: "/operator/object-storage.json", ProviderEnvSrc: "/operator/provider.env"}
	onCompute := managed
	onCompute.ComputeNodes, onCompute.GatewayProviderEnvSrc = true, "/operator/gateway-provider.env"
	control := Host{Name: "cp-1", Role: "control-plane"}
	compute := Host{Name: "cn-1", Role: "compute-only", PortMbps: 1000}
	slow := Host{Name: "cn-2", Role: "compute-only"}
	edge := PublicEdge{ComputeNodes: true}
	for _, tc := range []struct {
		name    string
		storage ObjectStorage
		edge    PublicEdge
		hosts   []Host
		wantErr string
	}{
		{name: "control plane only", storage: managed, hosts: []Host{control, slow}},
		{name: "on compute", storage: onCompute, edge: edge, hosts: []Host{control, compute}},
		{name: "unmanaged", storage: ObjectStorage{ComputeNodes: true, GatewayProviderEnvSrc: "/operator/gateway-provider.env"}, edge: edge, hosts: []Host{control, compute}, wantErr: "object_storage.compute_nodes: needs the managed gateway"},
		{name: "no gateway credential", storage: ObjectStorage{Gateway: "managed", ConfigSrc: "/operator/object-storage.json", ComputeNodes: true}, edge: edge, hosts: []Host{control, compute}, wantErr: "object_storage.gateway_provider_env_src"},
		{name: "gateway credential without compute nodes", storage: ObjectStorage{Gateway: "managed", ConfigSrc: "/operator/object-storage.json", GatewayProviderEnvSrc: "/operator/gateway-provider.env"}, hosts: []Host{control}, wantErr: "used only with compute_nodes"},
		{name: "no app edge", storage: onCompute, hosts: []Host{control, compute}, wantErr: "needs public_edge.compute_nodes"},
		{name: "no port speed", storage: onCompute, edge: edge, hosts: []Host{control, compute, slow}, wantErr: "fleet.hosts[2].port_mbps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := append(tc.storage.validate(), tc.storage.validateOnCompute(tc.edge, tc.hosts)...)
			if tc.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("validate = %v, want no errors", errs)
				}
				return
			}
			if !strings.Contains(errs.Error(), tc.wantErr) {
				t.Fatalf("validate = %v, want an error mentioning %q", errs, tc.wantErr)
			}
		})
	}
}
