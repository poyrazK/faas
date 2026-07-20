package netns

import (
	"strconv"
	"strings"
	"testing"
)

// TestHostPolicyRenderHasFlushAndShebang — the file is exec'd directly by
// `nft -f` on Linux; shebang + flush must appear before the table so any
// prior ruleset is wiped.
func TestHostPolicyRenderHasFlushAndShebang(t *testing.T) {
	out := DefaultHostPolicy.Render()
	if !strings.HasPrefix(out, "#!/usr/sbin/nft -f") {
		t.Errorf("missing shebang; first line was %q", strings.SplitN(out, "\n", 2)[0])
	}
	if !strings.Contains(out, "\nflush ruleset\n") {
		t.Error("missing `flush ruleset` before the table")
	}
}

// TestHostPolicyRenderForwardsViaBridge — the typo regression: the forward
// chain's allow rule MUST use `br-tenants` (the actual bridge name), not the
// old `faas-tenant-bridge` that exists in the pre-#27 ansible template.
func TestHostPolicyRenderForwardsViaBridge(t *testing.T) {
	out := DefaultHostPolicy.Render()
	want := `iif "br-tenants" oifname "eth0" accept`
	if !strings.Contains(out, want) {
		t.Errorf("forward allow rule missing or wrong; want %q in:\n%s", want, out)
	}
	// Anti-regression: the dead name must be gone.
	if strings.Contains(out, "faas-tenant-bridge") {
		t.Errorf("rendered ruleset references the dead name `faas-tenant-bridge`; see #27 history:\n%s", out)
	}
}

// TestHostPolicyForwardDefaultDrop — both filter chains must default-drop.
// A ruleset that defaults-accept would silently let tenant traffic through.
// See: spec §11 ("Tenant egress: deny …"), CLAUDE.md ("ship-blocking").
func TestHostPolicyForwardDefaultDrop(t *testing.T) {
	out := DefaultHostPolicy.Render()
	for _, want := range []string{
		"chain input {",
		"type filter hook input priority 0; policy drop;",
		"chain forward {",
		"type filter hook forward priority 0; policy drop;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ruleset missing %q", want)
		}
	}
	// The output chain defaults accept (host outbound isn't filtered).
	if !strings.Contains(out, "type filter hook output priority 0; policy accept;") {
		t.Error("output chain must default-accept")
	}
}

// TestHostPolicyRenderDeniesAllSMTPPorts — table-driven over the SMTP deny
// list. Every port in spec §11 ("deny 25, 465, 587") must render as a drop.
//
// Reordering or silently dropping a port would let the Hetzner abuse desk
// come knocking — see spec §7 founding doc R6 ("spam = existential").
func TestHostPolicyRenderDeniesAllSMTPPorts(t *testing.T) {
	out := DefaultHostPolicy.Render()
	start := strings.Index(out, "tcp dport { ")
	if start < 0 {
		t.Fatalf("no tcp dport deny line in ruleset:\n%s", out)
	}
	end := strings.Index(out[start:], " } drop")
	if end < 0 {
		t.Fatalf("malformed tcp dport deny line:\n%s", out)
	}
	dportLine := out[start : start+end]
	for _, p := range DefaultHostPolicy.ForwardDenyTCPPorts {
		needle := strconv.Itoa(p)
		if !strings.Contains(dportLine, needle) {
			t.Errorf("tcp port %s not in deny set; line %q", needle, dportLine)
		}
	}
}

// TestHostPolicyRenderDeniesRFC1918AndMetadata — table-driven over the CIDR
// deny list. Every range in spec §11 ("RFC1918 + link-local + metadata") must
// render as a drop.
func TestHostPolicyRenderDeniesRFC1918AndMetadata(t *testing.T) {
	out := DefaultHostPolicy.Render()
	dportLineIdx := strings.Index(out, "ip daddr { ")
	if dportLineIdx < 0 {
		t.Fatalf("no ip daddr line in ruleset:\n%s", out)
	}
	end := strings.Index(out[dportLineIdx:], " } drop")
	if end < 0 {
		t.Fatalf("malformed ip daddr line:\n%s", out)
	}
	dportLine := out[dportLineIdx : dportLineIdx+end]
	for _, cidr := range DefaultHostPolicy.ForwardDenyCIDRs {
		if !strings.Contains(dportLine, cidr) {
			t.Errorf("CIDR %s not in ip daddr deny set; line %q", cidr, dportLine)
		}
	}
}

// TestHostPolicyRenderDeniesIPv6LinkLocalAndULA — table-driven over the IPv6
// CIDR deny list. The list mirrors pkg/oci/egress.go::deniedCIDRv6 per ADR-023
// ("spec §11 is IPv4-only; fe80::/10 + ULA + multicast unblocked"). Every
// range must render as a `ip6 daddr { … } drop` line — a missing entry is a
// lateral-movement / metadata-exposure regression.
func TestHostPolicyRenderDeniesIPv6LinkLocalAndULA(t *testing.T) {
	out := DefaultHostPolicy.Render()
	lineIdx := strings.Index(out, "ip6 daddr { ")
	if lineIdx < 0 {
		t.Fatalf("no ip6 daddr line in ruleset:\n%s", out)
	}
	end := strings.Index(out[lineIdx:], " } drop")
	if end < 0 {
		t.Fatalf("malformed ip6 daddr line:\n%s", out)
	}
	denyLine := out[lineIdx : lineIdx+end]
	for _, cidr := range DefaultHostPolicy.ForwardDenyIPv6CIDRs {
		if !strings.Contains(denyLine, cidr) {
			t.Errorf("CIDR %s not in ip6 daddr deny set; line %q", cidr, denyLine)
		}
	}
	// No `meta nfproto` wrapper — the table is `inet faas` so family is
	// implicit, matching the v4 line above (ADR-023 rejected alternative).
	if strings.Contains(out, "meta nfproto") {
		t.Errorf("ip6 daddr rule wrapped in `meta nfproto`; ADR-023 chose the implicit form")
	}
}

// TestHostPolicyRenderBridgeNameParam — vary BridgeName and confirm the
// rendered ruleset substitutes correctly. Catches any future "hard-coded
// `br-tenants`" that bypasses the field.
func TestHostPolicyRenderBridgeNameParam(t *testing.T) {
	p := DefaultHostPolicy
	p.BridgeName = "custom-bridge"
	out := p.Render()
	if !strings.Contains(out, `iifname "custom-bridge" accept`) {
		t.Error("input chain did not pick up the BridgeName substitution")
	}
	if !strings.Contains(out, `iif "custom-bridge" oifname "eth0" accept`) {
		t.Error("forward chain did not pick up the BridgeName substitution")
	}
	if strings.Contains(out, "br-tenants") {
		t.Errorf("stray `br-tenants` in the substituted ruleset:\n%s", out)
	}
}

// TestHostPolicyRenderPanicsOnEmptyRequiredField — the renderer hard-fails
// rather than writing a broken ruleset that defaults to "drop everything" or
// "accept everything". Both are silent killers.
func TestHostPolicyRenderPanicsOnEmptyRequiredField(t *testing.T) {
	for _, mut := range []func(*HostPolicy){
		func(p *HostPolicy) { p.BridgeName = "" },
		func(p *HostPolicy) { p.PublicIface = "" },
	} {
		p := DefaultHostPolicy
		mut(&p)
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Error("expected panic on empty required field")
				}
			}()
			_ = p.Render()
		}()
	}
}
