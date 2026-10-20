// Package netns_test — external cross-renderer invariant test (PR-D).
//
// This file lives in the external test package so it can import
// pkg/oci without forming a cycle (pkg/oci already imports
// pkg/netns). The internal denylist_test.go covers provenance,
// typed-slice coherence, and the per-entry shape of NewDefaultDenySet;
// this file covers the harder contract: every entry in the shared
// catalog must be DENIED by all three enforcement surfaces.
//
// Three consumers:
//
//   - Per-netns nftables renderer: Config.NftCommands() —
//     table "ip faas" / "ip6 faas", chain "forward".
//   - Host nftables renderer: DefaultHostPolicy.Render() —
//     table "inet faas", chain "forward".
//   - OCI puller dialer: oci.EgressIPAllowed — the exported
//     wrapper added in PR-D for exactly this purpose.
//
// The test enumerates NewDefaultDenySet().Entries (the canonical,
// provenance-bearing view) and asserts that each entry's CIDR is
// present in all three sinks. The OCI-only v4 extras in
// pkg/oci/egress.go are tested in pkg/oci/egress_test.go — they
// are NOT in the shared catalog and must NOT be in this loop.
//
// The test is intentionally pure: no os/exec, no nft. It is the
// non-metal proof that the spec §11 contract holds end-to-end.
package netns_test

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/oci"
)

