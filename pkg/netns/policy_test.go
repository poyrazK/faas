package netns

import (
	"fmt"
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
	denies := []string{
		"tcp dport { 25,465,587 } drop",
		"ip daddr { 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,100.64.0.0/10 } drop",
		"ip6 daddr { fe80::/10,fc00::/7,ff00::/8,::1/128,::/128 } drop",
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

// TestHostPolicyRenderNftSyntaxCheck is the local equivalent of the
// ansible role's `nft -c -f /etc/nftables.conf` step. CI gates this via
// `make egress-check` (regenerates + byte-compares the artifact), but on
// macOS devs without a Linux CI loop, having nft locally
// (`brew install nftables`) gets the same nft(8)-side syntax gate as CI.
//
// Why this matters: the regex/substring checks above assert that the
// render LOOKS right. `nft -c -f` asserts that nft(8) ACCEPTS it — a
// different class of bug (typo in a keyword, missing semicolon, wrong
// hook) only nft itself can catch. Skipping silently when nft isn't on
// PATH keeps the test non-fatal on dev hosts that don't have it.
//
// Note: `nft -c -f` parses WITHOUT touching the live kernel's
// ruleset, so this is safe to run on any host that has nft available.
// On a host with a running firewall, the ruleset's `flush ruleset`
// directive is NOT executed because `-c` skips the apply phase.
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
	if err := cmd.Run(); err != nil {
		t.Fatalf("nft -c -f rejected the rendered ruleset (raw `nft` error below); ruleset:\n%s\n--- nft stderr ---\n%s", out, stderr.String())
	}
}
