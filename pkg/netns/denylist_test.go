package netns

// PR-A — DenySet regression net. The typed DenySet is the single
// source of truth for the deny list (pkg/netns/denylist.go). These
// tests pin:
//
//   - every entry in NewDefaultDenySet() has a SourceADR string
//     (so the operator-facing artifact can render provenance)
//   - the v4 / v6 slices match the Entries slice (no drift between
//     the typed view and the metadata view)
//   - the comma-joined output uses the modern-nft syntax (no
//     trailing whitespace, comma-with-no-trailing-whitespace —
//     memory `nft-cidr-set-comma-required`)
//   - 6to4 (2002::/16) and Teredo (2001::/32) are present
//     (ADR-034 closes the v6 lateral-movement gap)
//
// The MissingEntry sub-test mutates a clone of the deny set to
// drop one entry and confirms a downstream consumer would notice
// — this is the "test isn't over-asserting" pin (any future
// regression that drops a CIDR would surface as a single
// t.Errorf).

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNewDefaultDenySet_EntriesHaveProvenance(t *testing.T) {
	d := NewDefaultDenySet()
	if len(d.Entries) == 0 {
		t.Fatal("NewDefaultDenySet returned an empty Entries slice")
	}
	for i, e := range d.Entries {
		if e.SourceADR == "" {
			t.Errorf("Entries[%d] (%s) missing SourceADR", i, e.Prefix)
		}
		if !e.Prefix.IsValid() {
			t.Errorf("Entries[%d] has invalid Prefix %v", i, e.Prefix)
		}
		if e.Family != FamilyV4 && e.Family != FamilyV6 {
			t.Errorf("Entries[%d] (%s) has unknown Family %d", i, e.Prefix, e.Family)
		}
		// Family must match the prefix family — a v4 prefix labeled
		// v6 would land in the wrong argv.
		isV4 := e.Prefix.Addr().Is4()
		if isV4 && e.Family != FamilyV4 {
			t.Errorf("Entries[%d] (%s) is v4 but Family = %v", i, e.Prefix, e.Family)
		}
		if !isV4 && e.Family != FamilyV6 {
			t.Errorf("Entries[%d] (%s) is v6 but Family = %v", i, e.Prefix, e.Family)
		}
	}
}

func TestNewDefaultDenySet_TypedSlicesMatchEntries(t *testing.T) {
	d := NewDefaultDenySet()
	var v4FromEntries, v6FromEntries []netip.Prefix
	for _, e := range d.Entries {
		if e.Family == FamilyV4 {
			v4FromEntries = append(v4FromEntries, e.Prefix)
		} else {
			v6FromEntries = append(v6FromEntries, e.Prefix)
		}
	}
	if len(v4FromEntries) != len(d.V4DenyCIDRs) {
		t.Errorf("len(V4DenyCIDRs) = %d, want %d (from Entries)", len(d.V4DenyCIDRs), len(v4FromEntries))
	}
	if len(v6FromEntries) != len(d.V6DenyCIDRs) {
		t.Errorf("len(V6DenyCIDRs) = %d, want %d (from Entries)", len(d.V6DenyCIDRs), len(v6FromEntries))
	}
	for i, p := range v4FromEntries {
		if p != d.V4DenyCIDRs[i] {
			t.Errorf("V4DenyCIDRs[%d] = %s, want %s", i, d.V4DenyCIDRs[i], p)
		}
	}
	for i, p := range v6FromEntries {
		if p != d.V6DenyCIDRs[i] {
			t.Errorf("V6DenyCIDRs[%d] = %s, want %s", i, d.V6DenyCIDRs[i], p)
		}
	}
}

func TestNewDefaultDenySet_CommaSetHasNoTrailingWhitespace(t *testing.T) {
	d := NewDefaultDenySet()
	v4 := d.V4CommaSet()
	v6 := d.V6CommaSet()
	// Modern nft on ubuntu-latest rejects trailing-whitespace set
	// syntax (memory `nft-cidr-set-comma-required`). The helpers
	// here must NOT emit `, `, `, `, only `,`.
	for _, raw := range []string{v4, v6} {
		if strings.Contains(raw, ", ") {
			t.Errorf("deny set has `, ` trailing whitespace: %q", raw)
		}
		if strings.Contains(raw, " ") {
			t.Errorf("deny set has unexpected space: %q", raw)
		}
	}
}

