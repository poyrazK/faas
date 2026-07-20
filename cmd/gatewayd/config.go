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
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/onebox-faas/faas/pkg/gateway"
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

	// APIDLoopback is the in-box URL gatewayd proxies /dashboard/* to.
	// Defaults to http://127.0.0.1:8081 (apid's own bind).
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
		OnDemandHTTP01Allowlist: allowlist,
	}
}