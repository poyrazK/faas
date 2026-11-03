package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/role"
)

// The private-network reconcilers sweep every attachment and push it to every
// node, so only the fleet's one central scheduler runs them; a node-local
// schedd shares the host-wide flag and would race its status writes.
func TestPrivateNetworkReconcilersRunOnlyOnTheCentralScheduler(t *testing.T) {
	for _, tc := range []struct {
		role role.Role
		want bool
	}{
		{role.RoleSingleBox, true},
		{role.RoleControlPlane, true},
		{role.RoleComputeOnly, false},
	} {
		if got := privateNetworkReconcilersRun(tc.role); got != tc.want {
			t.Errorf("privateNetworkReconcilersRun(%q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}
