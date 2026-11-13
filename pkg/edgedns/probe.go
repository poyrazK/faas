package edgedns

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// ProbeTimeout bounds one readiness probe, shorter than the 2 s tick.
const ProbeTimeout = time.Second

// HTTPProbe asks a compute edge for /readyz on its control port over the
// private network, as faas-check-compute-edges does (ADR-1343 §5).
type HTTPProbe struct {
	Client *http.Client
}

// Ready reports whether url answered 200 within ProbeTimeout.
func (p HTTPProbe) Ready(ctx context.Context, url string) bool {
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// DirectPort is where a client of a DNS-only name connects
// (ADR-20261103163954 §1).
const DirectPort = 443

// TLSProbe connects to a node's public address the way a client of a
// DNS-only name does: TCP 443, the name as SNI, the certificate verified.
// A node's /readyz says its edge is up; only this says the public address
// serves the name. A node rolled back to a release without direct entry
// answers /readyz and has nothing on 443 (2026-11-05, rc.324 on fsn-2).
type TLSProbe struct {
	// Roots verifies the chain; nil uses the system roots, which is what a
	// client trusts.
	Roots *x509.CertPool
	// NameOnly checks the leaf's names and validity but not its chain, for
	// a staging certificate no client trusts anyway.
	NameOnly bool
	// Dial connects; nil uses a net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Serves reports whether addr completed a verified handshake for host
// within ProbeTimeout.
func (p TLSProbe) Serves(ctx context.Context, addr netip.Addr, host string) bool {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	dial := p.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	raw, err := dial(ctx, "tcp", netip.AddrPortFrom(addr, DirectPort).String())
	if err != nil {
		return false
	}
	defer raw.Close() //nolint:errcheck // a probe connection
	cfg := &tls.Config{ServerName: host, RootCAs: p.Roots, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	if p.NameOnly {
		cfg.InsecureSkipVerify = true //nolint:gosec // VerifyConnection checks the name and validity
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("edgedns: no certificate")
			}
			leaf := cs.PeerCertificates[0]
			if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return errors.New("edgedns: certificate outside its validity")
			}
			return leaf.VerifyHostname(host)
		}
	}
	conn := tls.Client(raw, cfg)
	return conn.HandshakeContext(ctx) == nil
}
