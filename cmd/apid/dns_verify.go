// cmd/apid/dns_verify.go — domain verification + cert dial helpers
// (issue #961 / Mega-A PR-3, code-review round).
//
// Two new seams for the `gregale domains verify` and `gregale domains
// show` surface:
//
//  1. cnameLookupFunc — mirrors the existing txtLookupFunc at
//     cmd/apid/dns_poller.go:184. Tests inject a fake that returns
//     canned CNAME targets; production uses net.Resolver.LookupCNAME.
//
//  2. dialCert — performs a TLS handshake on <domain>:443 and
//     returns the leaf cert. Used by the show endpoint to surface
//     NotAfter + SANs, and by the verify endpoint to detect the
//     "customer's DNS points at a CDN" case (leaf cert has no
//     <domain> in DNSNames).
//
// Review fixes (Aug 2026):
//   - HIGH-1: sanContains now matches exact hostnames only; wildcard
//     SANs (e.g. "*.example.com") match a queried subdomain only when
//     the queried host's suffix matches the wildcard base. A cert
//     issued for "*.cloudflare.com" no longer falsely matches a
//     customer's "api.example.com" query.
//   - CRIT-2: dialCert now uses net.Dialer.DialContext(ctx, ...) so
//     request cancellation and request-budget timeouts abort the TCP
//     dial (and the TLS handshake that follows). Previously the ctx
//     was passed but only consumed via tls.DialWithDialer's internal
//     timeout path, so a hung DNS server held the request goroutine
//     indefinitely.
//   - MED-4: dialCert now returns the structured errCertFailure
//     sentinel on any dial/parse failure, so domainResponseWithCert
//     can surface a CertStatus field rather than silently leaving
//     CertNotAfter/CertSANs empty. The verify endpoint maps the
//     sentinel to 422 CertNotIssued.

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// cnameLookupFunc is the test seam for the CNAME verifier. Mirrors
// txtLookupFunc (cmd/apid/dns_poller.go:184) — production uses the
// real net.Resolver; tests inject a fake that returns canned records.
var cnameLookupFunc = func(ctx context.Context, target string) (string, error) {
	return (&net.Resolver{}).LookupCNAME(ctx, target)
}

// certLookupIPFunc and certDialContextFunc split DNS resolution from the TCP
// connection so production validates every resolved address and dials an exact
// approved IP. The hostname remains only as TLS SNI, closing the
// resolve-check-resolve DNS-rebinding gap.
var certLookupIPFunc = func(ctx context.Context, target string) ([]net.IPAddr, error) {
	return (&net.Resolver{}).LookupIPAddr(ctx, target)
}

var certDialContextFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: dialCertTimeout}
	return dialer.DialContext(ctx, network, address)
}

// errCDNCert is returned by dialCert when the port-443 cert is a
// CDN cert whose SANs do not include the customer's domain. The
// verifyDomain handler maps this to 422 CodeDomainCertNotIssued.
var errCDNCert = errors.New("port-443 cert does not include target domain")

// errCertFailure is returned by dialCert when the TCP dial, TLS
// handshake, or cert parse fails for any reason other than a CDN
// cert mismatch. The show endpoint surfaces this as
// CertStatus="dial_failed:<reason>" so the customer can distinguish
// "DNS not propagated" from "cert not yet issued" from "TLS handshake
// refused by upstream CDN".
var errCertFailure = errors.New("cert dial failed")

// These errors deliberately omit resolved addresses and peer details so they
// are safe to return through customer-facing domain diagnostics.
var errCertAddressBlocked = errors.New("certificate endpoint is not a public address")
var errCertRoutingMismatch = errors.New("domain does not point to Gregale")

// CertStatus tokens rendered on the CustomDomainResponse.CertStatus
// wire field. Pulled into named consts so goconst (package-wide)
// stops tripping on the literal "pending" / "issued" used across
// handlers_ext.go + the dialCert call sites.
const (
	certStatusIssued  = "issued"
	certStatusPending = "pending"
)

// dialCertTimeout is the upper bound on dialCert. Matches the
// DNSVerifier timeout (5s) so the verify endpoint never blocks past
// the request budget. A misconfigured DNS server (the most common
// stall) trips this and the operator gets a clean failure.
const dialCertTimeout = 5 * time.Second

