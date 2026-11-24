package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// ADR-20261123100000: each compute edge with an IPv4 entry address gets the
// other compute edges' IPv4 entry addresses, so its guests' 443 calls to
// them hairpin to its own edge.
func TestRenderPeerEntryVars(t *testing.T) {
	edge := manifest.PublicEdge{ComputeNodes: true, Entry: manifest.PublicEdgeEntryBoth, Front: manifest.PublicEdgeFrontNone}
	control := manifest.Host{Name: "bhs-1", Role: roleControlPlane, PublicIPv4: "149.56.23.148"}
	a := manifest.Host{Name: "bhs-2", Role: roleComputeOnly, PublicIPv4: "15.235.50.108", EntryIPv4: "51.222.39.227"}
	b := manifest.Host{Name: "bhs-3", Role: roleComputeOnly, PublicIPv4: "15.235.50.109", EntryIPv4: "51.222.39.228"}
	c := manifest.Host{Name: "bhs-4", Role: roleComputeOnly, PublicIPv4: "15.235.50.110", EntryIPv4: "51.222.39.229"}
	v6 := manifest.Host{Name: "bhs-5", Role: roleComputeOnly, PublicIPv4: "15.235.50.111", EntryIPv4: "2001:db8::5"}
	noEntry := manifest.Host{Name: "bhs-6", Role: roleComputeOnly, PublicIPv4: "15.235.50.112"}
	hosts := []manifest.Host{control, a, b, c, v6, noEntry}

	for _, tc := range []struct {
		name string
		edge manifest.PublicEdge
		host manifest.Host
		want string
	}{
		{name: "peers exclude itself, the control plane, IPv6 and entry-less nodes", edge: edge, host: a,
			want: "faas_peer_entry_addrs: [\"51.222.39.228\", \"51.222.39.229\"]\n"},
		{name: "another node", edge: edge, host: c,
			want: "faas_peer_entry_addrs: [\"51.222.39.227\", \"51.222.39.228\"]\n"},
		{name: "control plane", edge: edge, host: control},
		{name: "node without an entry address", edge: edge, host: noEntry},
		{name: "node with an IPv6 entry address", edge: edge, host: v6},
		{name: "Cloudflare in front", edge: manifest.PublicEdge{ComputeNodes: true, Entry: manifest.PublicEdgeEntryBoth}, host: a},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderPeerEntryVars(tc.edge, hosts, tc.host); got != tc.want {
				t.Fatalf("renderPeerEntryVars = %q, want %q", got, tc.want)
			}
		})
	}

	if got := renderPeerEntryVars(edge, []manifest.Host{control, a}, a); got != "" {
		t.Fatalf("single compute edge rendered peers: %q", got)
	}
}