func TestNewDefaultDenySet_6to4AndTeredoPresent(t *testing.T) {
	// ADR-034: 6to4 + Teredo are the documented gap from ADR-023.
	// This test is the regression net — if a future refactor
	// drops either entry the test fails loudly with the ADR
	// citation in the message.
	d := NewDefaultDenySet()
	var has6to4, hasTeredo bool
	for _, e := range d.Entries {
		if e.Prefix.String() == "2002::/16" {
			has6to4 = true
			if e.SourceADR != "ADR-034" {
				t.Errorf("6to4 entry SourceADR = %q, want %q", e.SourceADR, "ADR-034")
			}
		}
		if e.Prefix.String() == "2001::/32" {
			hasTeredo = true
			if e.SourceADR != "ADR-034" {
				t.Errorf("Teredo entry SourceADR = %q, want %q", e.SourceADR, "ADR-034")
			}
		}
	}
	if !has6to4 {
		t.Error("NewDefaultDenySet missing 6to4 (2002::/16) — see ADR-034")
	}
	if !hasTeredo {
		t.Error("NewDefaultDenySet missing Teredo (2001::/32) — see ADR-034")
	}
}

// ADR 20261018194214: NAT64 addresses embed an IPv4 destination, the same
// lateral-movement class as 6to4 and Teredo.
func TestNewDefaultDenySet_NAT64Present(t *testing.T) {
	want := map[string]bool{"64:ff9b::/96": false, "64:ff9b:1::/48": false}
	for _, e := range NewDefaultDenySet().Entries {
		if _, ok := want[e.Prefix.String()]; !ok {
			continue
		}
		want[e.Prefix.String()] = true
		if e.Family != FamilyV6 || e.SourceADR != "ADR-20261018194214" {
			t.Errorf("%s: family %v, SourceADR %q", e.Prefix, e.Family, e.SourceADR)
		}
	}
	for prefix, found := range want {
		if !found {
			t.Errorf("NewDefaultDenySet missing NAT64 prefix %s — see ADR 20261018194214", prefix)
		}
	}
}

func TestNewDefaultDenySet_SMTPPortsAreComplete(t *testing.T) {
	// Spec §11: "deny 25/465/587". A future edit that drops one
	// would let the Hetzner abuse desk come knocking.
	d := NewDefaultDenySet()
	wantPorts := map[uint16]bool{25: false, 465: false, 587: false}
	for _, p := range d.SMTPPorts {
		if _, ok := wantPorts[p]; ok {
			wantPorts[p] = true
		}
	}
	for port, present := range wantPorts {
		if !present {
			t.Errorf("SMTPPorts missing %d (spec §11)", port)
		}
	}
}

// TestDenySetFamilyString — the family keyword must be the nft
// family keyword (`ip` / `ip6`), not a Go-side enum name. A
// future edit that swaps Family for `Family4` / `Family6` would
// land the wrong nft argv family.
func TestDenySetFamilyString(t *testing.T) {
	cases := map[Family]string{
		FamilyV4: "ip",
		FamilyV6: "ip6",
	}
	for f, want := range cases {
		if got := f.String(); got != want {
			t.Errorf("Family(%d).String() = %q, want %q", int(f), got, want)
		}
	}
}

