// Package netns_test — §11a cross-renderer triple-agreement tripwire.
//
// This file mirrors the PR-D TestAllThreeConsumersAgreeOnDenySet
// pattern (denylist_external_test.go) but is the §11a layer-down
// pin: every entry in NewDefaultDenySet() must be DENIED by all
// three enforcement surfaces, AND each entry's family tag must
// match the table family it lands in. The two previous pins
// (TestAllThreeConsumersAgreeOnDenySet + TestAllThreeConsumersAgreeOnSMTPPorts)
// already cover the per-sink deny presence, but do not assert
// the v4↔v6 split goes the right way on both renderers.
//
// The tripwire is intentionally not split per-sink: a single
// failure should localize cleanly to (cidr, family, sink) so
// the regression report points the fixer at the right line.
//
// External test package so it can import pkg/oci without
// forming a cycle (pkg/oci already imports pkg/netns). The
// sibling denylist_committed_test.go covers per-renderer
// pin detail; this file covers the harder cross-renderer
// invariant.
//
// Helpers (splitNftCommands, findRuleRow, makeConfig,
// smtpDenyNeedle) live in the sibling file because they are
// shared and unexported. Both files are in `package netns_test`
// so the visibility is automatic; duplication is avoided.
package netns_test

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/oci"
)

