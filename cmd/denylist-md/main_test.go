package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/oci"
)

// The customer page is served at gregale.dev/docs/egress-denylist. It names
// what is blocked, never how the platform enforces it.
func TestRenderCustomerOmitsEnforcementInternals(t *testing.T) {
	ds := netns.NewDefaultDenySet()
	out := renderCustomer(ds, oci.OCIOnlyDenyCIDRsV4(), api.TenantEgressAllowlistBlockedPorts(), portReason)
	if again := renderCustomer(ds, oci.OCIOnlyDenyCIDRsV4(), api.TenantEgressAllowlistBlockedPorts(), portReason); again != out {
		t.Fatal("renderCustomer is non-deterministic")
	}
	internal := regexp.MustCompile(`ADR|\bspec\b|pkg/|_test\.go|nftables|netns|Hetzner|lateral|OCI dialer`)
	if m := internal.FindString(out); m != "" {
		t.Fatalf("customer page mentions %q:\n%s", m, out)
	}
	for _, want := range []string{"`10.0.0.0/8`", "`169.254.169.254`", "`fc00::/7`", "`25`", "`22`", "Mail submission ports `465` and `587`", "`240.0.0.0/4`"} {
		if !strings.Contains(out, want) {
			t.Errorf("customer page lacks %s:\n%s", want, out)
		}
	}
}