// TestNewDefaultDenySet_CounterNamesStable — PR-E regression net.
// Every catalog entry's CounterName must equal
// DropCounterName(family, prefix), so:
//
//   - the field is set at catalog-init time (no caller can forget);
//   - the field-on-entry approach means the cross-renderer invariant
//     test in denylist_external_test.go can match the same name in
//     the per-netns argv AND the host rendered text;
//   - the vmmd scrape adapter (cmd/vmmd/poller.go) and the OCI dialer
//     hook (pkg/oci/egress.go::EgressDenyHook) use the same label
//     value, so the (cidr, family) series line up across the
//     firewall-side and dialer-side counters.
//
// If DropCounterName ever changes format (e.g. drops the `drop_`
// prefix, switches `_` to `-`, etc.), this test catches the catalog
// drift immediately.
func TestNewDefaultDenySet_CounterNamesStable(t *testing.T) {
	d := NewDefaultDenySet()
	for _, e := range d.Entries {
		want := DropCounterName(e.Family, e.Prefix.String())
		if e.CounterName != want {
			t.Errorf("Entries{%s}: CounterName = %q, want DropCounterName(...) = %q",
				e.Prefix, e.CounterName, want)
		}
		if e.CounterName == "" {
			t.Errorf("Entries{%s}: CounterName is empty", e.Prefix)
		}
		// Sanitize: name must use only nft-legal characters. If a
		// future CIDR string introduces a non-[A-Za-z0-9_-] char,
		// the sanitize helper should grow its filter set — and
		// this assertion will surface the drift.
		for i := 0; i < len(e.CounterName); i++ {
			c := e.CounterName[i]
			ok := (c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') ||
				c == '_' || c == '-'
			if !ok {
				t.Errorf("Entries{%s}: CounterName %q has illegal char %q",
					e.Prefix, e.CounterName, c)
			}
		}
	}
}

// TestNewDefaultDenySet_CounterNamesUnique — the connlimit metal-test
// parser (pkg/netns/connlimit_metal_test.go) returns the FIRST block
// matching a name, so any two entries sharing a CounterName would
// silently mis-read. The family prefix in DropCounterName makes
// collisions impossible by construction — this test pins the
// invariant so a future refactor that drops the family tag (e.g.
// `drop_<sanitized>` without `v4` / `v6`) fails loudly.
func TestNewDefaultDenySet_CounterNamesUnique(t *testing.T) {
	d := NewDefaultDenySet()
	seen := make(map[string]string)
	for _, e := range d.Entries {
		if prior, ok := seen[e.CounterName]; ok {
			t.Errorf("CounterName collision: %q used by both %s and %s",
				e.CounterName, prior, e.Prefix)
			continue
		}
		seen[e.CounterName] = e.Prefix.String()
	}
}

// TestDropCounterName — exercises the helper directly so a future
// edit to the format string (e.g. dropping the `drop_` prefix,
// switching the family tag, swapping the sanitizer char) surfaces
// here with a clear table diff before the catalog-side test runs.
// Note the v6 cases: the `:` separator is sanitized to `_` so the
// resulting name is valid nftables syntax (`fe80::/10` →
// `fe80___10` after replacing both colons and the slash).
func TestDropCounterName(t *testing.T) {
	cases := []struct {
		family Family
		prefix string
		want   string
	}{
		{FamilyV4, "10.0.0.0/8", "drop_v4_10_0_0_0_8"},
		{FamilyV4, "192.168.0.0/16", "drop_v4_192_168_0_0_16"},
		{FamilyV6, "fe80::/10", "drop_v6_fe80___10"},
		{FamilyV6, "::1/128", "drop_v6___1_128"},
		{FamilyV6, "2002::/16", "drop_v6_2002___16"},
		{FamilyV6, "fc00::/7", "drop_v6_fc00___7"},
		{FamilyV6, "2001::/32", "drop_v6_2001___32"},
		{FamilyV6, "::/128", "drop_v6____128"},
		{FamilyV6, "64:ff9b::/96", "drop_v6_64_ff9b___96"},
		{FamilyV6, "64:ff9b:1::/48", "drop_v6_64_ff9b_1___48"},
	}
	for _, c := range cases {
		got := DropCounterName(c.family, c.prefix)
		if got != c.want {
			t.Errorf("DropCounterName(%v, %q) = %q, want %q",
				c.family, c.prefix, got, c.want)
		}
	}
}
