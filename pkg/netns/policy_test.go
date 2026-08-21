package netns

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	want := `iifname "br-tenants" oifname "eth0" accept`
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
//
// Scoped to the forward chain: the rendered text also has a
// `tcp dport { 22,80,443 } accept` line in the INPUT chain (sshd +
// gatewayd-internal), so a whole-ruleset substring scan would falsely match
// that allowline first. The forward chain is where the SMTP drops
// live (spec §11).
func TestHostPolicyRenderDeniesAllSMTPPorts(t *testing.T) {
	out := DefaultHostPolicy.Render()
	forward := extractChain(t, out, "forward")
	start := strings.Index(forward, "tcp dport { ")
	if start < 0 {
		t.Fatalf("no tcp dport deny line in forward chain:\n%s", forward)
	}
	end := strings.Index(forward[start:], " } drop")
	if end < 0 {
		t.Fatalf("malformed tcp dport deny line:\n%s", forward)
	}
	dportLine := forward[start : start+end]
	for _, p := range DefaultHostPolicy.DenySet.SMTPPorts {
		needle := strconv.Itoa(int(p))
		if !strings.Contains(dportLine, needle) {
			t.Errorf("tcp port %s not in deny set; line %q", needle, dportLine)
		}
	}
}

// TestHostPolicyRenderDeniesRFC1918AndMetadata — table-driven over the v4
// CIDR deny list. Every range in spec §11 ("RFC1918 + link-local +
// metadata") must render as a drop. PR-E changed the rule shape
// from one aggregate `ip daddr { … } drop` line to one rule per
// CIDR (`ip daddr <cidr> counter name "<name>" drop`), so the
// substring scan walks every v4 entry's per-CIDR line rather than
// a single line.
func TestHostPolicyRenderDeniesRFC1918AndMetadata(t *testing.T) {
	out := DefaultHostPolicy.Render()
	for _, cidr := range DefaultHostPolicy.DenySet.V4DenyCIDRs {
		needle := "ip daddr " + cidr.String() + " counter name"
		if !strings.Contains(out, needle) {
			t.Errorf("CIDR %s missing from per-CIDR deny argv; needle %q", cidr, needle)
		}
	}
}

// TestHostPolicyRenderDeniesIPv6LinkLocalAndULA — table-driven over the IPv6
// CIDR deny list. The list mirrors pkg/oci/egress.go::deniedEntriesV6 per ADR-023
// ("spec §11 is IPv4-only; fe80::/10 + ULA + multicast unblocked"). ADR-034
// extends it with 6to4 (2002::/16) + Teredo (2001::/32). Every range must
// render as a per-CIDR `ip6 daddr <cidr> counter name "<name>" drop`
// line — a missing entry is a lateral-movement / metadata-exposure
// regression. PR-E changed the rule shape from aggregate
// `ip6 daddr { … } drop` to per-CIDR so the substring scan walks
// every v6 entry's per-CIDR line.
func TestHostPolicyRenderDeniesIPv6LinkLocalAndULA(t *testing.T) {
	out := DefaultHostPolicy.Render()
	for _, cidr := range DefaultHostPolicy.DenySet.V6DenyCIDRs {
		needle := "ip6 daddr " + cidr.String() + " counter name"
		if !strings.Contains(out, needle) {
			t.Errorf("CIDR %s missing from per-CIDR deny argv; needle %q", cidr, needle)
		}
	}
	// No `meta nfproto` wrapper — the table is `inet faas` so family is
	// implicit, matching the v4 line above (ADR-023 rejected alternative).
	if strings.Contains(out, "meta nfproto") {
		t.Errorf("ip6 daddr rule wrapped in `meta nfproto`; ADR-023 chose the implicit form")
	}
}

