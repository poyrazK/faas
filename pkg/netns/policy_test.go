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

// TestHostPolicyForwardDeniesComeBeforeBroadAllow locks the section-11 fix
// from PR-#122: nftables is first-match, so the broad bridged-tenant
// allow (`iif "br-tenants" oifname "eth0" accept`) MUST sit AFTER the
// SMTP / RFC1918 / IPv6 drops, otherwise the denylist is theater for
// bridged tenant traffic -- every allowed packet matches the broad
// rule first and never reaches the drops. Asserted per-rule (not
// block) on the isolated forward chain so a future reorder within the
// denylist cannot sneak a deny line behind the broad allow, AND so the
// established,related accept stays first (its daddr ∊ 10.100.0.0/16 ⊆
// 10.0.0.0/8 would otherwise hit the new RFC1918 drop and break reply
// traffic on published connections).
func TestHostPolicyForwardDeniesComeBeforeBroadAllow(t *testing.T) {
	out := DefaultHostPolicy.Render()
	forward := extractChain(t, out, "forward")
	// Pin the established/related accept at the top. Replies to inbound
	// DNAT'd connections carry daddr ∊ 10.100.0.0/16 which is a subset of
	// the new 10.0.0.0/8 RFC1918 drop -- they MUST survive the chain.
	// `extractChain` returns the body that follows `chain forward {`,
	// which starts with "\n    type filter hook forward ..." -- the
	// first non-empty, non-metadata rule is what we want.
	firstRule := firstRuleLine(forward)
	if firstRule != "ct state established,related accept" {
		t.Errorf("first forward rule must be `ct state established,related accept`, got %q\nchain:\n%s", firstRule, forward)
	}
	broadAllow := `iif "br-tenants" oifname "eth0" accept`
	broadIdx := strings.Index(forward, broadAllow)
	if broadIdx < 0 {
		t.Fatalf("forward chain missing broad allow %q\nchain:\n%s", broadAllow, forward)
	}
	denies := []string{
		"tcp dport { 25,465,587 } drop",
		"ip daddr { 10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 100.64.0.0/10 } drop",
		"ip6 daddr { fe80::/10 fc00::/7 ff00::/8 ::1/128 ::/128 } drop",
	}
	for _, d := range denies {
		idx := strings.Index(forward, d)
		if idx < 0 {
			t.Errorf("deny line missing in forward chain: %q", d)
			continue
		}
		if idx > broadIdx {
			t.Errorf("deny %q (idx %d) must precede broad allow (idx %d)\nchain:\n%s", d, idx, broadIdx, forward)
		}
	}
}

// extractChain returns the body of the named filter chain (the lines
// between `chain <name> {` and its matching depth-zero `}`). Used by
// tests that need to assert per-rule ordering WITHOUT scanning other
// chains for incidental matches or being fooled by the `}` inside
// port set syntax like `{ 25,465,587 } drop`. nftables Render emits
// `chain <name> {` on one line and the closer `  }` (two leading
// spaces) at depth zero, so we walk the body tracking brace depth and
// return everything strictly between depth-1 and depth-0.
func extractChain(t *testing.T, rendered, name string) string {
	t.Helper()
	openTag := "chain " + name + " {"
	start := strings.Index(rendered, openTag)
	if start < 0 {
		t.Fatalf("chain %q not found in rendered ruleset:\n%s", name, rendered)
	}
	body := rendered[start+len(openTag):]
	depth := 1
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return body[:i]
			}
		}
	}
	t.Fatalf("chain %q has no depth-zero `}`:\n%s", name, body)
	return ""
}

// firstRuleLine returns the first non-blank, non-`type filter hook ...;
// policy drop;` metadata line of a chain body. The metadata header is
// emitted before any rule and counts as chain config, not a rule.
func firstRuleLine(chainBody string) string {
	for _, ln := range strings.Split(chainBody, "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "type filter hook") && strings.Contains(trimmed, "policy drop") {
			continue
		}
		return trimmed
	}
	return ""
}

// TestHostPolicyForwardIPv6ImmediatelyFollowsIPv4 locks ADR-023's
// v4/v6 adjacency in the HOST renderer (the per-netns adjacency is
// already covered by the per-netns renderer -- this is the host-side
// pin). Scoped to the forward chain via extractChain so a future
// `ip daddr` line in some unrelated context cannot accidentally
// satisfy the assertion. Reordering the v4 and v6 lines, or inserting
// any rule between them, breaks the "next to each other" mandate.
func TestHostPolicyForwardIPv6ImmediatelyFollowsIPv4(t *testing.T) {
	out := DefaultHostPolicy.Render()
	forward := extractChain(t, out, "forward")
	v4Idx := strings.Index(forward, "ip daddr {")
	v6Idx := strings.Index(forward, "ip6 daddr {")
	if v4Idx < 0 || v6Idx < 0 {
		t.Fatalf("missing one of v4/v6 daddr lines (v4=%d v6=%d) in forward chain:\n%s", v4Idx, v6Idx, forward)
	}
	if v6Idx <= v4Idx {
		t.Errorf("ip6 daddr line (idx %d) must come AFTER ip daddr line (idx %d) -- ADR-023 adjacency", v6Idx, v4Idx)
	}
	// Adjacency = only whitespace and the `}` between the two lines.
	between := forward[v4Idx:v6Idx]
	after := strings.SplitN(between, "\n", 2)[1]
	if strings.TrimSpace(after) != "" {
		t.Errorf("v4 daddr and v6 daddr are not adjacent; between them:\n%q", between)
	}
}