// extractChainBody returns the body of the chain named `chain`
// inside `rendered`. Mirrors the brace-depth-walking helper used
// by the internal policy_test (memory `pkg-netns-brace-depth-extractchain`).
// Copied here because we are in package netns_test and cannot reach
// unexported test helpers. Anchor on " <name> {" preceded by any
// amount of indent / table-prefix so we work for both
// "  chain forward {" (host) and " chain ip faas forward {"
// (per-netns argv).
func extractChainBody(t *testing.T, rendered, chain string) string {
	t.Helper()
	openTag := " " + chain + " {"
	start := strings.Index(rendered, openTag)
	if start < 0 {
		t.Fatalf("chain %q not found in rendered ruleset", chain)
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
	t.Fatalf("chain %q has no depth-zero closing brace", chain)
	return ""
}

// flattenArgv joins each [][]string argv row with spaces and
// concatenates rows with newlines. Mirrors pkg/netns/config_test.go::flatten.
func flattenArgv(cmds [][]string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

// sampleAddrInPrefix returns a deterministic address inside the
// prefix for OCI predicate probing. Result is masked to guarantee
// it's inside the prefix even for boundary cases like 127.0.0.0/8.
func sampleAddrInPrefix(p netip.Prefix) netip.Addr {
	addr := p.Addr()
	bits := p.Bits()
	if bits == 0 {
		if addr.Is4() {
			return netip.MustParseAddr("1.0.0.0")
		}
		return netip.MustParseAddr("::1")
	}
	// The prefix's own network address. The earlier version set a "host"
	// bit by indexing from the front of the 16-byte form, which for any
	// prefix longer than /64 set a network bit and sampled outside the
	// prefix (64:ff9b::/96 gave 64:ff9b:8000::).
	return p.Masked().Addr()
}

// rowHasCIDRDeny scans the flattened argv for a rule row that denies
// <cidr>. PR-E changed the per-CIDR shape from `<daddrKW> { ... <cidr> ... } drop`
// (one rule per family, aggregate set) to `<daddrKW> <cidr> counter name "<name>" drop`
// (one rule per CIDR with a named counter attached). The helper matches
// the post-PR-E shape: a row that contains the daddrKW token, the cidr
// token, AND ends in `counter name "<name>" drop` (where name is matched
// separately via rowHasCIDRWithCounter). rowHasCIDRDeny is the
// no-counter-name variant — the counter-name pin lives in
// TestAllThreeConsumersAgreeOnDenySet below via rowHasCIDRWithCounter.
func rowHasCIDRDeny(argv, daddrKW, cidr string) bool {
	for _, row := range strings.Split(argv, "\n") {
		if !strings.Contains(row, daddrKW) {
			continue
		}
		if !strings.Contains(row, cidr) {
			continue
		}
		// Per-CIDR rules end with `counter name "<x>" drop` — the
		// presence of "drop" alone is not enough (the established/
		// related accept rule row might contain the CIDR as part
		// of an exception clause). Require the counter-name suffix
		// shape so the regression net only fires on deny rules.
		if !strings.Contains(row, "counter name") {
			continue
		}
		if !strings.HasSuffix(strings.TrimSpace(row), "drop") {
			continue
		}
		return true
	}
	return false
}

// rowHasCIDRWithCounter is the PR-E counterpart to rowHasCIDRDeny:
// scans argv rows for a per-CIDR deny rule that ALSO attaches the
// given CounterName. The argv is space-joined (no quoting preserved
// — config.go emits the name as a bare token to `nft` via the OS
// exec path, not via shell), so the on-the-wire shape is
// `... <daddrKW> <cidr> counter name <counterName> drop`.
//
// Without this assertion a regression could keep the per-CIDR deny
// shape but forget the counter attachment — the deny would still
// fire, the operator would still see the rule in `nft list ruleset`,
// but the per-CIDR Prometheus series would be empty.
//
// The counter name is matched as a whole-word token so a CIDR like
// `drop_v4_10_0_0_0_8` doesn't accidentally match `drop_v4_10_0_0_0_80`.
// We use the simpler approach: require the literal `counter name <name>`
// substring, then check the row ends with `drop`. This keeps the helper
// allocation-free and the assertion unambiguous.
func rowHasCIDRWithCounter(argv, daddrKW, cidr, counterName string) bool {
	needle := "counter name " + counterName
	for _, row := range strings.Split(argv, "\n") {
		if !strings.Contains(row, daddrKW) {
			continue
		}
		if !strings.Contains(row, cidr) {
			continue
		}
		if !strings.Contains(row, needle) {
			continue
		}
		if !strings.HasSuffix(strings.TrimSpace(row), "drop") {
			continue
		}
		return true
	}
	return false
}

// rowHasSMTPPort scans the flattened argv for the SMTP-port rule row
// `tcp dport { 25,465,587 } drop` (or a sibling row containing the
// specific port token).
func rowHasSMTPPort(argv, port string) bool {
	for _, row := range strings.Split(argv, "\n") {
		if !strings.Contains(row, "tcp dport") {
			continue
		}
		if !strings.Contains(row, port) {
			continue
		}
		if !strings.Contains(row, "} drop") {
			continue
		}
		return true
	}
	return false
}

// TestAllThreeConsumersAgreeOnDenySet (PR-D) is the non-metal
// cross-renderer invariant test. Every entry in
// NewDefaultDenySet() must be denied by:
//
//	(a) per-netns Config.NftCommands() — the `ip daddr { … } drop`
//	    or `ip6 daddr { … } drop` rule row in the per-netns forward chain.
//	(b) host DefaultHostPolicy.Render() — same line in the host
//	    forward chain body.
//	(c) oci.EgressIPAllowed(sample) == false — the user-space dial
//	    check used by the OCI puller.
//
// A regression that drops one entry from any sink — or removes it
// from NewDefaultDenySet() — surfaces here as a t.Errorf with the
// specific entry, family, and sink that disagreed.
func TestAllThreeConsumersAgreeOnDenySet(t *testing.T) {
	ds := netns.NewDefaultDenySet()
	if len(ds.Entries) == 0 {
		t.Fatal("NewDefaultDenySet() returned empty Entries; cannot enforce the invariant")
	}

	cfg := netns.NewConfig("instance", "faas-ns", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.1"))
	cfg.Tap = "tap0"
	cfg.EgressAllowlist = nil
	cfg.ConntrackCap = 4096
	perNetnsArgv := flattenArgv(cfg.NftCommands())

	hostRender := netns.DefaultHostPolicy.Render()
	hostFwd := extractChainBody(t, hostRender, "forward")

	for i, e := range ds.Entries {
		cidr := e.Prefix.String()
		// e.Family.String() returns the nft family keyword (`ip` / `ip6`).
		// The cross-renderer test pins the keyword at the Family-tag
		// interface, so any divergence between the enum and the nft
		// keyword surfaces as a literal-string mismatch here.
		daddrKW := e.Family.String() + " daddr"

		// Per-netns sink.
		if !rowHasCIDRDeny(perNetnsArgv, daddrKW, cidr) {
			t.Errorf("entries[%d] (%s) missing from per-netns %q deny argv row", i, cidr, daddrKW)
		}
		// PR-E: also assert the counter attachment — the per-CIDR rule
		// shape is `<daddrKW> <cidr> counter name "<CounterName>" drop`.
		// Without this assertion a regression could keep the deny rule
		// but lose the counter name; the rule would still fire and the
		// deny observability panel would silently go dark.
		if !rowHasCIDRWithCounter(perNetnsArgv, daddrKW, cidr, e.CounterName) {
			t.Errorf("entries[%d] (%s) per-netns %q deny argv missing counter name %q",
				i, cidr, daddrKW, e.CounterName)
		}

		// Host sink. The host forward chain is a single `table inet faas`
		// body — the deny lines are emitted one per CIDR (PR-E), each
		// as `<daddrKW> <cidr> counter name "<CounterName>" drop`. The
		// pre-PR-E shape used the aggregate `<daddrKW> { … } drop`; the
		// daddrNeedle helper below expects the per-CIDR shape.
		hostDenyNeedle := daddrKW + " " + cidr
		idx := strings.Index(hostFwd, hostDenyNeedle)
		if idx < 0 {
			t.Errorf("entries[%d] (%s, %s) host forward chain missing %q deny line",
				i, cidr, daddrKW, hostDenyNeedle)
			continue
		}
		// Walk forward from the deny-line start to the end-of-line /
		// counter-name suffix; check the counter attachment is present.
		end := idx + len(hostDenyNeedle)
		// Find the next newline so we don't match across CIDRs.
		if nl := strings.Index(hostFwd[end:], "\n"); nl >= 0 {
			end += nl
		}
		line := hostFwd[idx:end]
		if !strings.Contains(line, `counter name "`+e.CounterName+`"`) {
			t.Errorf("entries[%d] (%s) host %q deny line missing counter name %q",
				i, cidr, daddrKW, e.CounterName)
		}
		if !strings.HasSuffix(strings.TrimSpace(line), "drop") {
			t.Errorf("entries[%d] (%s) host %q deny line does not end in `drop`",
				i, cidr, daddrKW)
		}

		// Counter pre-declaration at table scope (host side). The
		// policy.go renderer emits `  counter <name> {}` at the top
		// of the `table inet faas` body — a regression that drops
		// the pre-declaration would land an `nft: counter name "..."
		// does not exist` error at `nft -f` time.
		if !strings.Contains(hostRender, "  counter "+e.CounterName+" {}") {
			t.Errorf("entries[%d] (%s) host render missing counter pre-declaration %q",
				i, cidr, e.CounterName)
		}

		// OCI sink.
		sample := sampleAddrInPrefix(e.Prefix)
		if oci.EgressIPAllowed(sample) {
			t.Errorf("entries[%d] (%s) sample %s ALLOWED by oci.EgressIPAllowed, want denied",
				i, cidr, sample)
		}
	}
}

// TestAllThreeConsumersAgreeOnSMTPPorts (PR-D) — the SMTP deny
// set is a separate typed slice on DenySet (not Entries), so the
// CIDR loop above doesn't cover it. Spec §11 requires 25/465/587
// to be denied by the renderer sinks; OCI has no SMTP-port concept
// so the assertion is renderer-only.
func TestAllThreeConsumersAgreeOnSMTPPorts(t *testing.T) {
	ds := netns.NewDefaultDenySet()
	wantPorts := map[uint16]bool{25: false, 465: false, 587: false}
	for _, p := range ds.SMTPPorts {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for port, present := range wantPorts {
		if !present {
			t.Errorf("NewDefaultDenySet().SMTPPorts missing %d (spec §11)", port)
		}
	}

	cfg := netns.NewConfig("instance", "faas-ns", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.1"))
	cfg.Tap = "tap0"
	cfg.ConntrackCap = 4096
	perNetnsArgv := flattenArgv(cfg.NftCommands())
	hostFwd := extractChainBody(t, netns.DefaultHostPolicy.Render(), "forward")

	for _, p := range ds.SMTPPorts {
		needle := strconv.Itoa(int(p))
		if !rowHasSMTPPort(perNetnsArgv, needle) {
			t.Errorf("SMTP port %s missing from per-netns argv row", needle)
		}
		if !strings.Contains(hostFwd, needle) {
			t.Errorf("SMTP port %s missing from host forward chain", needle)
		}
	}
}