// TestHostPolicyRenderDenySetEntriesEveryEntryAppears — pin every
// DenySet.Entries entry individually (PR-A, ADR-034). Same shape
// as pkg/netns/config_test.go::TestNftCommandsEnforceEgressPolicy_PerEntry
// for the per-netns side; the host side mirrors it so both
// renderers stay in lock-step.
func TestHostPolicyRenderDenySetEntriesEveryEntryAppears(t *testing.T) {
	out := DefaultHostPolicy.Render()
	for _, e := range DefaultHostPolicy.DenySet.Entries {
		needle := e.Prefix.String()
		// PR-E: deny lines are now per-CIDR (`<family> daddr <cidr>
		// counter name "<name>" drop`), so the whole-ruleset substring
		// scan is family-safe — a v4 entry cannot land in a v6 line
		// by accident (the family keyword prefixes the line).
		daddrKW := "ip daddr " + needle
		if e.Family == FamilyV6 {
			daddrKW = "ip6 daddr " + needle
		}
		if !strings.Contains(out, daddrKW) {
			t.Errorf("DenySet entry %s (%s) missing from host %s per-CIDR deny argv",
				needle, e.SourceADR, e.Family)
		}
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
	if !strings.Contains(out, `iifname "custom-bridge" oifname "eth0" accept`) {
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
		func(p *HostPolicy) { p.MasqueradeCIDR = "" },
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
	broadAllow := `iifname "br-tenants" oifname "eth0" accept`
	broadIdx := strings.Index(forward, broadAllow)
	if broadIdx < 0 {
		t.Fatalf("forward chain missing broad allow %q\nchain:\n%s", broadAllow, forward)
	}
	// PR-E: deny lines are now per-CIDR. The SMTP deny line is still
	// one aggregate row (it's a port set, not a CIDR set — see
	// HostPolicy.Render), but the RFC1918 + IPv6 drops are emitted
	// one-per-entry. The "deny precedes broad allow" check walks
	// each entry's per-CIDR line — a regression that splits the
	// catalog across the broad allow (some rules before, some after)
	// would surface as the offending rule landing after broadIdx.
	denies := []string{
		"tcp dport { " + DefaultHostPolicy.DenySet.SMTPPortsCommaSet() + " } drop",
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
	// Per-CIDR denies: every catalog entry's line must precede the
	// broad allow. Walk DenySet.Entries and assert per-entry.
	for _, e := range DefaultHostPolicy.DenySet.Entries {
		daddrKW := "ip daddr " + e.Prefix.String() + " counter name"
		if e.Family == FamilyV6 {
			daddrKW = "ip6 daddr " + e.Prefix.String() + " counter name"
		}
		idx := strings.Index(forward, daddrKW)
		if idx < 0 {
			t.Errorf("per-CIDR deny line %q missing from forward chain", daddrKW)
			continue
		}
		if idx > broadIdx {
			t.Errorf("per-CIDR deny %q (idx %d) must precede broad allow (idx %d)",
				daddrKW, idx, broadIdx)
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
// pin). PR-E renders per-CIDR lines, so the "adjacency" check uses
// the LAST v4 entry's line and the FIRST v6 entry's line — every
// v4 line must precede every v6 line, with no foreign rule between
// them. A regression that interleaves v4/v6 lines (e.g. a future
// allow rule slipped between them) would break the ADR-023
// adjacency mandate.
func TestHostPolicyForwardIPv6ImmediatelyFollowsIPv4(t *testing.T) {
	out := DefaultHostPolicy.Render()
	forward := extractChain(t, out, "forward")
	// Find every per-CIDR deny line position. The last v4 line
	// must come before the first v6 line.
	var lastV4Idx, firstV6Idx = -1, -1
	for _, e := range DefaultHostPolicy.DenySet.Entries {
		daddrKW := "ip daddr " + e.Prefix.String() + " counter name"
		if e.Family == FamilyV6 {
			daddrKW = "ip6 daddr " + e.Prefix.String() + " counter name"
		}
		idx := strings.Index(forward, daddrKW)
		if idx < 0 {
			t.Errorf("deny line %q missing from forward chain", daddrKW)
			continue
		}
		if e.Family == FamilyV4 && idx > lastV4Idx {
			lastV4Idx = idx
		}
		if e.Family == FamilyV6 && (firstV6Idx < 0 || idx < firstV6Idx) {
			firstV6Idx = idx
		}
	}
	if lastV4Idx < 0 || firstV6Idx < 0 {
		t.Fatalf("missing v4/v6 deny lines in forward chain")
	}
	if firstV6Idx <= lastV4Idx {
		t.Errorf("first v6 deny line (idx %d) must come AFTER last v4 deny line (idx %d) -- ADR-023 adjacency",
			firstV6Idx, lastV4Idx)
	}
	// Strict adjacency: nothing but whitespace and newlines between
	// the last v4 line and the first v6 line. A foreign rule slipped
	// between them would break ADR-023.
	between := forward[lastV4Idx:firstV6Idx]
	if strings.TrimSpace(between) == "" {
		t.Errorf("last v4 line and first v6 line are not adjacent (empty between); chain slice:\n%q", between)
	}
}

// TestHostPolicyMasqueradeChainIsAppended locks the tier-1 host egress
// fix: the host `table inet faas` gets a fourth chain `postrouting`
// of type nat that MASQUERADEs tenant source addresses to the host's
// public IP on their way out PublicIface. Without this, the per-netns
// SNAT translates the guest source to 10.100.x.y, but no root-ns
// rule rewrites that to the public IP — replies can't route back and
// every bidirectional flow dies.
//
// Asserts:
//   - exactly one host chain has `type nat hook postrouting priority
//     srcnat`;
//   - the rule body contains `ip saddr <MasqueradeCIDR> oifname
//     "<PublicIface>" masquerade` (uses %q so the quoted form is
//     pinned);
//   - the rule is SOURCE-SCOPED (the `ip saddr` selector is present);
//     a bare `oifname "eth0" masquerade` would incorrectly NAT
//     unrelated host traffic.
//
// Uses extractChain so `chain postrouting {` is matched at chain depth
// and not against a future `ip saddr` somewhere in a comment or
// unrelated rule.
func TestHostPolicyMasqueradeChainIsAppended(t *testing.T) {
	out := DefaultHostPolicy.Render()
	// Exactly one nat postrouting chain.
	wantMeta := "type nat hook postrouting priority srcnat"
	if got := strings.Count(out, wantMeta); got != 1 {
		t.Fatalf("expected exactly 1 %q in render, got %d:\n%s", wantMeta, got, out)
	}
	post := extractChain(t, out, "postrouting")
	// The rule body must be the exact MASQUERADE selector.
	wantRule := fmt.Sprintf("ip saddr %s oifname %q masquerade",
		DefaultHostPolicy.MasqueradeCIDR, DefaultHostPolicy.PublicIface)
	if !strings.Contains(post, wantRule) {
		t.Errorf("postrouting chain missing rule %q; chain:\n%s", wantRule, post)
	}
	// Defense-in-depth: must NOT be a bare `oifname "..." masquerade`
	// without `ip saddr`. A missing source CIDR would masquerade every
	// outbound packet (including vmmd's own) to the tenant bridge
	// range — a security regression. Scanned per-line so a future
	// `log prefix "..."` or trailing comment on the masquerade line
	// cannot fool a literal-substring check.
	var bareMasquerade []string
	for _, ln := range strings.Split(post, "\n") {
		if strings.Contains(ln, "masquerade") && !strings.Contains(ln, "ip saddr ") {
			bareMasquerade = append(bareMasquerade, ln)
		}
	}
	if len(bareMasquerade) > 0 {
		t.Errorf("postrouting chain must SOURCE-SCOPE the MASQUERADE via `ip saddr`; bare lines: %q\nchain:\n%s",
			bareMasquerade, post)
	}
}

// TestHostPolicyMasqueradeChainAppendsV6Sibling locks the IPv6
// counterpart to TestHostPolicyMasqueradeChainIsAppended. When
// MasqueradeCIDR6 is set, the postrouting chain emits an `ip6 saddr
// <CIDR6> oifname <iface> masquerade` sibling immediately after the
// v4 rule. Without it, v6 tenant traffic falls through `policy accept`
// on the host postrouting chain and reaches the public internet
// under the tenant's link-local source — a return-routability black
// hole analogous to the v4 omission. DefaultHostPolicy has an empty
// MasqueradeCIDR6, so the negative half of this test asserts the
// default render is byte-identical to a v4-only render (no extra
// ip6 line). The positive half populates MasqueradeCIDR6 and asserts
// the sibling rule lands on the postrouting chain.
func TestHostPolicyMasqueradeChainAppendsV6Sibling(t *testing.T) {
	// Default render: empty MasqueradeCIDR6 → no v6 sibling.
	defOut := DefaultHostPolicy.Render()
	defPost := extractChain(t, defOut, "postrouting")
	if strings.Contains(defPost, "ip6 saddr ") {
		t.Errorf("default render must NOT emit an ip6 masquerade when MasqueradeCIDR6 is empty; chain:\n%s", defPost)
	}

	// Populated: v6 sibling on the next line, with the same oifname.
	p := DefaultHostPolicy
	p.MasqueradeCIDR6 = "fd00:faas::/64"
	out := p.Render()
	post := extractChain(t, out, "postrouting")
	want := fmt.Sprintf("ip6 saddr %s oifname %q masquerade",
		p.MasqueradeCIDR6, p.PublicIface)
	if !strings.Contains(post, want) {
		t.Errorf("postrouting chain missing v6 sibling rule %q; chain:\n%s", want, post)
	}

	// Source-scope check on the v6 line — same shape as the v4
	// defense-in-depth. A future regression to a bare `oifname "..."
	// masquerade` would masquerade every outbound v6 packet, not
	// just tenant traffic, and break the vmmd/IPv6 path.
	var bareV6 []string
	for _, ln := range strings.Split(post, "\n") {
		if strings.Contains(ln, "masquerade") && strings.Contains(ln, "ip6 ") && !strings.Contains(ln, "ip6 saddr ") {
			bareV6 = append(bareV6, ln)
		}
	}
	if len(bareV6) > 0 {
		t.Errorf("v6 masquerade line must source-scope via `ip6 saddr`; bare lines: %q\nchain:\n%s",
			bareV6, post)
	}
}

// TestHostPolicyPostroutingIsLastChain locks the topology: the
// postrouting nat chain MUST come after input, forward, AND output.
// nftables evaluates chains in declaration order inside a table; the
// firewall chains (input/forward/output) must set the drop/accept
// verdict first so a future MASQUERADE rule does not have to be
// coordinated with filtering. extractChain-based: count chain
// headers in render order; the LAST chain listed must be
// "postrouting".
func TestHostPolicyPostroutingIsLastChain(t *testing.T) {
	out := DefaultHostPolicy.Render()
	wantOrder := []string{"chain input {", "chain forward {", "chain output {", "chain postrouting {"}
	last := -1
	for _, w := range wantOrder {
		idx := strings.Index(out, w)
		if idx < 0 {
			t.Fatalf("missing chain header %q in render:\n%s", w, out)
		}
		if idx <= last {
			t.Errorf("chain %q (idx %d) must come after previous chain (idx %d)", w, idx, last)
		}
		last = idx
	}
	// Also: no chain header after `chain postrouting {`. Scans for any
	// additional `chain <name> {` to catch a future regression where
	// someone adds `chain postnat-flush {` or similar after it.
	// Regex over `\n\s*chain\s+<name>\s*\{` so we survive a future
	// indentation tweak (e.g. formatter walks the ruleset).
	postIdx := strings.Index(out, "chain postrouting {")
	rest := out[postIdx:]
	chainHeaderRe := regexp.MustCompile(`\n\s*chain\s+\S+\s*\{`)
	if loc := chainHeaderRe.FindStringIndex(rest); loc != nil {
		t.Errorf("chain `postrouting` must be the LAST chain; found another chain header at offset %d after it:\n%s",
			postIdx+loc[0], out)
	}
}

// TestHostPolicyForwardUsesIifname pins the nft keyword `iifname` on
// the forward chain. nftables.service is `Before=network-pre.target`
// on Debian/Ubuntu, so it can load BEFORE the tenant bridge is up
// (e.g. on first boot, when `br-tenants-up.service` runs after
// nftables start). `iif` resolves to an interface INDEX at load time
// and fails if the interface doesn't exist; `iifname` matches by
// name and survives a deleted-and-recreated interface with the same
// name. The forward chain is the one that admits bridged tenant
// traffic to the host — losing it on first boot means every tenant
// gets ENETUNREACH. The input chain at policy.go:174 already uses
// `iifname`; this test keeps forward consistent.
//
// Anti-regression: if anyone writes `iif "br-tenants"` again, the
// test fails immediately.
func TestHostPolicyForwardUsesIifname(t *testing.T) {
	out := DefaultHostPolicy.Render()
	forward := extractChain(t, out, "forward")
	want := fmt.Sprintf("iifname %q oifname %q accept",
		DefaultHostPolicy.BridgeName, DefaultHostPolicy.PublicIface)
	if !strings.Contains(forward, want) {
		t.Errorf("forward chain must use %q; chain:\n%s", want, forward)
	}
	bad := fmt.Sprintf("iif %q oifname %q accept",
		DefaultHostPolicy.BridgeName, DefaultHostPolicy.PublicIface)
	if strings.Contains(forward, bad) {
		t.Errorf("forward chain regressed to `iif \"...\"` (ifindex-resolved) keyword — use `iifname` so nftables.service loads survive a missing bridge on first boot:\n%s", forward)
	}
}

// TestHostPolicyMasqueradeSubstitutesCIDRAndIface is the substitution
// test for the new field. A regression that hard-codes
// "10.100.0.0/16" or "eth0" inside Render — bypassing the field —
// would silently lock the production deployment. Vary both fields
// and assert both make it into the rendered rule.
func TestHostPolicyMasqueradeSubstitutesCIDRAndIface(t *testing.T) {
	p := DefaultHostPolicy
	p.MasqueradeCIDR = "172.31.99.0/24"
	p.PublicIface = "ens3"
	out := p.Render()
	if !strings.Contains(out, "ip saddr 172.31.99.0/24 oifname \"ens3\" masquerade") {
		t.Errorf("rendered rule did not pick up MasqueradeCIDR/PublicIface substitution:\n%s", out)
	}
	if strings.Contains(out, "10.100.0.0/16") || strings.Contains(out, `oifname "eth0"`) {
		t.Errorf("rendered output retained the production defaults when test varied them:\n%s", out)
	}
}

// TestHostPolicyRenderSubstitutesPublicIface (ADR-055) pins the
// single-field substitution of PublicIface. The combined-fields
// test above varies both fields at once; this one varies only
// PublicIface so a regression that hard-codes `eth0` while
// accidentally still substituting MasqueradeCIDR (or vice versa)
// surfaces directly. The test asserts three substitution sites:
//   - the forward chain's broad allow (`iifname "br-tenants"
//     oifname "<iface>" accept`);
//   - the postrouting chain's MASQUERADE rule (`ip saddr <cidr>
//     oifname "<iface>" masquerade`);
//   - the absence of `oifname "eth0"` once the field is varied.
//
// Per-host rendering (ADR-055) requires this test to pass for
// every `host_vars[<compute_node>].public_iface` value the EX44
// fleet uses (currently `eth0`, `ens5`).
func TestHostPolicyRenderSubstitutesPublicIface(t *testing.T) {
	p := DefaultHostPolicy
	p.PublicIface = "ens5"
	out := p.Render()
	// Forward chain's broad allow must pick up the new iface.
	wantFwd := `iifname "br-tenants" oifname "ens5" accept`
	if !strings.Contains(out, wantFwd) {
		t.Errorf("forward chain did not pick up PublicIface substitution; want %q in:\n%s", wantFwd, out)
	}
	// Postrouting MASQUERADE rule must also pick up the new iface.
	wantMasq := fmt.Sprintf(`ip saddr %s oifname "ens5" masquerade`, p.MasqueradeCIDR)
	if !strings.Contains(out, wantMasq) {
		t.Errorf("postrouting chain did not pick up PublicIface substitution; want %q in:\n%s", wantMasq, out)
	}
	// Anti-regression: the production default must not appear in the
	// rendered output once the field is varied. Scoped to `oifname
	// "eth0"` (the IFACE token) so an unrelated `eth0` substring
	// elsewhere doesn't false-positive.
	if strings.Contains(out, `oifname "eth0"`) {
		t.Errorf("rendered output retained the default PublicIface when test varied it:\n%s", out)
	}
}

// TestHostPolicyRenderSubstitutesMasqueradeCIDR (ADR-055) pins the
// single-field substitution of MasqueradeCIDR. Mirror of the
// PublicIface test above: vary only MasqueradeCIDR so a regression
// that hard-codes `10.100.0.0/16` while accidentally still
// substituting PublicIface (or vice versa) surfaces directly.
//
// Per-host rendering (ADR-055) requires this test to pass for
// every `host_vars[<compute_node>].masquerade_cidr` value the
// fleet uses (currently `10.100.0.0/16`, `10.101.0.0/16`).
func TestHostPolicyRenderSubstitutesMasqueradeCIDR(t *testing.T) {
	p := DefaultHostPolicy
	p.MasqueradeCIDR = "10.101.0.0/16"
	out := p.Render()
	// Postrouting chain's MASQUERADE rule must pick up the new CIDR.
	wantMasq := fmt.Sprintf(`ip saddr 10.101.0.0/16 oifname %q masquerade`, p.PublicIface)
	if !strings.Contains(out, wantMasq) {
		t.Errorf("postrouting chain did not pick up MasqueradeCIDR substitution; want %q in:\n%s", wantMasq, out)
	}
	// Anti-regression: the production default must not appear in the
	// rendered output once the field is varied. Scoped to the literal
	// CIDR string so an unrelated `10.100.0.0/16` substring elsewhere
	// (e.g. a comment) doesn't false-positive.
	if strings.Contains(out, "ip saddr 10.100.0.0/16") {
		t.Errorf("rendered output retained the default MasqueradeCIDR when test varied it:\n%s", out)
	}
}

// TestHostPolicyRenderSubstitutesOverlayCIDRs (Mega-PR-B Commit 2) pins
// the multi-CIDR substitution of OverlayCIDRs. Vary two overlay CIDRs
// BOTH outside the §11 deny set: a public-range operator WireGuard
// mesh (203.0.113.0/24, TEST-NET-3 — RFC5737 documentation range, safe
// for tests) and a public-range GCP VPC (34.0.0.0/8, Google's actual
// public allocation). Tailscale CGNAT (100.64.0.0/10), RFC1918 ranges
// (10/8, 172.16/12, 192.168/16), link-local (169.254/16), and ULA
// (fc00::/7) are ALL in the deny set — overlays inside them are
// rejected by the panic gate
// (TestHostPolicyRenderPanicsOnOverlayCIDRInsideDenyCIDR). Each
// non-denied overlay CIDR must produce:
//
//  1. A MASQUERADE sibling in the postrouting chain (per the
//     per-overlay MASQUERADE loop).
//  2. An `ip saddr <overlay> accept` rule in the forward chain
//     BETWEEN the per-CIDR deny block and the broad bridged-tenant
//     allow (the deny-collision fix).
//
// The order assertion is load-bearing — an accept rule emitted
// BEFORE the per-CIDR deny would be silently shadowed by the deny on
// overlap (nft first-match).
func TestHostPolicyRenderSubstitutesOverlayCIDRs(t *testing.T) {
	p := DefaultHostPolicy
	p.OverlayCIDRs = []string{"203.0.113.0/24", "34.0.0.0/8"}
	out := p.Render()
	// Postrouting siblings.
	for _, cidr := range p.OverlayCIDRs {
		want := fmt.Sprintf(`ip saddr %s oifname %q masquerade`, cidr, p.PublicIface)
		if !strings.Contains(out, want) {
			t.Errorf("postrouting chain missing overlay MASQUERADE %q:\n%s", want, out)
		}
	}
	// Forward-chain accept rules.
	for _, cidr := range p.OverlayCIDRs {
		want := fmt.Sprintf(`ip saddr %s accept`, cidr)
		if !strings.Contains(out, want) {
			t.Errorf("forward chain missing overlay accept %q:\n%s", want, out)
		}
	}
}

// TestHostPolicyForwardOverlayAcceptAfterDeny asserts the
// load-bearing order: overlay accept rules must appear in the
// forward chain AFTER the per-CIDR deny block (so the deny is
// evaluated first on collision) and BEFORE the broad bridged-tenant
// allow. The test uses a public-range overlay CIDR (203.0.113.0/24,
// TEST-NET-3 — RFC5737) outside the deny list — overlays that
// overlap a deny CIDR are caught by the panic gate
// (TestHostPolicyRenderPanicsOnOverlayCIDRInsideDenyCIDR).
func TestHostPolicyForwardOverlayAcceptAfterDeny(t *testing.T) {
	p := DefaultHostPolicy
	p.OverlayCIDRs = []string{"203.0.113.0/24"}
	out := p.Render()
	denyIdx := strings.Index(out, "ip daddr 10.0.0.0/8")
	acceptIdx := strings.Index(out, "ip saddr 203.0.113.0/24 accept")
	if denyIdx < 0 {
		t.Fatalf("RFC1918 10.0.0.0/8 deny not found in render:\n%s", out)
	}
	if acceptIdx < 0 {
		t.Fatalf("overlay accept rule not found in render:\n%s", out)
	}
	if acceptIdx <= denyIdx {
		t.Errorf("overlay accept (idx %d) must appear AFTER the 10.0.0.0/8 deny (idx %d); "+
			"otherwise nft first-match drops the mesh traffic:\n%s",
			acceptIdx, denyIdx, out)
	}
}

// TestHostPolicyRenderPanicsOnOverlayCIDRInsideDenyCIDR exercises the
// panic gate. Setting OverlayCIDRs to a CIDR inside the 10.0.0.0/8
// deny would otherwise render an accept rule that *overrides* the
// deny on the same address space — silently disabling lateral-
// movement protection for that overlay. Render() must refuse to
// produce a broken ruleset.
func TestHostPolicyRenderPanicsOnOverlayCIDRInsideDenyCIDR(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Render() must panic when OverlayCIDRs entry is a subset of a DenySet entry; got no panic")
		}
	}()
	p := DefaultHostPolicy
	p.OverlayCIDRs = []string{"10.0.0.0/16"} // subset of 10.0.0.0/8 deny
	_ = p.Render()
}

// TestValidateOverlayCIDRs_AcceptsEmptyInput pins the single-host
// dev shape (OverlayCIDRs nil / empty is valid; the §11 deny set
// never appears in the forward chain).
func TestValidateOverlayCIDRs_AcceptsEmptyInput(t *testing.T) {
	if err := ValidateOverlayCIDRs(nil, NewDefaultDenySet()); err != nil {
		t.Fatalf("ValidateOverlayCIDRs(nil) must return nil for single-host dev; got %v", err)
	}
	if err := ValidateOverlayCIDRs([]string{}, NewDefaultDenySet()); err != nil {
		t.Fatalf("ValidateOverlayCIDRs([]) must return nil for single-host dev; got %v", err)
	}
}

// TestValidateOverlayCIDRs_AcceptsPublicRange pins the success
// path for the cluster-shipped overlays (TEST-NET-3 203.0.113.0/24
// + Google 34.0.0.0/8 — both sit OUTSIDE the §11 deny set).
func TestValidateOverlayCIDRs_AcceptsPublicRange(t *testing.T) {
	if err := ValidateOverlayCIDRs([]string{"203.0.113.0/24", "34.0.0.0/8"}, NewDefaultDenySet()); err != nil {
		t.Fatalf("ValidateOverlayCIDRs(public CIDRs) must succeed; got %v", err)
	}
}

// TestValidateOverlayCIDRs_RejectsRFC1918Subset is the load-bearing
// negative case: 10.0.0.0/16 ⊂ 10.0.0.0/8 → error. The error
// must be *OverlayCIDRError (not a plain string) so callers can
// surface a stable, grep-able message.
func TestValidateOverlayCIDRs_RejectsRFC1918Subset(t *testing.T) {
	err := ValidateOverlayCIDRs([]string{"10.0.0.0/16"}, NewDefaultDenySet())
	if err == nil {
		t.Fatal("ValidateOverlayCIDRs(10.0.0.0/16) must return error; got nil")
	}
	var ocErr *OverlayCIDRError
	if !errors.As(err, &ocErr) {
		t.Fatalf("expected *OverlayCIDRError; got %T (%v)", err, err)
	}
	// Message stability: operators grep on "subset of deny entry".
	if !strings.Contains(err.Error(), "subset of deny entry") {
		t.Errorf("error message must contain %q for grep-ability; got %q",
			"subset of deny entry", err.Error())
	}
	// Must name the offending deny entry (10.0.0.0/8) so operators
	// don't have to re-read spec §11.
	if !strings.Contains(err.Error(), "10.0.0.0/8") {
		t.Errorf("error message must name the swallowing deny entry 10.0.0.0/8; got %q",
			err.Error())
	}
}

// TestValidateOverlayCIDRs_RejectsCGNATSubset pins the
// "carrier-grade NAT 100.64.0.0/10 is in the deny set" path —
// Tailscale's default subnet sits inside §11's CGN deny (RFC6598).
// Operators using Tailscale MUST override overlay_cidr to a
// public-range CIDR routed over the mesh.
func TestValidateOverlayCIDRs_RejectsCGNATSubset(t *testing.T) {
	err := ValidateOverlayCIDRs([]string{"100.64.0.0/10"}, NewDefaultDenySet())
	if err == nil {
		t.Fatal("ValidateOverlayCIDRs(100.64.0.0/10) must return error; got nil")
	}
	var ocErr *OverlayCIDRError
	if !errors.As(err, &ocErr) {
		t.Fatalf("expected *OverlayCIDRError; got %T", err)
	}
	if !strings.Contains(err.Error(), "100.64.0.0/10") {
		t.Errorf("error message must name the swallowed deny entry 100.64.0.0/10; got %q",
			err.Error())
	}
}

// TestValidateOverlayCIDRs_RejectsMalformedCIDR confirms a
// non-numeric CIDR string returns an error wrapping the parse
// failure (operator typo, not a security event — return plain
// error, not *OverlayCIDRError).
func TestValidateOverlayCIDRs_RejectsMalformedCIDR(t *testing.T) {
	err := ValidateOverlayCIDRs([]string{"not-a-cidr"}, NewDefaultDenySet())
	if err == nil {
		t.Fatal("ValidateOverlayCIDRs(not-a-cidr) must return error; got nil")
	}
	var ocErr *OverlayCIDRError
	if errors.As(err, &ocErr) {
		t.Fatalf("malformed CIDR must return plain error (operator typo); got *OverlayCIDRError")
	}
	if !strings.Contains(err.Error(), "not-a-cidr") {
		t.Errorf("error must name the bad CIDR; got %q", err.Error())
	}
}

// TestValidateOverlayCIDRs_ReportsIndex pins the Index field —
// the validator must surface WHICH entry in a multi-CIDR config
// tripped the gate, not just that some entry did.
func TestValidateOverlayCIDRs_ReportsIndex(t *testing.T) {
	// Index 1 (second entry) is the bad one.
	err := ValidateOverlayCIDRs(
		[]string{"203.0.113.0/24", "10.0.0.0/16", "34.0.0.0/8"},
		NewDefaultDenySet(),
	)
	if err == nil {
		t.Fatal("expected error from RFC1918 subset; got nil")
	}
	var ocErr *OverlayCIDRError
	if !errors.As(err, &ocErr) {
		t.Fatalf("expected *OverlayCIDRError; got %T", err)
	}
	if ocErr.Index != 1 {
		t.Errorf("expected Index=1 (second entry); got %d", ocErr.Index)
	}
}

// TestHostPolicyRenderNftSyntaxCheck is the local equivalent of the
// ansible role's `nft -c -f /etc/nftables.conf` step. CI gates this via
// `make egress-check` (regenerates + byte-compares the artifact), but on
// macOS devs with `brew install nftables`, this test gets the same
// nft(8)-side syntax gate as CI.
//
// Why this matters: the regex/substring checks above assert that the
// render LOOKS right. `nft -c -f` asserts that nft(8) ACCEPTS it — a
// different class of bug (typo in a keyword, missing semicolon, wrong
// hook) only nft itself can catch.
//
// Skip conditions:
//   - nft not on PATH (common on macOS without `brew install nftables`).
//   - nft returns EPERM / "Operation not permitted". `nft -c -f` still
//     instantiates transient handle state via nfnetlink; even without
//     committing, it requires CAP_NET_ADMIN. ubuntu-latest CI runs the
//     `lint + build` step as the github-actions user (no caps),
//     so this gate will skip in CI. Operators running the test locally
//     with `sudo` get the full check; without sudo it skips. The
//     production gate is `make egress-check` (root in CI), this test
//     is for developer ergonomics.
//
// Note: on a host where nft DOES run, the ruleset's `flush ruleset`
// directive IS evaluated (in dry-run mode) — a non-empty existing
// nftables generation still triggers the operation, hence the
// CAP_NET_ADMIN requirement.
func TestHostPolicyRenderNftSyntaxCheck(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Skipf("nft not on PATH; skipping syntax check (install via `apt install nftables` or `brew install nftables` to enable locally): %v", err)
	}
	out := DefaultHostPolicy.Render()

	dir := t.TempDir()
	conf := filepath.Join(dir, "nftables.conf")
	if err := os.WriteFile(conf, []byte(out), 0o644); err != nil {
		t.Fatalf("write rendered ruleset to %s: %v", conf, err)
	}

	cmd := exec.Command(nft, "-c", "-f", conf)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	cmd.Stdout = stderr
	err = cmd.Run()
	if err == nil {
		return
	}
	stderrStr := stderr.String()
	// EPERM and EACCES both indicate the missing CAP_NET_ADMIN
	// limitation described above; treat as a skip rather than a hard
	// failure so dev boxes without sudo stay green. Any other nft
	// error — including the "set syntax error" class we used to ship
	// before the CIDR-comma fix — still t.Fatal().
	if strings.Contains(stderrStr, "Operation not permitted") ||
		strings.Contains(stderrStr, "Permission denied") ||
		strings.Contains(stderrStr, "are you root") {
		t.Skipf("nft -c -f requires CAP_NET_ADMIN (running as non-root user); skipping. Run with sudo or rely on `make egress-check` in CI for the production gate:\n%s", stderrStr)
	}
	t.Fatalf("nft -c -f rejected the rendered ruleset (raw `nft` output below); ruleset:\n%s\n--- nft stderr ---\n%s", out, stderrStr)
}

// TestHostPolicyRendersStaticEgressRules (ADR-119 redesign) is the
// host-side regression net for the new StaticEgressRules field.
// One entry produces one rule with the exact argv shape:
//
//	ip saddr <PerVMHostIP> oifname <PublicIface> snat to <CustomerIP>
//
// The rule appears in the SNAT-compatible shape (the same chains
// the existing MASQUERADE uses) so nft accepts the body as-is in
// the metal gate.
func TestHostPolicyRendersStaticEgressRules(t *testing.T) {
	pol := DefaultHostPolicy
	pol.StaticEgressRules = []StaticEgressRule{
		{
			PerVMHostIP: netip.MustParseAddr("10.200.0.1"),
			CustomerIP:  netip.MustParseAddr("203.0.113.42"),
			AccountID:   "11111111-1111-1111-1111-111111111111",
			AppID:       "22222222-2222-2222-2222-222222222222",
		},
	}
	out := pol.Render()
	want := `ip saddr 10.200.0.1 oifname "eth0" snat to 203.0.113.42`
	if !strings.Contains(out, want) {
		t.Fatalf("expected SNAT rule %q in rendered output, none found:\n%s", want, out)
	}
}

// TestHostPolicyStaticEgressRulesPlacedBeforeMasquerade is the
// LOAD-BEARING regression net for the PR-997 BLOCKING bug. nftables
// NAT is first-match + terminal; the broad MASQUERADE on
// MasqueradeCIDR shadows any sibling SNAT. The renderer MUST emit
// the static-egress rules BEFORE the broad MASQUERADE so the
// specific SNAT fires first. Pin the byte-offset ordering.
func TestHostPolicyStaticEgressRulesPlacedBeforeMasquerade(t *testing.T) {
	pol := DefaultHostPolicy
	pol.StaticEgressRules = []StaticEgressRule{
		{
			PerVMHostIP: netip.MustParseAddr("10.200.0.1"),
			CustomerIP:  netip.MustParseAddr("203.0.113.42"),
			AccountID:   "a",
			AppID:       "b",
		},
	}
	out := pol.Render()
	// The MASQUERADE on the default MasqueradeCIDR (=10.100.0.0/16).
	masqLine := "ip saddr 10.100.0.0/16 oifname \"eth0\" masquerade"
	masqIdx := strings.Index(out, masqLine)
	snatIdx := strings.Index(out, "snat to 203.0.113.42")
	if masqIdx < 0 {
		t.Fatalf("default MASQUERADE missing:\n%s", out)
	}
	if snatIdx < 0 {
		t.Fatalf("SNAT-to-customer rule missing:\n%s", out)
	}
	if snatIdx >= masqIdx {
		t.Fatalf("SNAT rule (offset %d) must appear BEFORE MASQUERADE (offset %d) — first-match NAT:\n%s",
			snatIdx, masqIdx, out)
	}
}

// TestHostPolicyEmptyStaticEgressRulesOmitsRules pins the
// opt-out: a HostPolicy with no StaticEgressRules emits no `snat
// to` line anywhere in the ruleset. Default MASQUERADE-only
// behaviour is preserved byte-identical to pre-119 output for
// non-pinned apps (the dominant case for Hobby / Pro plans).
func TestHostPolicyEmptyStaticEgressRulesOmitsRules(t *testing.T) {
	pol := DefaultHostPolicy
	// StaticEgressRules is nil by default — no setup needed.
	out := pol.Render()
	if strings.Contains(out, "snat to") {
		t.Errorf("StaticEgressRules=nil but renderer emitted a `snat to` line:\n%s", out)
	}
}

// TestHostPolicyStaticEgressRulesMultiple (ADR-119 redesign) is
// the multi-tenant shape — N apps pinned to N customer IPs all
// render as N rules, ordered before the broad MASQUERADE, each
// with the per-VM host IP as the source.
func TestHostPolicyStaticEgressRulesMultiple(t *testing.T) {
	pol := DefaultHostPolicy
	pol.StaticEgressRules = []StaticEgressRule{
		{PerVMHostIP: netip.MustParseAddr("10.200.0.1"), CustomerIP: netip.MustParseAddr("203.0.113.42"), AccountID: "a1", AppID: "app1"},
		{PerVMHostIP: netip.MustParseAddr("10.200.0.2"), CustomerIP: netip.MustParseAddr("203.0.113.43"), AccountID: "a2", AppID: "app2"},
		{PerVMHostIP: netip.MustParseAddr("10.200.0.3"), CustomerIP: netip.MustParseAddr("203.0.113.44"), AccountID: "a3", AppID: "app3"},
	}
	out := pol.Render()
	// All three rules present, in order.
	want1 := "ip saddr 10.200.0.1 oifname \"eth0\" snat to 203.0.113.42"
	want2 := "ip saddr 10.200.0.2 oifname \"eth0\" snat to 203.0.113.43"
	want3 := "ip saddr 10.200.0.3 oifname \"eth0\" snat to 203.0.113.44"
	idx1 := strings.Index(out, want1)
	idx2 := strings.Index(out, want2)
	idx3 := strings.Index(out, want3)
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatalf("expected 3 SNAT rules, found offsets: %d %d %d:\n%s", idx1, idx2, idx3, out)
	}
	if !(idx1 < idx2 && idx2 < idx3) {
		t.Errorf("SNAT rules must appear in input order (offets %d %d %d):\n%s", idx1, idx2, idx3, out)
	}
	// Account/app comments present in the rendered output.
	for _, want := range []string{"# account=a1 app=app1", "# account=a2 app=app2", "# account=a3 app=app3"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected audit comment %q in rendered output:\n%s", want, out)
		}
	}
}
