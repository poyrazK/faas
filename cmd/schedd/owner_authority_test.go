// adr: 20261110210000
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/role"
)

func TestOwnerAuthorityFromEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  ownerAuthority
		ok    bool
	}{
		{"", ownerAuthorityAny, true},
		{"any", ownerAuthorityAny, true},
		{"control-plane", ownerAuthorityControlPlane, true},
		{"controlplane", "", false},
		{"enforce", "", false},
	} {
		t.Setenv("FAAS_APP_OWNER_AUTHORITY", tc.value)
		got, err := ownerAuthorityFromEnv()
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("FAAS_APP_OWNER_AUTHORITY=%q -> %q, %v; want %q ok=%v", tc.value, got, err, tc.want, tc.ok)
		}
	}
}

// Under control-plane authority no compute schedd claims, recovers onto
// itself or moves pressured apps, and only the control plane recovers
// centrally. "any" keeps today's behaviour on every role.
func TestOwnerAuthorityByRole(t *testing.T) {
	type want struct{ claims, ownNode, central bool }
	for _, tc := range []struct {
		authority ownerAuthority
		role      role.Role
		want      want
	}{
		{ownerAuthorityAny, role.RoleComputeOnly, want{true, true, false}},
		{ownerAuthorityAny, role.RoleControlPlane, want{true, true, false}},
		{ownerAuthorityAny, role.RoleSingleBox, want{true, true, false}},
		{ownerAuthorityControlPlane, role.RoleComputeOnly, want{false, false, false}},
		{ownerAuthorityControlPlane, role.RoleControlPlane, want{true, false, true}},
		{ownerAuthorityControlPlane, role.RoleSingleBox, want{true, true, false}},
	} {
		got := want{tc.authority.claimsOwners(tc.role), tc.authority.recoversOntoOwnNode(tc.role), tc.authority.recoversCentrally(tc.role)}
		if got != tc.want {
			t.Errorf("%s on %s = %+v, want %+v", tc.authority, tc.role, got, tc.want)
		}
	}
}
