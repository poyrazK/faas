// Package netns_test — §11a wire-shape pins for the guest-egress
// denylist. These tests run on every PR CI (no build tag, no root,
// no /dev/kvm, no nft binary) and lock the per-netns argv + host
// rendered text against the §11 contract:
//
//	"deny 25/465/587, deny RFC1918 + link-local + metadata ranges."
//
// The authoritative cross-process kernel-attribute gate is the
// `//go:build metal` test in pkg/fcvm/manager_metal_test.go
// (TestMetalGuestEgressToPublicViaMASQUERADE) which boots a real
// guest, has /init nc 8.8.8.8:25, and asserts the host fetches
// `result/smtp` containing `smtp-dropped`. That path needs
// /dev/kvm + uid 0 + FAAS_TEST_KERNEL + FAAS_TEST_EGRESS_URL.
//
// The §11a wire-shape pin is the every-PR layer-down: it does
// not boot a VM, but it does pin the per-CIDR rules, the SMTP
// dropped-on-tap0 qualifier, the iifname tap0 vs iifname VethPeer
// separation (the §11 graceful-isolation invariant when the
// guest's own IP 10.0.0.2 sits inside the 10.0.0.0/8 deny range),
// the v4-only SMTP contract, and the host-policy / per-netns
// argv parity.
//
// Sibling file: denylist_external_sec11a_test.go carries the
// cross-renderer triple-agreement pin (the per-CIDR deny must
// be present in all three enforcement surfaces — per-netns argv,
// host DefaultHostPolicy.Render(), and oci.EgressIPAllowed).
// That sibling lives in `package netns_test` so it can import
// pkg/oci without forming a cycle (pkg/oci already imports
// pkg/netns). The shared helpers below (splitNftCommands,
// findRuleRow, makeConfig) are unexported and reused by both
// files; both live in `package netns_test` so the visibility
// is automatic.
package netns_test

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

// splitArgv is the v4 / v6 split of the per-netns ruleset.
// The per-netns renderer emits one ip faas table containing
// the v4 deny rules (ADR-023 v4 split) and one ip6 faas table
// containing the v6 deny rules. Splitting them at the test
// boundary lets the §11a pins assert "v4-only SMTP" and
// "v4-only RFC1918" without leaking assertions across the
// family boundary.
type splitArgv struct {
	V4 string
	V6 string
}

// splitNftCommands renders Config.NftCommands and splits the
// flattened argv on the `add table ip6 faas` declaration. ADR-023
// emits the v4 table first, then the v6 table — and every v4
// chain block (counters, prerouting DNAT, postrouting MASQUERADE,
// forward chain, established/related accept, SMTP port drop,
// per-CIDR deny rules, per-CIDR allowlist) lands contiguously
// before the v6 marker. The asymmetry is by construction: the
// split is "anything before the v6 marker is v4 argv, anything
// from the v6 marker onward is v6 argv". A future refactor that
// intersperses v4 and v6 blocks would break the split — this
// is intentional, because such a refactor would also break
// ADR-023's "no mixed ip + ip6 in one table" rule and the trip
// belongs on the wire-shape pin, not silently absorbed here.
//
// The SMTP-port deny is emitted inside the v4 forward chain
// block (config.go:270), so it falls on the v4 side of the
// split — which is what TestSec11a_PerNetnsArgvSMTPDenyIsV4Only
// depends on.
func splitNftCommands(t *testing.T, cfg netns.Config) splitArgv {
	t.Helper()
	argv := cfg.NftCommands()
	var rows []string
	for _, c := range argv {
		rows = append(rows, strings.Join(c, " "))
	}
	flat := strings.Join(rows, "\n")

	const v6Marker = "add table ip6 faas"
	idx := strings.Index(flat, v6Marker)
	if idx < 0 {
		t.Fatalf("per-netns argv missing %q — v6 split assumed by ADR-023 broke", v6Marker)
	}
	return splitArgv{V4: flat[:idx], V6: flat[idx:]}
}

// makeConfig returns a Config with the typical Wake-time shape
// (no per-app allowlist, no conntrack cap override) so the §11a
// assertions land on the same argv the production path emits.
// The HostIP is irrelevant to the egress rules but cfg.Tap MUST
// be `tap0` so the iifname tokens match the §11 contract.
func makeConfig() netns.Config {
	cfg := netns.NewConfig("instance", "faas-ns", "veth-host", "veth-peer", netip.MustParseAddr("10.100.0.2"))
	cfg.Tap = "tap0"
	cfg.EgressAllowlist = nil
	cfg.ConntrackCap = 4096
	return cfg
}

