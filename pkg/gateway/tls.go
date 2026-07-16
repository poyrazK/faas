// TLS seam for gatewayd (spec §4.1, §11). The public listener will, in M4,
// terminate TLS via CertMagic with:
//
//   - wildcard *.apps.DOMAIN via DNS-01 (Hetzner DNS API token from
//     /etc/faas/secrets/hetzner-dns.token, sealed at rest per §11/G2)
//   - on-demand HTTP-01 for customer custom_domains, gated by a Postgres
//     lookup against the custom_domains allowlist so an attacker can't
//     trick gatewayd into minting a cert for an unrelated hostname
//   - storage at /var/lib/faas/certs (root:root 0700)
//
// This PR ships the Go shape only — no CertMagic dependency, no DNS-01
// wiring (those need a real apps.DOMAIN + Hetzner token to test). When M4
// lands, cmd/gatewayd reads the TLSConfig from TOML and wraps the
// existing public *http.Server via tls.NewListener.
package gateway

import (
	"crypto/tls"
	"errors"
)

// TLSConfig is the configuration bucket cmd/gatewayd reads from TOML. Empty
// fields mean "TLS is off; serve plain HTTP" — current behavior.
//
// When M4 lands, every field is populated:
//   - WildcardCertDomain: "apps.example.com"
//   - HetznerDNSAPITokenPath: "/etc/faas/secrets/hetzner-dns.token"
//   - HetznerZone: "example.com"
//   - OnDemandHTTP01Allowlist: a func(appID) that returns true when a
//     CUSTOM DOMAIN is verified in the custom_domains table
//   - StorageDir: "/var/lib/faas/certs"
//   - StorageMode: 0700 (root:root)
type TLSConfig struct {
	// Disabled (or all-empty) → plain HTTP; current behavior.
	Disabled bool

	// WildcardCertDomain is the *.apps.DOMAIN suffix that DNS-01 mints
	// the wildcard cert for (M4 only). Example: "apps.example.com".
	WildcardCertDomain string

	// HetznerDNSAPITokenPath is the path to the DNS-01 solver token.
	// Must be readable by root only; the daemon reads it on startup.
	HetznerDNSAPITokenPath string

	// HetznerZone is the DNS zone the wildcard cert is bound to (must
	// match WildcardCertDomain).
	HetznerZone string

	// OnDemandHTTP01Allowlist is consulted by the HTTP-01 solver for each
	// certificate request outside the wildcard. Returning true means the
	// hostname is in the custom_domains table → mint a cert. Returning
	// false means reject (close the cert-mint abuse vector).
	OnDemandHTTP01Allowlist func(host string) bool

	// StorageDir is the CertMagic storage directory. Created root:root 0700
	// if missing.
	StorageDir string

	// ListenAddrs are the bind addresses for the TLS-enabled listeners.
	// Defaults to ":443" + ":80" (the :80 listener is the HTTP-01 solver
	// handler and an M8 redirect to :443 for non-ACME traffic).
	ListenAddrs []string
}

// ErrTLSMisconfigured is returned by Validate when Hetzner fields are
// partially populated — a half-configured TLS path is a worse failure mode
// than no TLS at all.
var ErrTLSMisconfigured = errors.New("gateway: TLS config partial — set Disabled=true or fill all wildcards")

// Validate sanity-checks a TLSConfig. cmd/gatewayd calls this before
// attempting to bind :443.
func (c TLSConfig) Validate() error {
	if c.Disabled {
		return nil
	}
	if c.WildcardCertDomain == "" || c.HetznerDNSAPITokenPath == "" ||
		c.HetznerZone == "" || c.StorageDir == "" {
		return ErrTLSMisconfigured
	}
	return nil
}

// MinTLSVersion is the floor for the TLS handshake — TLS 1.3 since the
// spec is built on Go 1.23+ which has it stable (M4 TLS PR must use
// this constant; do not let any future PR lower it).
const MinTLSVersion = tls.VersionTLS13
