package pki

import (
	"slices"
	"sort"
	"testing"
)

// TestRolesForBoxMatchLiveHosts pins each host role's required leaf set.
//
// Every role RolesForBox returns is required by trust-bundle validation,
// renewal and expiry metrics, but no rollout or renewal path issues a leaf an
// existing host lacks. v0.1.18-rc.255 added two ADR-989 leaves to these sets
// and every live-node compute rollout then failed: "--pki-dir is neither a
// valid compute trust bundle nor issuable operator PKI". Adding a role here
// therefore needs an issuance migration for existing hosts first; update this
// list in the same change as that migration.
func TestRolesForBoxMatchLiveHosts(t *testing.T) {
	for _, tc := range []struct {
		box  string
		want []string
	}{
		{"compute-only", []string{
			"builderd/server", "builderd/vmmd-client", "egress/egress",
			"gatewayd-internal-public/leader-client", "gatewayd/apid-client",
			"gatewayd/schedd-client", "gatewayd/vmmd-client", "imaged/githubd-client",
			"imaged/server", "imaged/vmmd-client", "schedd/server", "schedd/vmmd-client",
			"vmmd/apid-client", "vmmd/schedd-client", "vmmd/server",
		}},
		{"control-plane", []string{
			"apid/advisory", "apid/githubd-client", "gatewayd-public/leader-server",
			"githubd/server", "meterd/egress-client", "meterd/schedd-client",
			"meterd/server", "schedd/server", "schedd/vmmd-client",
		}},
	} {
		var got []string
		for _, role := range RolesForBox(tc.box) {
			got = append(got, role.Directory+"/"+role.Filename)
		}
		sort.Strings(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("RolesForBox(%q) = %q\nwant %q\na new required leaf needs an issuance migration for existing hosts first", tc.box, got, tc.want)
		}
	}
}