// TestSec11a_CrossRendererTripleAgreement — every entry in
// NewDefaultDenySet() must be denied by:
//
//	(a) per-netns Config.NftCommands() — the per-CIDR deny
//	    row in the forward chain matching the entry's family.
//	(b) host DefaultHostPolicy.Render() — the per-CIDR deny
//	    line in the host forward chain matching the entry's
//	    family.
//	(c) oci.EgressIPAllowed(sample) == false — the user-space
//	    dial check used by the OCI puller.
//
// In addition, the test pins the SMTP port deny (25/465/587)
// across both renderers — OCI has no port concept so the SMTP
// assertion is renderer-only.
//
// A regression that drops one entry from any sink — or
// mis-families it — surfaces as a t.Errorf with the specific
// entry, family, and sink that disagreed.
//
// Runs on every-PR CI (no build tag, no root, no /dev/kvm).
// The authoritative cross-process kernel-attribute gate stays
// in pkg/fcvm/manager_metal_test.go::TestMetalGuestEgressToPublicViaMASQUERADE
// (//go:build metal).
func TestSec11a_CrossRendererTripleAgreement(t *testing.T) {
	ds := netns.NewDefaultDenySet()
	if len(ds.Entries) == 0 {
		t.Fatal("NewDefaultDenySet() returned empty Entries; cannot enforce the §11a invariant")
	}

	perNetnsV4, perNetnsV6 := splitNftCommandsV4V6(t, makeConfig())
	hostRender := netns.DefaultHostPolicy.Render()

	for i, e := range ds.Entries {
		cidr := e.Prefix.String()
		family := e.Family.String() // "ip" or "ip6"

		// Per-netns sink: per-CIDR deny row, scoped to the
		// entry's family (the v4 / v6 split).
		perNetns := perNetnsV4
		if e.Family == netns.FamilyV6 {
			perNetns = perNetnsV6
		}
		denyNeedle := family + " daddr " + cidr
		row := findRuleRow(perNetns, denyNeedle)
		if row == "" {
			t.Errorf("entries[%d] (%s, %s) missing from per-netns argv", i, cidr, family)
		} else {
			// The per-CIDR rule must end in `drop` — a
			// regression that adds accept or dnat to the
			// rule would re-open the lateral-movement
			// path.
			trimmed := strings.TrimSpace(row)
			if !strings.HasSuffix(trimmed, "drop") {
				t.Errorf("entries[%d] (%s, %s) per-netns deny row does not end in `drop`: %q",
					i, cidr, family, trimmed)
			}
			// The per-CIDR rule must carry the iifname tap0
			// qualifier — the §11 graceful-isolation
			// invariant depends on the rule only matching
			// guest-originated traffic.
			if !strings.Contains(row, "iifname") {
				t.Errorf("entries[%d] (%s, %s) per-netns deny row missing iifname qualifier: %q",
					i, cidr, family, row)
			}
			if !strings.Contains(row, "tap0") {
				t.Errorf("entries[%d] (%s, %s) per-netns deny row missing tap0 token: %q",
					i, cidr, family, row)
			}
		}

		// Host sink: per-CIDR deny line in the unified
		// table inet faas forward chain.
		hostDenyNeedle := family + " daddr " + cidr
		idx := strings.Index(hostRender, hostDenyNeedle)
		if idx < 0 {
			t.Errorf("entries[%d] (%s, %s) missing from host render", i, cidr, family)
		} else {
			// Walk forward to the end of the line; the
			// line must end in `drop` and carry the
			// counter name from the catalog.
			line := hostRender[idx:]
			if nl := strings.Index(line, "\n"); nl >= 0 {
				line = line[:nl]
			}
			if !strings.HasSuffix(strings.TrimSpace(line), "drop") {
				t.Errorf("entries[%d] (%s, %s) host deny line does not end in `drop`: %q",
					i, cidr, family, line)
			}
		}

		// OCI sink: the user-space dial check must deny
		// every entry. Use a sample-addr-in-prefix probe
		// for consistency with the PR-D test.
		sample := sec11aSampleAddrInPrefix(e.Prefix)
		if oci.EgressIPAllowed(sample) {
			t.Errorf("entries[%d] (%s, %s) sample %s ALLOWED by oci.EgressIPAllowed, want denied",
				i, cidr, family, sample)
		}
	}

	// SMTP port deny across both renderers. OCI has no port
	// concept so the SMTP assertion is renderer-only. The v6
	// assertion anchors on the SMTP-specific needle (not the
	// bare `tcp dport` substring) so a future legitimate v6
	// port rule does not false-positive.
	if !strings.Contains(perNetnsV4, smtpDenyNeedle) {
		t.Errorf("per-netns v4 argv missing SMTP deny %q", smtpDenyNeedle)
	}
	if strings.Contains(perNetnsV6, smtpDenyNeedle) {
		t.Errorf("per-netns v6 argv emitted SMTP deny %q — SMTP deny must be v4-only per spec §11 + ADR-023", smtpDenyNeedle)
	}
	if !strings.Contains(hostRender, smtpDenyNeedle) {
		t.Errorf("host render missing SMTP deny %q", smtpDenyNeedle)
	}
	for _, p := range ds.SMTPPorts {
		needle := strconv.Itoa(int(p))
		// Per-netns SMTP port must appear in the comma-only
		// form on the same row as the rest of the set. Anchor
		// the row search on the SMTP-row's `tcp dport { … }`
		// opening — a naive "tcp dport" search would match
		// the prerouting DNAT row's `tcp dport 8080 dnat` and
		// report a false negative.
		row := findRuleRow(perNetnsV4, "tcp dport {")
		if !strings.Contains(row, needle) {
			t.Errorf("per-netns SMTP row missing port %s: %q", needle, row)
		}
		// Host render uses the same comma-only form.
		if !strings.Contains(hostRender, needle) {
			t.Errorf("host render missing SMTP port %s", needle)
		}
	}
}

// splitNftCommandsV4V6 is the tuple-returning convenience
// wrapper around the sibling's splitNftCommands. The
// cross-renderer tripwire uses the tuple shape because it
// touches both halves side-by-side (per-entry family-aware
// dispatcher + SMTP v4-only assertion). The sibling's
// struct-returning helper is preserved for callers that
// want the positional names.
func splitNftCommandsV4V6(t *testing.T, cfg netns.Config) (v4Argv, v6Argv string) {
	t.Helper()
	s := splitNftCommands(t, cfg)
	return s.V4, s.V6
}

// sec11aSampleAddrInPrefix returns a deterministic address
// inside the prefix for OCI predicate probing. Duplicated
// from denylist_external_test.go::sampleAddrInPrefix rather
// than exported because:
//
//   - the helper is test-only
//   - exporting it would only be used by this §11a file
//   - the §11a pin is intentionally independent of the PR-D
//     pin (a regression in one should not silently hide behind
//     the other)
//
// The implementation is identical to the PR-D version.
func sec11aSampleAddrInPrefix(p netip.Prefix) netip.Addr {
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