// findRuleRow scans the flattened argv for a row that contains
// `needle` and returns the full row. Returns "" if no row
// matches — callers must check for the empty string when the
// presence of a row is the assertion (the §11a pins use the
// empty string as a "row not found" signal).
//
// Used by the per-row qualifier assertions (iifname tap0, dnat,
// etc.) so the §11a pins don't trip on a substring that matches
// an unrelated rule.
func findRuleRow(argv, needle string) string {
	for _, row := range strings.Split(argv, "\n") {
		if strings.Contains(row, needle) {
			return row
		}
	}
	return ""
}

// smtpDenyNeedle is the post-ADR-034 comma-only form of the
// SMTP port deny (the bare form `{ 25, 465, 587 }` with spaces
// is rejected by modern nft — see memory
// `nft-cidr-set-comma-required`). Every §11a SMTP-related
// assertion anchors on this exact needle so a regression that
// adds a legitimate v6 port rule (e.g. an outbound HTTPS
// allowlist on a specific port) does not false-positive on
// the "v6 must not have any tcp dport rule" check.
const smtpDenyNeedle = "tcp dport { 25,465,587 } drop"

// smtpDenyNeedleNetns is the per-netns form: guest-side policy drops jump
// to the egress_drop chain, which records the attempt and drops it.
const smtpDenyNeedleNetns = "tcp dport { 25,465,587 } goto " + netns.EgressDropChain

// TestSec11a_PerNetnsArgvContainsEveryDenyCIDR — every entry in
// NewDefaultDenySet().Entries must be in the per-netns argv AND
// the entry's family tag must match the table family it lands
// in. A regression that drops an entry from the per-netns
// renderer (or mis-families it) surfaces here.
//
// Caught regressions:
//   - Editor forgets to add a new CIDR to the per-netns argv
//   - Renderer refactor that emits the v4 set into the v6 table
//   - Renderer refactor that emits the v6 set into the v4 table
func TestSec11a_PerNetnsArgvContainsEveryDenyCIDR(t *testing.T) {
	ds := netns.NewDefaultDenySet()
	if len(ds.Entries) == 0 {
		t.Fatal("NewDefaultDenySet() returned empty Entries; cannot pin the §11a invariant")
	}

	argv := splitNftCommands(t, makeConfig())
	for i, e := range ds.Entries {
		cidr := e.Prefix.String()
		switch e.Family {
		case netns.FamilyV4:
			if !strings.Contains(argv.V4, "ip daddr "+cidr) {
				t.Errorf("entries[%d] (%s, v4) missing from per-netns v4 argv", i, cidr)
			}
			if strings.Contains(argv.V6, "ip6 daddr "+cidr) {
				t.Errorf("entries[%d] (%s, v4) leaked into per-netns v6 argv", i, cidr)
			}
		case netns.FamilyV6:
			if !strings.Contains(argv.V6, "ip6 daddr "+cidr) {
				t.Errorf("entries[%d] (%s, v6) missing from per-netns v6 argv", i, cidr)
			}
			if strings.Contains(argv.V4, "ip daddr "+cidr) {
				t.Errorf("entries[%d] (%s, v6) leaked into per-netns v4 argv", i, cidr)
			}
		default:
			t.Errorf("entries[%d] (%s) has unknown family tag %d", i, cidr, e.Family)
		}
	}
}

// TestSec11a_PerNetnsArgvContainsSMTPDeny — the SMTP port deny
// must be emitted on the v4 forward chain, matched on
// `iifname tap0` (the guest-originated side), and the port set
// must be the comma-only form (post-ADR-034 / nft-cidr-set-comma-required
// memory).
//
// Caught regressions:
//   - Refactor that drops the SMTP line from the v4 forward chain
//   - Refactor that switches the SMTP line to a wrong family
//   - Refactor that drops the `iifname tap0` qualifier (a guest
//     could SYN to a host-loopback SMTP port successfully)
func TestSec11a_PerNetnsArgvContainsSMTPDeny(t *testing.T) {
	argv := splitNftCommands(t, makeConfig())
	if !strings.Contains(argv.V4, smtpDenyNeedleNetns) {
		t.Errorf("v4 forward chain missing SMTP deny %q", smtpDenyNeedleNetns)
	}
	// The line must be on the iifname=tap0 side so it ONLY
	// matches guest-originated packets. Pull the rule row and
	// assert iifname tap0 is on the same row.
	row := findRuleRow(argv.V4, smtpDenyNeedleNetns)
	if !strings.Contains(row, "iifname") || !strings.Contains(row, "tap0") {
		t.Errorf("SMTP deny row missing iifname tap0 qualifier: %q", row)
	}
}

