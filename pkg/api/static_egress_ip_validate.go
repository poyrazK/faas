// ADR-119 per-app static egress IP deny set — single source of truth
// for "is this customer-supplied IPv4 safe to alias on br-tenants?"
//
// Lives in pkg/api (rather than pkg/netns) because pkg/api is the
// shared input-validation home (limits, errors, DTOs). The depguard
// rule at .golangci.yml:41 forbids cmd/apid from importing pkg/netns
// ("apid is control-plane only; it must not manage host networking"),
// so any validator apid needs to consume must live in pkg/api or a
// sibling that apid is allowed to import.
//
// Four layers consume this gate today:
//
//  1. cmd/apid/handlers_apps_static_egress_ip.go (the customer-facing
//     400 gate at PUT time). Fast-fails before the column write so a
//     typo'd IP gets surfaced inline rather than at egress-renderer
//     time.
//  2. pkg/fcvm/manager.go (wire-side defence at Wake time). apid may
//     have validated but a vmmd that forgets to re-validate must not
//     be able to smuggle a bad IP past the bridge alias.
//  3. cmd/vmmd/egress_static_ip_bundle.go (operator-side defence at
//     TOML bundle load time). An operator typo in
//     /etc/faas/egress/static_egress_ips.toml must not pin a reserved
//     IP.
//  4. pkg/netns/static_egress_ip_metal_test.go (metal acceptance gate
//     — test fixture IP must pass).
//
// All four used to be near-identical implementations. Drift was a
// real risk: a future maintainer adding e.g. 198.18.0.0/15
// (benchmarking range) to one but forgetting the others would have
// let a bad IP through the missing layer. ValidateStaticEgressIP is
// the single edit point.
//
// Mirrors the existing egress-deny catalog in pkg/netns/denylist.go:
// the customer-egress deny set is a strict subset of the host-egress
// deny set — the host layer additionally blocks SMTP ports and
// metadata ranges, neither of which is meaningful for a customer IP
// (the SMTP block lives on the host firewall, not the IP choice).
//
// Deny set (v1, deliberately narrow):
//   - IPv4 only (v6 deferred to follow-up ADR)
//   - RFC1918 (10/8, 172.16/12, 192.168/16)
//   - CGN (100.64/10)
//   - Link-local v4 (169.254/16) + IsLinkLocalUnicast + IsLinkLocalMulticast
//   - Multicast (224/4) + IsMulticast
//   - Loopback + IsUnspecified (0.0.0.0) + 0.0.0.0/8 (RFC1122 §3.2.1.3
//     "this network"; the IsUnspecified helper only catches the single
//     zero address — the full /8 is reserved per RFC6890)
//
// Deliberately NOT denied:
//   - TEST-NET-1/2/3 (192.0.2/24, 198.51.100/24, 203.0.113/24) —
//     these are public blocks reserved for documentation; a customer
//     may legitimately BYOIP from them. Denying would reject
//     "203.0.113.42" even though that's a legal public IP for them
//     to bring. The test suite pins the allowance
//     (cmd/vmmd/egress_static_ip_bundle_test.go's TEST-NET-1 case +
//     pkg/netns/static_egress_ip_metal_test.go uses 203.0.113.42 as
//     the fixture IP).
package api

import (
	"errors"
	"net/netip"
)

// staticEgressIPDenyCIDRs is the v4 CIDR deny set. Adding a new
// range here automatically extends every gate — that is the whole
// point of the consolidation.
//
// 0.0.0.0/8 (RFC1122 §3.2.1.3 / RFC6890 "this network") is the
// fix for the pre-redesign gap: the docstring claimed "0.0.0.0/8
// denied" but the original validator only checked IsUnspecified()
// (the single zero address). The /8 prefix check catches the
// whole reserved range (0.0.0.1, 0.1.2.3, 0.255.255.255, etc.)
// — every address a customer might be tempted to "type a zero"
// into. Replaces the hand-rolled copies in pkg/fcvm/manager.go,
// cmd/vmmd/egress_static_ip_bundle.go, and the metal test.
var staticEgressIPDenyCIDRs = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network" (RFC1122 §3.2.1.3)
	netip.MustParsePrefix("10.0.0.0/8"),     // RFC1918
	netip.MustParsePrefix("172.16.0.0/12"),  // RFC1918
	netip.MustParsePrefix("192.168.0.0/16"), // RFC1918
	netip.MustParsePrefix("100.64.0.0/10"),  // CGN (shared address space)
	netip.MustParsePrefix("169.254.0.0/16"), // link-local v4
	netip.MustParsePrefix("224.0.0.0/4"),    // multicast v4
}

// StaticEgressIPDenyCIDRs returns a copy of the deny set for
// introspection (e.g. rendering in an error message or a dashboard
// panel). The returned slice is a copy — callers may not mutate the
// package-private state.
func StaticEgressIPDenyCIDRs() []netip.Prefix {
	out := make([]netip.Prefix, len(staticEgressIPDenyCIDRs))
	copy(out, staticEgressIPDenyCIDRs)
	return out
}

// ValidateStaticEgressIP returns nil if ip is a legal customer-
// supplied static egress IP per ADR-119. The check is fail-closed:
// an unknown family or an empty address returns an error. The
// returned error identifies which deny entry matched (or "not
// ipv4", "loopback", etc.) so the apid handler can surface a useful
// 400 detail.
//
// This is the canonical helper; the four call sites that used to
// each carry a near-identical deny-set implementation all route
// through here now.
func ValidateStaticEgressIP(ip netip.Addr) error {
	if !ip.IsValid() {
		return errStaticEgressIP("invalid address")
	}
	if !ip.Is4() {
		return errStaticEgressIP("not ipv4 (ipv6 deferred to follow-up ADR)")
	}
	if ip.IsUnspecified() {
		return errStaticEgressIP("unspecified (0.0.0.0)")
	}
	if ip.IsLoopback() {
		return errStaticEgressIP("loopback")
	}
	if ip.IsLinkLocalUnicast() {
		return errStaticEgressIP("link-local unicast (169.254/16)")
	}
	if ip.IsLinkLocalMulticast() {
		return errStaticEgressIP("link-local multicast")
	}
	if ip.IsMulticast() {
		return errStaticEgressIP("multicast (224/4)")
	}
	for _, p := range staticEgressIPDenyCIDRs {
		if p.Contains(ip) {
			return errStaticEgressIP("in deny set: " + p.String())
		}
	}
	return nil
}

// staticEgressIPError is the typed error ValidateStaticEgressIP
// returns. Callers can match with IsStaticEgressIPError without an
// errors.As walk; the reason field is also accessible via .Error().
type staticEgressIPError struct{ reason string }

func (e *staticEgressIPError) Error() string { return "static egress ip: " + e.reason }

func errStaticEgressIP(reason string) error {
	return &staticEgressIPError{reason: reason}
}

// IsStaticEgressIPError reports whether err is a ValidateStaticEgressIP
// rejection. Convenience for callers that want to distinguish "bad IP"
// from other error categories.
func IsStaticEgressIPError(err error) bool {
	var target *staticEgressIPError
	return errors.As(err, &target)
}
