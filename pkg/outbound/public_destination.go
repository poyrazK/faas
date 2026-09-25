package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

var ErrUnsafeDestination = errors.New("outbound destination is not globally reachable")

// PublicDestinationDialer resolves each provider hostname at connection time,
// rejects the whole answer if any address is non-public, and dials one of the
// checked IPs directly. The URL hostname is retained by net/http for TLS SNI
// and certificate verification; only the TCP destination is pinned.
type PublicDestinationDialer struct {
	resolver *net.Resolver
	dialer   *net.Dialer
}

func NewPublicDestinationDialer() *PublicDestinationDialer {
	return &PublicDestinationDialer{
		resolver: net.DefaultResolver,
		dialer:   &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
	}
}

// ValidatePublicOrigin verifies the current DNS answer for a prospective
// HTTPS integration. The gateway repeats this check at every new connection,
// so a later DNS change cannot redirect the socket into a private network.
func ValidatePublicOrigin(ctx context.Context, origin *url.URL) error {
	if origin == nil || origin.Scheme != "https" || origin.Hostname() == "" ||
		origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" {
		return fmt.Errorf("%w: origin must be an https URL without credentials, query, or fragment", ErrUnsafeDestination)
	}
	port := origin.Port()
	if port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return fmt.Errorf("%w: origin port is invalid", ErrUnsafeDestination)
		}
	}
	_, err := lookupPublicAddresses(ctx, net.DefaultResolver, origin.Hostname())
	return err
}

func (d *PublicDestinationDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("outbound target address is invalid: %w", err)
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("outbound target network is unsupported: %s", network)
	}
	resolver := d.resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := d.dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	ips, err := lookupPublicAddresses(ctx, resolver, host)
	if err != nil {
		return nil, err
	}
	var dialErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr == nil {
		dialErr = errors.New("no address matched the requested network")
	}
	return nil, fmt.Errorf("outbound public destination dial failed: %w", dialErr)
}

func lookupPublicAddresses(ctx context.Context, resolver *net.Resolver, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if !isPublicDestination(ip) {
			return nil, ErrUnsafeDestination
		}
		return []netip.Addr{ip}, nil
	}
	if resolver == nil || host == "" {
		return nil, fmt.Errorf("%w: hostname is missing", ErrUnsafeDestination)
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve outbound destination: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: hostname has no addresses", ErrUnsafeDestination)
	}
	for _, ip := range ips {
		if !isPublicDestination(ip.Unmap()) {
			return nil, ErrUnsafeDestination
		}
	}
	return ips, nil
}

func isPublicDestination(ip netip.Addr) bool {
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is6() && !ipv6GlobalUnicastPrefix.Contains(ip) {
		return false
	}
	blocked := ipv4NonPublicPrefixes
	if ip.Is6() {
		blocked = ipv6NonPublicPrefixes
	}
	for _, prefix := range blocked {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

var ipv4NonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),  // Shared address space.
	netip.MustParsePrefix("192.0.0.0/24"),   // Protocol assignments; conservative over globally reachable exceptions.
	netip.MustParsePrefix("192.0.2.0/24"),   // Documentation.
	netip.MustParsePrefix("192.88.99.0/24"), // Deprecated 6to4 relay space.
	netip.MustParsePrefix("198.18.0.0/15"),  // Benchmarking.
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), // Reserved and limited broadcast.
}

var ipv6GlobalUnicastPrefix = netip.MustParsePrefix("2000::/3")

var ipv6NonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("::/96"),          // Deprecated IPv4-compatible addresses.
	netip.MustParsePrefix("64:ff9b::/96"),   // Well-known NAT64 can encode private IPv4 destinations.
	netip.MustParsePrefix("64:ff9b:1::/48"), // Local-use NAT64.
	netip.MustParsePrefix("100::/64"),       // Discard-only.
	netip.MustParsePrefix("100:0:0:1::/64"), // Dummy prefix.
	netip.MustParsePrefix("2001::/23"),      // IETF protocol assignments.
	netip.MustParsePrefix("2001:db8::/32"),  // Documentation.
	netip.MustParsePrefix("2002::/16"),      // 6to4.
	netip.MustParsePrefix("3fff::/20"),      // Documentation.
	netip.MustParsePrefix("5f00::/16"),      // Segment-routing SIDs.
}