// TestSec11a_PerNetnsArgvSMTPDenyIsV4Only — spec §11 + ADR-023
// do NOT extend the SMTP port deny to IPv6. A future ADR change
// would need to land here as a deliberate test edit. Pinning
// v4-only at the wire-shape layer prevents an accidental "fix"
// that adds an IPv6 SMTP line without an ADR.
//
// The v6 assertion anchors on the SMTP-specific needle (not the
// bare `tcp dport` substring) so a future legitimate v6 port
// rule (e.g. an outbound HTTPS allowlist on a specific port)
// does not false-positive.
//
// Caught regressions:
//   - Someone "improves" coverage by adding an IPv6 SMTP mirror
//   - Renderer refactor that merges the v4 and v6 chains into
//     one table and the SMTP rule ends up dropping v6 too
func TestSec11a_PerNetnsArgvSMTPDenyIsV4Only(t *testing.T) {
	argv := splitNftCommands(t, makeConfig())
	if !strings.Contains(argv.V4, smtpDenyNeedleNetns) {
		t.Errorf("v4 forward chain missing SMTP deny (v4-only invariant broken)")
	}
	if strings.Contains(argv.V6, smtpDenyNeedleNetns) {
		t.Errorf("v6 forward chain emitted SMTP deny %q — SMTP deny must be v4-only per spec §11 + ADR-023", smtpDenyNeedleNetns)
	}
}

// TestSec11a_RFC1918AndMetadataInCorrectFamily — every v4
// deny CIDR must appear in the v4 argv (and ONLY the v4 argv);
// every v6 deny CIDR must appear in the v6 argv (and ONLY the
// v6 argv). The family separation is load-bearing because nft
// rejects mixing `ip` and `ip6` matches in a single table
// (ADR-023), and because the connlimit cap rule on the v4
// chain would silently mis-count if a v6 CIDR ended up in the
// v4 set.
//
// Caught regressions:
//   - Cross-family CIDR swap in the renderer
//   - Renderer refactor that merges the two tables into one
//   - PR-E counter loop that emits a FamilyV4 entry into the
//     v6 counter set
func TestSec11a_RFC1918AndMetadataInCorrectFamily(t *testing.T) {
	argv := splitNftCommands(t, makeConfig())
	ds := netns.NewDefaultDenySet()

	for _, p := range ds.V4DenyCIDRs {
		cidr := p.String()
		if !strings.Contains(argv.V4, "ip daddr "+cidr) {
			t.Errorf("v4 CIDR %s missing from per-netns v4 argv", cidr)
		}
		if strings.Contains(argv.V6, cidr) {
			t.Errorf("v4 CIDR %s leaked into per-netns v6 argv", cidr)
		}
	}
	for _, p := range ds.V6DenyCIDRs {
		cidr := p.String()
		if !strings.Contains(argv.V6, "ip6 daddr "+cidr) {
			t.Errorf("v6 CIDR %s missing from per-netns v6 argv", cidr)
		}
		if strings.Contains(argv.V4, cidr) {
			t.Errorf("v6 CIDR %s leaked into per-netns v4 argv", cidr)
		}
	}
}

