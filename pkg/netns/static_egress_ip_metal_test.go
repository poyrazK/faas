//go:build metal

package netns

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

// TestMetalHostPolicyStaticEgressIPRules pipes a host policy carrying an
// ADR-119 static-egress SNAT rule through `nft -c -f`. The ADR-119 redesign
// moved per-app static IPs from the per-netns ruleset to the host renderer
// (HostPolicy.StaticEgressRules); this test previously set a per-netns
// field that no longer exists and had stopped compiling.
func TestMetalHostPolicyStaticEgressIPRules(t *testing.T) {
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skipf("nft not on PATH: %v", err)
	}
	p := DefaultHostPolicy
	p.StaticEgressRules = []StaticEgressRule{{
		PerVMHostIP: netip.MustParseAddr("10.200.0.5"),
		CustomerIP:  netip.MustParseAddr("203.0.113.42"),
		AccountID:   "acct-static",
		AppID:       "app-static",
	}}
	ruleset := p.Render()
	if !strings.Contains(ruleset, "203.0.113.42") {
		t.Fatalf("rendered host policy has no SNAT to the customer IP:\n%s", ruleset)
	}
	cmd := exec.Command("nft", "-c", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft -c -f rejected the static-egress host policy: %v\n%s\nruleset:\n%s", err, out, ruleset)
	}
}