// dialCertFunc is the test seam for the cert dial. Production uses
// the real tls.Dial; tests inject an httptest.NewTLSServer-backed
// dialer via test cert dial.
var dialCertFunc = func(ctx context.Context, domain string) (*x509.Certificate, error) {
	// CRIT-2 fix: net.Dialer.DialContext(ctx, ...) honours ctx
	// cancellation AND applies its own Timeout as a hard cap. We
	// wrap the resulting raw conn in tls.Client so the TLS
	// handshake inherits the ctx via its inner Read/Write deadlines
	// (tls.Conn.SetDeadline).
	addresses, err := certLookupIPFunc(ctx, domain)
	if err != nil || len(addresses) == 0 {
		return nil, fmt.Errorf("resolve certificate endpoint: %w", errCertFailure)
	}
	for _, address := range addresses {
		if !isPublicCertAddress(address.IP) {
			return nil, fmt.Errorf("%w: %w", errCertFailure, errCertAddressBlocked)
		}
	}
	// Fail closed on a mixed answer set above, then bind the connection to one
	// validated numeric answer so net.Dialer cannot perform a second lookup.
	approvedIP := addresses[0].IP.String()
	rawConn, err := certDialContextFunc(ctx, "tcp", net.JoinHostPort(approvedIP, "443"))
	if err != nil {
		return nil, fmt.Errorf("dial certificate endpoint: %w", joinErr(errCertFailure, err))
	}
	conn := tls.Client(rawConn, &tls.Config{
		ServerName: domain,
		MinVersion: tls.VersionTLS12, // gosec G402 — refuse TLS 1.0/1.1 handshakes.
	})
	// Bound the handshake too — a peer that accepts TCP but never
	// completes the TLS handshake would otherwise hold the
	// goroutine until the request budget kicks in elsewhere.
	hsCtx, hsCancel := context.WithTimeout(ctx, dialCertTimeout)
	defer hsCancel()
	if err := conn.HandshakeContext(hsCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("tls handshake %s: %w", domain, joinErr(errCertFailure, err))
	}
	defer func() { _ = conn.Close() }()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no peer certs for %s: %w", domain, errCertFailure)
	}
	leaf := state.PeerCertificates[0]
	// CDN detection: if the leaf cert doesn't include the target
	// domain, the customer's DNS is pointing at a CDN. Reject
	// with errCDNCert so the handler maps to 422 CertNotIssued.
	if !sanContains(leaf.DNSNames, domain) {
		return nil, fmt.Errorf("%w: cert SANs=%v", errCDNCert, leaf.DNSNames)
	}
	return leaf, nil
}

// isPublicCertAddress rejects address classes that can identify or reach local
// infrastructure. IPv4-mapped IPv6 is normalized through To4 first, so
// ::ffff:127.0.0.1 cannot evade the IPv4 checks.
func isPublicCertAddress(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, network := range certDeniedNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

var certDeniedNetworks = func() []*net.IPNet {
	// IANA special-purpose ranges beyond the classes covered by net.IP.
	// Documentation and benchmark networks are never valid customer origins.
	cidrs := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "100::/64", "2001::/23", "2001:db8::/32",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		out = append(out, network)
	}
	return out
}()

// dialCert is the production entry point. Routed through
// dialCertFunc so tests can swap a fake. Convenience wrapper for
// the verify + show handlers.
func dialCert(ctx context.Context, domain string) (*x509.Certificate, error) {
	return dialCertFunc(ctx, domain)
}

// sanContains reports whether the cert's SAN list covers domain.
//
// HIGH-1 fix: wildcard SANs (RFC 6125 §6.4.3 dNSName form) match a
// queried host only when the wildcard's base labels are an exact
// parent suffix of the queried host's labels. Two safeguards:
//
//  1. A wildcard SAN is recognised only when the queried host has
//     strictly more labels than the wildcard base. e.g. "a.b.c"
//     matches "*.b.c" (one extra label); "b.c" does NOT match
//     "*.b.c" (RFC 6125 §6.4.3 rule 3 forbids wildcard match on the
//     base itself, and rule 4 forbids partial-label wildcards).
//  2. The wildcard base must be a suffix of the queried host. A cert
//     issued for "*.cloudflare.com" therefore does NOT match a
//     query for "api.example.com" — the suffix is "example.com",
//     not "cloudflare.com".
//
// Together these prevent the prior behaviour where any wildcard SAN
// could match any query.
func sanContains(sans []string, domain string) bool {
	if domain == "" {
		return false
	}
	for _, s := range sans {
		if s == domain {
			return true
		}
		if strings.HasPrefix(s, "*.") {
			base := s[2:]
			if base == "" || strings.Contains(base, "*") {
				// Malformed wildcard SAN — never matches.
				continue
			}
			if !strings.HasSuffix(domain, "."+base) {
				continue
			}
			// The queried host must have exactly one more label
			// than the wildcard base (rule 3: match a single
			// label; rule 4: partial-label wildcards are invalid).
			qLabels := strings.Count(domain, ".")
			bLabels := strings.Count(base, ".")
			if qLabels == bLabels+1 {
				return true
			}
		}
	}
	return false
}

// joinErr composes two errors into a single wrapped error that
// satisfies errors.Is/As for either operand. Required by the
// errorlint lint rule, which forbids `fmt.Errorf("%w ... %v", a, b)`
// where `b` is also an error (only the LAST %w wraps; anything else
// in the chain silently disappears). joinErr keeps both errors
// in the chain via a multi-errors.Unwrap shim.
type wrappedPair struct{ a, b error }

func (w *wrappedPair) Error() string   { return w.a.Error() + ": " + w.b.Error() }
func (w *wrappedPair) Unwrap() []error { return []error{w.a, w.b} }

func joinErr(a, b error) error {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &wrappedPair{a: a, b: b}
}