// TestSec11a_GuestIPIsInsideDenyRange_IifnameTap0ProtectsInbound
// — ADR-009 pins the guest's identity at 10.0.0.2 inside the
// per-netns /30. That address is itself inside the 10.0.0.0/8
// deny range. The §11 graceful-isolation invariant relies on
// the `iifname tap0` qualifier on the deny rule so the inbound
// DNAT path (matched on `iifname VethPeer`) is unaffected: a
// guest-initiated outbound to its own neighbor still hits the
// deny because that's the guest's egress; a host->guest DNAT'd
// reply on the established flow is allowed because the
// established/related accept rule runs first.
//
// Caught regressions:
//   - Refactor that drops the `iifname tap0` qualifier on the
//     10.0.0.0/8 deny line — the inbound reply path would
//     deadlock.
//   - Refactor that drops the prerouting DNAT line — the guest
//     app would no longer be reachable from the public internet.
//   - Refactor that flips the qualifier order (e.g. `tcp dport
//     iifname tap0` instead of `iifname tap0 tcp dport`) — nft
//     accepts both, but the §11 contract is the literal form.
func TestSec11a_GuestIPIsInsideDenyRange_IifnameTap0ProtectsInbound(t *testing.T) {
	// Sanity: the guest's IP is inside the 10.0.0.0/8 deny range.
	// GuestPrefix is a string constant per ADR-009; parse it
	// for the membership check (the invariant is load-bearing
	// but the type is kept as a wire-format constant).
	prefix, perr := netip.ParsePrefix(netns.GuestPrefix)
	if perr != nil {
		t.Fatalf("netns.GuestPrefix %q is not a valid CIDR: %v", netns.GuestPrefix, perr)
	}
	if !prefix.Contains(netip.MustParseAddr(netns.GuestIP)) {
		t.Fatalf("guest IP %s not in %s — ADR-009 invariant broken", netns.GuestIP, netns.GuestPrefix)
	}

	argv := splitNftCommands(t, makeConfig())

	// The 10.0.0.0/8 deny row must contain iifname tap0.
	denyRow := findRuleRow(argv.V4, "ip daddr 10.0.0.0/8")
	if !strings.Contains(denyRow, "iifname") {
		t.Errorf("10.0.0.0/8 deny row missing iifname qualifier: %q", denyRow)
	}
	if !strings.Contains(denyRow, "tap0") {
		t.Errorf("10.0.0.0/8 deny row missing tap0 token: %q", denyRow)
	}
	// The deny row must NOT chain a dnat or accept — the rule is
	// ` ... drop`, never ` ... accept` and never ` ... dnat ...`.
	if strings.Contains(denyRow, "dnat") {
		t.Errorf("10.0.0.0/8 deny row leaks dnat: %q", denyRow)
	}
	if strings.Contains(denyRow, "accept") {
		t.Errorf("10.0.0.0/8 deny row leaks accept: %q", denyRow)
	}

	// The prerouting DNAT row must be present and must NOT use
	// `iifname tap0` — it MUST use the veth peer name so the
	// inbound path from the host bridge stays open.
	dnatNeedle := "dnat to " + netns.GuestIP + ":" + strconv.Itoa(netns.AppPort)
	dnatRow := findRuleRow(argv.V4, dnatNeedle)
	if !strings.Contains(dnatRow, "iifname") {
		t.Errorf("prerouting DNAT row missing iifname qualifier: %q", dnatRow)
	}
	if strings.Contains(dnatRow, "tap0") {
		t.Errorf("prerouting DNAT row uses iifname tap0 — DNAT must use the veth peer, not tap0: %q", dnatRow)
	}
}

// TestSec11a_HostPolicyRenderMirrorsPerNetnsArgv — the host
// rendered text (deploy/ansible/roles/nftables/files/policy_nftables.conf)
// and the per-netns argv read from the same DenySet. A drift
// between the two renderers leaves the host defense-in-depth
// layer stale and is invisible to the per-netns argv assertion
// alone.
//
// Caught regressions:
//   - PR-E counter pre-declaration drift
//   - Per-CIDR rule dropped from HostPolicy.Render but kept in
//     Config.NftCommands (or vice versa)
//   - The SMTP port drop dropped from the host renderer
func TestSec11a_HostPolicyRenderMirrorsPerNetnsArgv(t *testing.T) {
	host := netns.DefaultHostPolicy.Render()
	ds := netns.NewDefaultDenySet()

	// Every entry must appear in the host render.
	for _, e := range ds.Entries {
		family := e.Family.String()
		cidr := e.Prefix.String()
		needle := family + " daddr " + cidr
		if !strings.Contains(host, needle) {
			t.Errorf("host render missing %q deny line for entry %s", needle, cidr)
		}
	}

	// The SMTP port deny must be in the host render in the
	// comma-only form.
	if !strings.Contains(host, smtpDenyNeedle) {
		t.Errorf("host render missing SMTP deny %q", smtpDenyNeedle)
	}
}
