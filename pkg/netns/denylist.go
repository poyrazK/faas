package netns

// Single source of truth for the tenant-egress denylist. Spec §11 +
// ADR-023 (v6 family split) + ADR-034 (6to4 + Teredo). Three
// consumers — the per-netns renderer (pkg/netns/config.go), the host
// renderer (pkg/netns/policy.go), and the OCI puller
// (pkg/oci/egress.go) — all read from NewDefaultDenySet() so the
// firewall rules and the user-space check can never drift apart.
//
// Renaming a field on HostPolicy / inlining new CIDRs in
// NftCommands() / adding a deny to deniedEntriesV4 in oci/egress.go is
// how this code base silently dropped a deny line in the past
// (issue #146). The DenySet type makes "the deny list" a thing
// you import; add a new CIDR once, three places update.

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Family identifies which nft family an entry applies to. The
// per-netns forward chain is split into `ip faas` and `ip6 faas`
// (ADR-023); the host forward chain is a single `table inet faas`
// chain that uses `ip daddr` / `ip6 daddr` directly. The renderer
// picks the right nft keyword from this tag.
type Family int

const (
	FamilyV4 Family = iota
	FamilyV6
)

// Family keyword constants — nft uses `ip` for v4 and `ip6` for v6.
// Pulled into named constants so the goconst linter stops asking
// for them and so every call site reads as a typed symbol rather
// than a bare string.
const (
	familyKeywordV4 = "ip"
	familyKeywordV6 = "ip6"
)

func (f Family) String() string {
	switch f {
	case FamilyV4:
		return familyKeywordV4
	case FamilyV6:
		return familyKeywordV6
	default:
		return fmt.Sprintf("Family(%d)", int(f))
	}
}

// EgressDenyClass is the bounded operator-facing vocabulary for blocked
// tenant egress. Keep this closed: it is used as a Prometheus label.
type EgressDenyClass string

const (
	EgressDenyClassSMTP      EgressDenyClass = "smtp"
	EgressDenyClassRFC1918   EgressDenyClass = "rfc1918"
	EgressDenyClassMetadata  EgressDenyClass = "metadata"
	EgressDenyClassAllowlist EgressDenyClass = "allowlist"

	// Named counters used by the aggregate rules. Per-CIDR counters keep
	// their existing drop_* names and are rolled up by Class.
	EgressDenyCounterSMTP      = "deny_smtp"
	EgressDenyCounterAllowlist = "deny_allowlist"
)

// DenyEntry is a single CIDR entry on the denylist. SourceADR +
// Comment make the provenance machine-readable so a future "list
// every deny line" operator tool can render the table from
// introspection rather than a hand-maintained doc.
//
// CounterName is the nftables named counter attached to the deny
// rule for this entry (PR-E). Setting it once at catalog-init time
// keeps the name a stable contract across both renderers (the
// per-netns argv in Config.NftCommands and the host rendered text
// in HostPolicy.Render) and the OCI puller's hook. The naming
// convention is `drop_v4_<sanitized>` / `drop_v6_<sanitized>`; the
// family-tagged prefix ensures the v4 and v6 counters don't share
// a name (the connlimit metal-test parser at
// pkg/netns/connlimit_metal_test.go::counterPackets returns the
// FIRST block matching a name, so a v4/v6 collision would be
// silently mis-read). Built once via DropCounterName during
// NewDefaultDenySet so adding a new CIDR is one literal line.
type DenyEntry struct {
	Family    Family
	Prefix    netip.Prefix
	SourceADR string
	Comment   string
	// CounterName is the nftables named counter attached to this
	// entry's deny rule. Set by NewDefaultDenySet via DropCounterName;
	// tests / callers must not hand-set it.
	CounterName string
}

// Class returns the aggregate C1 class for this deny entry. The metadata
// range is kept distinct from the broader private/link-local catalog because
// an IMDS hit is an actionable control-plane signal.
func (e DenyEntry) Class() EgressDenyClass {
	if e.Prefix == netip.MustParsePrefix("169.254.0.0/16") {
		return EgressDenyClassMetadata
	}
	return EgressDenyClassRFC1918
}

// DropCounterName returns the nftables named counter for an entry
// of the given family. The format is `drop_v4_<sanitized>` for v4
// and `drop_v6_<sanitized>` for v6, where `<sanitized>` is the CIDR
// string with `.` and `/` replaced by `_`. Family is dropped because
// the family tag is already on the prefixed word — nft scopes named
// counters per table/chain family, but the v4+v6 counter names must
// be globally distinct because the connlimit metal-test parser (and
// the nft -j list counters JSON shape) returns the FIRST match by
// name.
//
// The counter name is exported as a counter (not a per-netns set
// rule) so each catalog entry is observable individually at
// `nft list counters` and via the vmmd scrape adapter on
// <daemon>_egress_deny_total{cidr,family}.
//
// nft accepts [A-Za-z0-9_-] in counter names; the security-critical
// characters in a CIDR — `.` and `/` — are not in that set, so the
// sanitization is deterministic and the resulting name is valid
// nftables syntax. The longest possible name is the
// 2001:0000:0000:0000:0000:0000:0000:0001 / 128 v6 sample
// (sanitized: `drop_v6_2001_0000_0000_0000_0000_0000_0000_0001_128`)
// at 50 chars — under nft's 64-char counter name ceiling.
func DropCounterName(family Family, prefix string) string {
	fam := "v4"
	if family == FamilyV6 {
		fam = "v6"
	}
	return sanitizeCounterName("drop_" + fam + "_" + prefix)
}

