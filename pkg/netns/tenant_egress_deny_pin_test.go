package netns

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// apid refuses egress allowlist entries inside api.TenantEgressDenyCIDRs
// because the netns denylist drops them (prod hunt r58). apid cannot import
// this package, so the two lists are pinned together here.
func TestTenantEgressDenyCIDRsMatchDefaultDenySet(t *testing.T) {
	deny := NewDefaultDenySet()
	var got []string
	for _, p := range deny.V4DenyCIDRs {
		got = append(got, p.String())
	}
	for _, p := range deny.V6DenyCIDRs {
		got = append(got, p.String())
	}
	want := slices.Clone(api.TenantEgressDenyCIDRs)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("api.TenantEgressDenyCIDRs = %v, netns default deny set = %v; update pkg/api/egress_deny.go", want, got)
	}
}
