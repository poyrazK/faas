// TLS seam for gatewayd (spec §4.1, §11). The public listener terminates TLS
// via CertMagic with:
//
//   - wildcard *.apps.DOMAIN via DNS-01 (Hetzner DNS API token from
//     /etc/faas/secrets/hetzner-dns.token, sealed at rest per §11/G2)
//   - on-demand HTTP-01 for customer custom_domains, gated by a Postgres
//     lookup against the custom_domains allowlist so an attacker can't
//     trick gatewayd into minting a cert for an unrelated hostname
//   - storage at /var/lib/faas/certs (root:root 0700)
//
// pkg/gateway/tls_wire.go wires the certmagic.Manager; pkg/gateway/dns01_hetzner.go
// implements the DNS-01 solver; pkg/gateway/allowlist.go implements the
// on-demand allowlist; pkg/gateway/acme.go builds the :80 listener mux. cmd/gatewayd
// reads TLSConfig from TOML and decides whether to bind the TLS listeners or fall
// back to plain :8080 (gated on TLSConfig.Disabled).
package gateway

import (
	"crypto/tls"
	"errors"
)

// OnDemandAllowlist is consulted by the HTTP-01 solver for every on-demand
// (custom-domain) certificate request. Returning true means the hostname is in
// the custom_domains table AND its TXT challenge has been satisfied — proceed
// to mint. Returning false means reject: the close-the-cert-mint-abuse-vector
// guard from spec §11. The callback is invoked on the cert-mint goroutine,
// which is rate-limited and cached by certmagic — direct Postgres calls are
// fine, but consider a short-lived cache if the table grows past ~10k rows.
//
// The signature is host-keyed because certmagic presents the request's SNI/Host
// to the decision func — there is no appID context at issuance time.
type OnDemandAllowlist func(host string) bool

// TLSConfig is the configuration bucket cmd/gatewayd reads from TOML. Empty
// fields mean "TLS is off; serve plain HTTP" — the legacy dev/e2e path.
//
// Production:
//
//	Disabled:                false
//	WildcardCertDomain:      "apps.example.com"
//	HetznerDNSAPITokenPath:  "/etc/faas/secrets/hetzner-dns.token"
//	HetznerZone:             "example.com"
//	StorageDir:              "/var/lib/faas/certs"
//	ContactEmail:            "ops@example.com"
//	UseStagingCA:            false
//	OnDemandHTTP01Allowlist: NewPGAllowlist(...) from pkg/gateway/allowlist.go
type TLSConfig struct {
	// Disabled (or all-empty) → plain HTTP; current e2e-harness behavior.
	Disabled bool

	// WildcardCertDomain is the *.apps.DOMAIN suffix that DNS-01 mints
	// the wildcard cert for. Example: "apps.example.com".
	WildcardCertDomain string

	// HetznerDNSAPITokenPath is the path to the DNS-01 solver token.
	// Must be readable by root only; the daemon reads it on startup via
	// loadHetznerDNSToken (cmd/gatewayd/secrets.go), which enforces a 0400
	// perm check mirroring pkg/secretbox.LoadRecipient.
	HetznerDNSAPITokenPath string

	// HetznerZone is the DNS zone the wildcard cert is bound to (must
	// match WildcardCertDomain). The DNS-01 solver writes TXT records into
	// this zone via the Hetzner DNS API.
	HetznerZone string

	// OnDemandHTTP01Allowlist is consulted by the HTTP-01 solver for each
	// certificate request outside the wildcard. Required when Disabled=false;
	// Validate fails closed if nil. See OnDemandAllowlist docs.
	OnDemandHTTP01Allowlist OnDemandAllowlist

	// StorageDir is the CertMagic storage directory. Created root:root 0700
	// if missing.
	StorageDir string

	// ContactEmail is the email CertMagic registers with the ACME CA for
	// expiry warnings. May be empty; production should set a monitored
	// address so a missed renewal isn't a customer's surprise.
	ContactEmail string

	// UseStagingCA, when true, switches CertMagic to Let's Encrypt's
	// staging directory. Production must leave this false — staging certs
	// are not browser-trusted. The metal test suite flips it on so a
	// misconfigured delegation doesn't burn the prod rate limit.
	UseStagingCA bool
}

// ErrTLSMisconfigured is returned by Validate when fields are partially
// populated — a half-configured TLS path is a worse failure mode than no TLS
// at all, because the gateway would serve 200s on plain HTTP from a config
// that LOOKS secure.
var ErrTLSMisconfigured = errors.New("gateway: TLS config partial — set Disabled=true or fill all wildcards + allowlist")

// ErrTLSAllowlistMissing is returned by Validate when TLS is enabled but
// OnDemandHTTP01Allowlist is nil. Without an allowlist, an attacker who can
// reach :80 could mint certs for arbitrary hostnames.
var ErrTLSAllowlistMissing = errors.New("gateway: TLS enabled without OnDemandHTTP01Allowlist — refusing to start (spec §11)")

// Validate sanity-checks a TLSConfig. cmd/gatewayd calls this before
// attempting to bind :443.
//
// Check order matters: we report the more specific error first so an
// operator who only forgot the allowlist doesn't get the misleading
// "partial config" sentinel. The allowlist check is the §11 ship-blocking
// guard — it must surface clearly when it's the actual defect.
func (c TLSConfig) Validate() error {
	if c.Disabled {
		return nil
	}
	if c.OnDemandHTTP01Allowlist == nil {
		return ErrTLSAllowlistMissing
	}
	if c.WildcardCertDomain == "" || c.HetznerDNSAPITokenPath == "" ||
		c.HetznerZone == "" || c.StorageDir == "" {
		return ErrTLSMisconfigured
	}
	return nil
}

// MinTLSVersion is the floor for the TLS handshake — TLS 1.3 since the
// spec is built on Go 1.23+ which has it stable. Do not let any future PR
// lower it; spec §11.
const MinTLSVersion = tls.VersionTLS13
