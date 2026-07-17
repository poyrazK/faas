package oci

// Egress guard for the OCI registry client (issue #27).
//
// The host-side nft ruleset (pkg/netns/policy.go, deployed to /etc/nftables.conf
// via `make egress-render`) filters traffic from guest VMs at the bridge
// boundary — but it does NOT filter egress that originates from the control
// plane (apid, imaged, schedd, gatewayd). imaged specifically reaches out to
// public registries on behalf of customers. A customer-controlled OCI
// reference could (via DNS or DNS-rebinding) resolve to a private IP and
// induce imaged to talk to the box's own RFC1918 range, the cloud metadata
// endpoint, or any other host the customer can intercept for an SSRF.
//
// This file plugs that gap at the http.Transport's DialContext level. The
// deny-lists mirror pkg/netns.DefaultHostPolicy.ForwardDenyCIDRs verbatim;
// TestEgressGuardMatchesHostPolicy in registry_test.go enforces that they
// stay in sync. We deliberately don't import pkg/netns from pkg/oci to avoid
// pulling netns-specific concepts (TenantBridge, AppPort) into the OCI seam.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrEgressDenied is returned when the egress guard refuses a dial because
// every resolved IP for the host falls inside a denied CIDR (RFC1918,
// link-local, metadata, CGN, loopback, multicast, unspecified), or the
// target port is SMTP. Stable sentinel — production callers (imaged deploy
// pipeline) and tests match on this via errors.Is.
var ErrEgressDenied = errors.New("oci: egress denied")

// deniedCIDRs is the IPv4 deny-list. Mirrors
// pkg/netns.DefaultHostPolicy.ForwardDenyCIDRs; the unit test
// TestEgressGuardMatchesHostPolicy fails CI if anyone drifts. Exported
// as a var so tests can iterate it; production code uses AllowIP().
//
// SMTP ports and IPv6 specials are NOT in this slice — they're checked
// inside AllowIP / isSMTPPort because the rule shape differs (host-pair
// vs port number vs IPv6 bit-pattern check).
var deniedCIDRs = mustCIDRs([]string{
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"169.254.0.0/16", // link-local + cloud metadata (covers 169.254.169.254)
	"100.64.0.0/10",  // CGN (RFC6598)
})

// mustCIDRs parses a slice of CIDR strings, panicking on failure. The slice
// is a fixed compile-time constant above; a parse error means a developer
// typo and we want to fail at init time, not at first dial.
func mustCIDRs(in []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(in))
	for _, s := range in {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(fmt.Sprintf("oci: egress deny-list contains invalid CIDR %q: %v", s, err))
		}
		out = append(out, n)
	}
	return out
}

// AllowIP returns true iff ip is allowed outbound by the egress policy.
//
// Denied:
//   - nil / unspecified / broadcast / multicast / loopback
//   - IPv4 0.0.0.0/8 ("this network")
//   - anything matching deniedCIDRs (RFC1918 + link-local/metadata + CGN)
//   - IPv4-mapped IPv6 (::ffff:0:0/96) — normalized to v4 and re-checked
//     so ::ffff:10.0.0.5 is denied because 10.0.0.5 is denied
//   - IPv6 link-local (fe80::/10)
//   - IPv6 ULA (fc00::/7)
//   - IPv6 multicast (ff00::/8)
//
// Allowed: any other IP (public IPv4/IPv6).
func AllowIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	// Normalize IPv4-in-IPv6 so ::ffff:10.0.0.5 is checked against 10.0.0.0/8.
	// Without this an attacker could present an IPv6-formatted RFC1918
	// address and slip through.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}

	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 0.0.0.0/8 ("this network") and 255.255.255.255 (limited broadcast).
		if ip4[0] == 0 || ip4.Equal(net.IPv4bcast) {
			return false
		}
		for _, n := range deniedCIDRs {
			if n.Contains(ip) {
				return false
			}
		}
		return true
	}
	// Pure IPv6 (16-byte form, not in 4-byte form).
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	// ULA: fc00::/7 covers fc00..fdff. Manual check; net.IPNet without
	// keeping a sentinel here saves an allocation.
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return true
}

// dialer resolves a host to IPs, filters through AllowIP, and dials the
// first allowed IP. The IP-pinning defeats DNS-rebinding: even if a
// subsequent resolver query returns a different IP, the live connection
// keeps talking to the IP we authorized at lookup time. The HTTP layer
// passes the original hostname as TLS ServerName / Host header via the
// existing http.Request fields, so the registry sees `ghcr.io/foo` and
// the kernel socket talks to the IP we vetted.
type dialer struct {
	resolver resolverIface
	timeout  time.Duration
}

// resolverIface is the subset of *net.Resolver the dialer depends on.
// Production wires *net.DefaultResolver; tests can swap in a stub that
// returns deterministic IPs to exercise deny/allow paths without DNS.
type resolverIface interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// dialContext is the DialContext handed to http.Transport. It mirrors
// what net.Dialer{DialContext:}.DialContext would do, but with the
// resolver + filter + IP-pin steps injected.
func (d *dialer) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: nil, Err: err}
	}

	// SMTP port denylist — matches pkg/netns ForwardDenyTCPPorts. The OCI
	// client is HTTPS-only by design; deny 25/465/587 explicitly so any
	// future plaintext path can't relay spam from the control plane.
	if isSMTPPort(port) {
		return nil, &net.OpError{
			Op:  "dial",
			Net: network,
			Err: fmt.Errorf("%w: port %s (SMTP)", ErrEgressDenied, port),
		}
	}

	ips, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: err}
	}
	if len(ips) == 0 {
		return nil, &net.OpError{Op: "dial", Net: network, Err: ErrEgressDenied}
	}
	for _, ip := range ips {
		if !AllowIP(ip.IP) {
			continue
		}
		// Land the socket on a vetted IP. http.Transport's TLS handshake
		// still uses http.Request.Host (the original `ghcr.io`); the
		// HTTP/1.1 Host header is set automatically to the request URL.
		var dial net.Dialer
		return dial.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
	}
	return nil, &net.OpError{
		Op:  "dial",
		Net: network,
		Err: fmt.Errorf("%w: host %s resolved to %s", ErrEgressDenied, host, ips),
	}
}

// isSMTPPort returns true for the three spec §11 SMTP ports. Strings, not
// ints, because net.SplitHostPort returns the port as a string and the
// common case is "443"/"80" not numeric.
func isSMTPPort(port string) bool {
	switch port {
	case "25", "465", "587":
		return true
	}
	return false
}

// WithEgressHTTPClient installs an http.Client whose transport's
// DialContext enforces the egress policy. The Client times out at 30s
// (matching the current default; consistent with the spec's pull
// latency budget).
//
// Production wiring (cmd/imaged/main.go) MUST install this. The legacy
// "no option" path remains for tests that point at httptest.NewServer
// (loopback), since AllowIP denies 127.0.0.0/8 and we'd otherwise need
// a separate option for every test.
func WithEgressHTTPClient() Option {
	return func(c *RegistryClient) {
		d := &dialer{
			resolver: net.DefaultResolver,
			timeout:  30 * time.Second,
		}
		// Fresh transport — never mutate http.DefaultTransport.DialContext,
		// which would be shared across the whole process.
		c.hc = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           d.dialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       60 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	}
}