// sanitizeCounterName replaces `.`, `/`, and `:` with `_`. Underscores
// are the only legal substitution: nft accepts [A-Za-z0-9_-] in
// counter names, and the dot-slash-colon sanitization is the same
// transformation the operator-facing artifact uses for safe CIDR
// strings. The colon is the v6 separator (`2001:db8::/32`) and
// MUST be in the filter set — omitting it would land names like
// `drop_v6_2001:db8::_32` which nft rejects with "invalid
// character in counter name".
func sanitizeCounterName(s string) string {
	// Allocate once; the result is at most len(s) bytes long.
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == '/' || c == ':' {
			b = append(b, '_')
		} else {
			b = append(b, c)
		}
	}
	return string(b)
}

// DenySet is the typed denylist. The four slice fields are the
// canonical allowlist-of-deny CIDRs / ports / counters every
// renderer reads. Entries is the metadata-rich view used by
// operator tooling and the regression net; the typed slices are
// derived from Entries so they can't drift apart.
type DenySet struct {
	// V4DenyCIDRs is the IPv4 egress denylist (spec §11).
	V4DenyCIDRs []netip.Prefix
	// V6DenyCIDRs is the IPv6 egress denylist (ADR-023 + ADR-034).
	V6DenyCIDRs []netip.Prefix
	// SMTPPorts is the egress TCP port denylist (spec §11):
	// 25, 465, 587. Spam = Hetzner abuse desk = existential
	// (spec §7 founding doc R6).
	SMTPPorts []uint16
	// ConntrackCap is the §7 per-instance conntrack cap (default
	// 4096). Renderers may consult this for telemetry / dashboard
	// exposition but it does NOT participate in the deny-line argv
	// (the cap is its own `ct count over N drop` rule).
	ConntrackCap uint32
	// Entries is the provenance-bearing view. Length == len(V4DenyCIDRs)
	// + len(V6DenyCIDRs); same data, sorted by family then CIDR.
	Entries []DenyEntry
	// OperatorExceptions is the explicit allow-before-deny list
	// (PR scale-out tier-1 residual Gap #4). The renderer emits
	// one `ip saddr <ex> accept` rule per entry, placed BEFORE
	// the per-CIDR deny block on both the host forward chain
	// (pkg/netns/policy.go::Render) and the per-netns forward
	// chain (pkg/netns/config.go::NftCommands). Operators set
	// this when they want overlay traffic to survive the §11
	// deny — typically when their overlay (e.g. Tail/wg subnet
	// 100.64.0.0/10 or a custom RFC1918) lives inside the
	// always-deny catalog. Empty by default — single-host dev
	// keeps the legacy "deny wins on RFC1918" posture.
	OperatorExceptions []netip.Prefix
}

