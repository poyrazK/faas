// Package main's config — parsed from /etc/faas/gatewayd.toml (or the path
// passed via FAAS_GATEWAYD_CONFIG). Mirrors the vmmd/meterd pattern in
// cmd/<daemon>/config.go so the ansible role can drop a single TOML file on
// disk and operators don't need to fight twelve env vars.
//
// The shape is one flat struct: gatewayd has fewer moving parts than the
// other daemons (no Postgres pool, no JWT) and most knobs flow through to
// gateway.TLSConfig verbatim. We deliberately do not re-export the full
// gateway.TLSConfig — the on-disk surface should be smaller than the
// in-process struct, and gateway.TLSConfig carries a function pointer
// (OnDemandHTTP01Allowlist) that can't survive a TOML round-trip.
package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of the daemon's TOML config.
// A missing file is not an error: LoadConfig returns defaults so the
// e2e harness (which sets TLSConfig.Disabled=true via env) keeps working
// without a config file.
type Config struct {
	// PublicAddr is the bind address for the customer-facing listener.
	// Defaults to ":8080" (the legacy plain-HTTP path). When TLS is enabled
	// via the [tls] table, the public listener moves to ":443" — this
	// field is then ignored unless [tls].disabled is explicitly true.
	PublicAddr string `toml:"public_addr"`

	// ControlAddr is the private /metrics + /healthz listener. Defaults
	// to 127.0.0.1:9090 (loopback only).
	ControlAddr string `toml:"control_addr"`

	// AppsDomain is the platform wildcard host (e.g. "apps.example.com").
	// gatewayd routes <slug>.apps.example.com to the customer's app and
	// applies the apps-suffix host guard. Empty disables wildcard routing
	// (custom-domain-only deployments).
	AppsDomain string `toml:"apps_domain"`

	// APIDLoopback is the in-box URL gatewayd reverse-proxies the apid
	// public surface (/v1/*, /dashboard/*, /oauth/*, /login*,
	// /auth/verify, /logout, /status*, /healthz) to. Defaults to
	// http://127.0.0.1:8081 (apid's loopback bind). Issue #85 widened
	// the proxy surface from /dashboard/* to the full set above.
	APIDLoopback string `toml:"apid_loopback"`

	// GithubdLoopback is the in-box URL gatewayd proxies /webhooks/github
	// to. Defaults to http://127.0.0.1:8083 (githubd's bind).
	GithubdLoopback string `toml:"githubd_loopback"`

	// WebhookSecretPath is the path to the github webhook secret (mode 0400).
	// Empty → read FAAS_GITHUB_WEBHOOK_SECRET from env (legacy path).
	WebhookSecretPath string `toml:"webhook_secret_path"`

	// TLS is the TLS-enabled listener configuration. When Disabled=true
	// the daemon serves plain HTTP on PublicAddr (the e2e harness path).
	// When Disabled=false the public listener binds :443 with certmagic,
	// and gatewayd additionally binds :80 for the ACME mux + redirect.
	TLS TOMLTLSConfig `toml:"tls"`

	// VMMDPingTLS is the mTLS material gatewayd uses to dial vmmd on
	// remote compute nodes (issue #98 / ADR-028, plumbed via issue
	// #120). All three paths empty => no client TLS; all three set =>
	// stdlib default mTLS verification (chain + SAN). Partial cluster
	// is rejected at startup with the vmmd_tls_* field names so an
	// operator can map the error straight to a TOML key. Single-box
	// deployments keep all three empty and continue to dial the unix
	// socket with nil TLS, which wire.DialContext accepts.
	VMMDPingTLSCertPath string `toml:"vmmd_tls_cert_path"`
	VMMDPingTLSKeyPath  string `toml:"vmmd_tls_key_path"`
	VMMDPingTLSCAPath   string `toml:"vmmd_tls_ca_path"`
}

// TOMLTLSConfig is the on-disk TLS subset. Function pointers and derived
// fields (the allowlist) don't survive TOML — we resolve them in
// resolveTLSConfig below.
type TOMLTLSConfig struct {
	Disabled               bool   `toml:"disabled"`
	WildcardCertDomain     string `toml:"wildcard_cert_domain"`
	HetznerDNSAPITokenPath string `toml:"hetzner_dns_api_token_path"`
	HetznerZone            string `toml:"hetzner_zone"`
	StorageDir             string `toml:"storage_dir"`

	// ContactEmail is the email CertMagic registers with the ACME CA for
	// expiry warnings. Default "" is allowed — CertMagic will simply not
	// register one — but production should set it to a monitored address.
	ContactEmail string `toml:"contact_email"`

	// UseStagingCA, when true, switches CertMagic to Let's Encrypt's
	// staging directory. Production must leave this false; the staging CA
	// issues certs browsers reject. Test and metal suites flip it on so a
	// misconfigured DNS delegation doesn't burn the prod rate limit.
	UseStagingCA bool `toml:"use_staging_ca"`
}

// LoadConfig reads path and returns the parsed Config with defaults applied.
// A missing file returns a Config populated with defaults (the legacy env
// path continues to work for the e2e harness).
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		PublicAddr:      ":8080",
		ControlAddr:     "127.0.0.1:9090",
		APIDLoopback:    "http://127.0.0.1:8081",
		GithubdLoopback: "http://127.0.0.1:8083",
		TLS:             TOMLTLSConfig{Disabled: true}, // e2e harness default
	}
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, fmt.Errorf("gatewayd: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("gatewayd: parse %q: %w", path, err)
	}
	return c, nil
}

// resolveTLSConfig lifts the TOML-shaped TLS into the gateway.TLSConfig the
// rest of the daemon consumes, injecting the allowlist from allowlistBuilder
// (which knows about the PG pool). Keeping this transform here (rather than
// in main.go) lets the config_test round-trip the TOML surface without a PG
// pool.
func (c *Config) resolveTLSConfig(allowlist gateway.OnDemandAllowlist) gateway.TLSConfig {
	return gateway.TLSConfig{
		Disabled:                c.TLS.Disabled,
		WildcardCertDomain:      c.TLS.WildcardCertDomain,
		HetznerDNSAPITokenPath:  c.TLS.HetznerDNSAPITokenPath,
		HetznerZone:             c.TLS.HetznerZone,
		StorageDir:              c.TLS.StorageDir,
		ContactEmail:            c.TLS.ContactEmail,
		UseStagingCA:            c.TLS.UseStagingCA,
		OnDemandHTTP01Allowlist: allowlist,
	}
}

// LoadVMMDPingTLS returns the client mTLS config gatewayd uses to dial
// vmmd (issue #98 / ADR-028, plumbed via issue #120). Empty cluster
// returns (nil, nil) — single-box default; wire.DialContext accepts nil
// TLS on unix targets. Partial cluster is rejected at startup with
// the vmmd_tls_* field names (not the generic tls_*) so an operator
// can map the error straight to a TOML key.
//
// Mirrors cmd/schedd/config.go LoadVMMTLS (issue #95). The helper
// goes through pkg/wire so stdlib's default verifier handles
// chain trust + SAN matching + EKU enforcement in a single pass
// — the same path cmd/schedd uses, so the [vmmd_tls] cluster has
// identical semantics on both daemons. Any change to this
// invariant must be reflected on both sides; partial TLS cluster
// is start-up fatal rather than a runtime fault (spec §11).
func (c *Config) LoadVMMDPingTLS() (*tls.Config, error) {
	return wire.LoadClientTLSConfigWithPrefix("vmmd_", c.VMMDPingTLSCertPath, c.VMMDPingTLSKeyPath, c.VMMDPingTLSCAPath)
}
