package oci

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

// Egress policy for the OCI puller (spec §11). Tenant egress is fenced by
// nftables; the puller's own HTTP client applies the same denylist in
// user-space so a misconfigured firewall never lets a public pull reach an
// internal address. The list is the conservative union of:
//
//   - RFC1918 (10/8, 172.16/12, 192.168/16)
//   - loopback (127/8)
//   - link-local (169.254/16) — covers the cloud metadata service (IMDS)
//   - IPv6 unique-local (fc00::/7) and link-local (fe80::/10)
//   - IPv4/IPv6 unspecified + multicast
//
// We DENY every denied range and only allow addresses in the public ranges.
// The transport refuses both DNS lookups that resolve into a denied range
// AND direct IPs in a denied range — closes the DNS-rebinding hole that a
// naive IP allowlist leaves open.
var (
	// deniedCIDRv4 lists every IPv4 range the puller must never reach.
	deniedCIDRv4 = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),      // unspecified
		netip.MustParsePrefix("10.0.0.0/8"),     // RFC1918
		netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT
		netip.MustParsePrefix("127.0.0.0/8"),    // loopback
		netip.MustParsePrefix("169.254.0.0/16"), // link-local + IMDS
		netip.MustParsePrefix("172.16.0.0/12"),  // RFC1918
		netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
		netip.MustParsePrefix("192.168.0.0/16"), // RFC1918
		netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
		netip.MustParsePrefix("224.0.0.0/4"),    // multicast
		netip.MustParsePrefix("240.0.0.0/4"),    // reserved
	}
	deniedCIDRv6 = []netip.Prefix{
		netip.MustParsePrefix("::/128"),    // unspecified
		netip.MustParsePrefix("::1/128"),   // loopback
		netip.MustParsePrefix("fe80::/10"), // link-local
		netip.MustParsePrefix("fc00::/7"),  // ULA
		netip.MustParsePrefix("ff00::/8"),  // multicast
	}
)

// EgressDialContext returns a DialContext that rejects every address that
// resolves into a denied range. It is the transport that cmd/imaged and
// pkg/builderd plug into the OCI puller.
//
// The check happens AFTER DNS resolution so a hostname that resolves to a
// denied IP is refused (this is the same class of attack the firewall
// denylist on the box itself tries to catch — the user-space check is the
// belt-and-braces duplicate the financial model relies on: a bug in either
// layer still leaves the other holding).
func EgressDialContext(parent *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if parent == nil {
		parent = &net.Dialer{}
	}
	resolver := parent.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("oci: egress: bad addr %q: %w", addr, err)
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("oci: egress: resolve %s: %w", host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("oci: egress: no addresses for %s", host)
		}
		// Reject the whole dial if ANY returned address is denied — a hostname
		// that resolves to both a public and a private IP would otherwise be
		// race-able, and the §11 policy is the conservative deny.
		for _, ipa := range ips {
			addr, ok := netip.AddrFromSlice(ipa.IP)
			if !ok {
				return nil, fmt.Errorf("oci: egress: unparseable address for %s", host)
			}
			addr = addr.Unmap()
			if !ipAllowed(addr) {
				// ADR-021: lift the egress-denial failure mode to a
				// sentinel that pkg/api.SentinelToCode maps to the
				// RFC 7807 CodeImageEgressDenied (security-class
				// signal, 403). The legacy ErrEgressDenied sentinel
				// below is kept wrapped inside this one for backwards
				// compat — pkg/oci consumers that already check
				// errors.Is(err, ErrEgressDenied) continue to work,
				// and pkg/api.SentinelToCode picks up the new
				// canonical sentinel.
				return nil, fmt.Errorf("%w: address %s (%s)",
					ErrImageEgressDenied, host, addr)
			}
		}
		// Dial the first public IP explicitly to defeat DNS rebinding across
		// the resolution/dial gap.
		return parent.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
}

// NewEgressHTTPClient returns an *http.Client that uses EgressDialContext as
// its transport dialer. Callers who already have an http.Client with options
// (proxy, timeouts) can build one with http.DefaultTransport and override
// only the dial hook:
//
//	tr := &http.Transport{DialContext: oci.EgressDialContext(nil)}
//	hc := &http.Client{Transport: tr, Timeout: 30*time.Second}
func NewEgressHTTPClient() *http.Client {
	tr := &http.Transport{
		DialContext: EgressDialContext(nil),
	}
	return &http.Client{Transport: tr}
}

// ipAllowed reports whether ip is in a publicly routable range. It is the
// single place the egress policy is enforced; add a new denied range to
// deniedCIDRv4 / deniedCIDRv6 and the test in egress_test.go picks it up.
func ipAllowed(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return false
	}
	var denied []netip.Prefix
	if ip.Is4() || ip.Is4In6() {
		denied = deniedCIDRv4
	} else {
		denied = deniedCIDRv6
	}
	for _, p := range denied {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ErrEgressDenied is returned (wrapped) when a dial target violates the
// §11 policy. Callers can errors.Is against it.
var ErrEgressDenied = errors.New("oci: egress denied")