// NewDefaultDenySet returns the platform-wide default denylist.
// This is the single function to edit when adding a new CIDR —
// every consumer (per-netns, host, oci) reads from it.
//
// Provenance: every entry names the ADR or RFC that sourced it.
// "spec" entries trace to spec §11 + spec §7 founding doc;
// "ADR-NNN" entries are platform decisions. Do not edit without
// an ADR — see the ADR-031 + ADR-033 precedent for "why each
// line is here".
func NewDefaultDenySet() DenySet {
	entries := []DenyEntry{
		// IPv4 — RFC1918 + link-local/metadata + CGN (spec §11).
		{
			Family:      FamilyV4,
			Prefix:      netip.MustParsePrefix("10.0.0.0/8"),
			SourceADR:   "spec-§11",
			Comment:     "RFC1918 — private network",
			CounterName: DropCounterName(FamilyV4, "10.0.0.0/8"),
		},
		{
			Family:      FamilyV4,
			Prefix:      netip.MustParsePrefix("172.16.0.0/12"),
			SourceADR:   "spec-§11",
			Comment:     "RFC1918 — private network",
			CounterName: DropCounterName(FamilyV4, "172.16.0.0/12"),
		},
		{
			Family:      FamilyV4,
			Prefix:      netip.MustParsePrefix("192.168.0.0/16"),
			SourceADR:   "spec-§11",
			Comment:     "RFC1918 — private network",
			CounterName: DropCounterName(FamilyV4, "192.168.0.0/16"),
		},
		{
			Family:      FamilyV4,
			Prefix:      netip.MustParsePrefix("169.254.0.0/16"),
			SourceADR:   "spec-§11",
			Comment:     "link-local; 169.254.169.254 = cloud metadata IMDS",
			CounterName: DropCounterName(FamilyV4, "169.254.0.0/16"),
		},
		{
			Family:      FamilyV4,
			Prefix:      netip.MustParsePrefix("100.64.0.0/10"),
			SourceADR:   "RFC6598",
			Comment:     "carrier-grade NAT",
			CounterName: DropCounterName(FamilyV4, "100.64.0.0/10"),
		},

		// IPv6 — link-local + ULA + multicast + loopback + unspecified
		// (ADR-023 + ADR-034).
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("fe80::/10"),
			SourceADR:   "ADR-023",
			Comment:     "IPv6 link-local; neighbor-table exposure to guests",
			CounterName: DropCounterName(FamilyV6, "fe80::/10"),
		},
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("fc00::/7"),
			SourceADR:   "ADR-023",
			Comment:     "IPv6 ULA (RFC4193); control-plane lateral movement",
			CounterName: DropCounterName(FamilyV6, "fc00::/7"),
		},
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("ff00::/8"),
			SourceADR:   "ADR-023",
			Comment:     "IPv6 multicast; no use case in this model",
			CounterName: DropCounterName(FamilyV6, "ff00::/8"),
		},
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("::1/128"),
			SourceADR:   "ADR-023",
			Comment:     "IPv6 loopback",
			CounterName: DropCounterName(FamilyV6, "::1/128"),
		},
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("::/128"),
			SourceADR:   "ADR-023",
			Comment:     "IPv6 unspecified; misconfigured or malicious",
			CounterName: DropCounterName(FamilyV6, "::/128"),
		},
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("2002::/16"),
			SourceADR:   "ADR-034",
			Comment:     "6to4 (RFC3056); tunnels IPv6 over IPv4 — lateral movement into 10/8 etc.",
			CounterName: DropCounterName(FamilyV6, "2002::/16"),
		},
		{
			Family:      FamilyV6,
			Prefix:      netip.MustParsePrefix("2001::/32"),
			SourceADR:   "ADR-034",
			Comment:     "Teredo (RFC4380); tunnels IPv6 over UDP/3544 — same lateral-movement risk as 6to4",
			CounterName: DropCounterName(FamilyV6, "2001::/32"),
		},
	}

	// Sanity: every entry has a CounterName. A future edit that
	// drops the `CounterName:` field on a literal would silently
	// lose the per-CIDR observability — the deny rule would still
	// fire, the entry would still deny, but `nft list counters`
	// would not show it. Surfacing the silent break here keeps the
	// catalog-add path mechanical (one literal line, every field
	// populated).
	for i, e := range entries {
		if e.CounterName == "" {
			panic(fmt.Sprintf("netns.NewDefaultDenySet: entry[%d] %s missing CounterName", i, e.Prefix.String()))
		}
	}

	// Sort Entries by family then prefix for deterministic ordering
	// across renderers + tests.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Family != entries[j].Family {
			return entries[i].Family < entries[j].Family
		}
		return entries[i].Prefix.Addr().Less(entries[j].Prefix.Addr())
	})

	d := DenySet{
		SMTPPorts:    []uint16{25, 465, 587},
		ConntrackCap: 4096,
		Entries:      entries,
	}
	for _, e := range entries {
		switch e.Family {
		case FamilyV4:
			d.V4DenyCIDRs = append(d.V4DenyCIDRs, e.Prefix)
		case FamilyV6:
			d.V6DenyCIDRs = append(d.V6DenyCIDRs, e.Prefix)
		}
	}
	return d
}

// V4CommaSet returns V4DenyCIDRs joined with `,` (the modern-nft
// CIDR-set syntax gate — see memory `nft-cidr-set-comma-required`).
// Used by both per-netns and host renderers so the renderer
// surface stays one helper, not three.
func (d DenySet) V4CommaSet() string {
	parts := make([]string, len(d.V4DenyCIDRs))
	for i, p := range d.V4DenyCIDRs {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

// V6CommaSet is the IPv6 sibling of V4CommaSet.
func (d DenySet) V6CommaSet() string {
	parts := make([]string, len(d.V6DenyCIDRs))
	for i, p := range d.V6DenyCIDRs {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

// SMTPPortsCommaSet renders SMTPPorts as a comma-joined uint16 list
// for the nft `tcp dport { … } drop` set syntax. Mirrors the
// HostPolicy.joinInts helper but with uint16 (SMTPPorts is the
// typed slice; the int slice on HostPolicy is the legacy surface
// the helper consumed).
func (d DenySet) SMTPPortsCommaSet() string {
	parts := make([]string, len(d.SMTPPorts))
	for i, p := range d.SMTPPorts {
		parts[i] = fmt.Sprintf("%d", p)
	}
	return strings.Join(parts, ",")
}
