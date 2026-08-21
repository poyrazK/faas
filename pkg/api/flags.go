// pkg/api/flags.go — env-driven feature flags that gate customer
// surface wiring. The TenantSurfaces flag is the dark-launch switch
// for issue #879 / ADR-100: the schema and the apid routes are
// wired in PR-C, but the routes 404 (or 503) until this flag is
// set, so a misconfigured rollout can be reverted by simply
// unsetting the env var (no migration to undo, no DNS to withdraw).
//
// Pattern mirrors cmd/apid/server.go:189-203 for FAAS_REKEY_ENABLED
// — direct os.Getenv with a stable "1" / "true" / "yes" accept
// set, and a default-off shape. No global mutable state outside
// the accessor function so tests can override with t.Setenv.
package api

import (
	"os"
	"strings"
)

// TenantSurfacesEnabled reports whether the customer surface
// HTTP API is live. Reads FAAS_TENANT_SURFACES_ENABLED at every
// call (not cached at boot) so an operator can flip the env var
// and SIGHUP-restart-free roll out / roll back the surface routes
// without bouncing every daemon. Default off; the cert engine +
// state surface are in place but the HTTP routes + CLI are
// gated until PR-C ships.
func TenantSurfacesEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_TENANT_SURFACES_ENABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// StaticEgressIPEnabled reports whether the per-app static egress
// IP surface (ADR-119) is live. Reads FAAS_STATIC_EGRESS_IP_ENABLED
// at every call (not cached at boot) so an operator can flip the
// env var and SIGHUP-restart-free roll out / roll back without
// bouncing every daemon. Default off; the schema + the apid
// routes + the host renderer are wired but the customer surface
// is gated until the PR ships — same rollout shape as
// TenantSurfacesEnabled above.
func StaticEgressIPEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_STATIC_EGRESS_IP_ENABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// DomainDoctorEnabled reports whether the per-domain doctor probe
// engine is live. Reads FAAS_DOMAIN_DOCTOR_ENABLED at every call
// (mirrors TenantSurfacesEnabled — operator can flip the env var
// and the next dns_poller tick picks it up without a daemon
// bounce). Default ON (post-ADR-120 Tier A3, after the dark-launch
// soak window closed) — the table + probes + endpoint + dashboard
// surface are wired and the dns_poller writes
// domain_doctor_observations rows on every 30 s tick unless the
// operator explicitly opts out. To disable, set the env var to one
// of 0/false/no/off; an empty/unset env var leaves the doctor on.
// The dns_poller (cmd/apid/dns_poller.go::emitDoctorSkip) bumps
// apid_domain_doctor_skipped_flag_disabled_total on every tick
// when the doctor is off so an explicit opt-out surfaces in
// Alertmanager via FaasDomainDoctorDisabledByOperator (info).
func DomainDoctorEnabled() bool {
	return envFlagEnabled("FAAS_DOMAIN_DOCTOR_ENABLED")
}

// envFlagEnabled reads the named env var and reports whether it
// is set to one of the truthy values ("1", "true", "yes", "on",
// case-insensitive). Trims surrounding whitespace before the
// comparison. Centralises the parser so StaticEgressIPEnabled,
// DomainDoctorEnabled, and any future dark-launch flag share the
// same shape; the TenantSurfacesEnabled switch above is the
// historical precedent.
func envFlagEnabled(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// CertEngineWired reports whether the per-host cert engine has
// the env configuration it needs to mint. The engine needs:
//
//   - FAAS_TLS_STORAGE_DIR     — certmagic's leaf storage root
//   - FAAS_TLS_CONTACT_EMAIL   — ACME registration contact
//
// Both are required. FAAS_TLS_DNS_PROVIDER + FAAS_TLS_DNS_TOKEN
// are also required for the DNS-01 solver but those are looked
// up via the DNSProviderFactory seam (per the wildcard TLS path
// in tls_wire.go:138-143). The wrapper at
// pkg/gateway/cert_issuer_tenant_surface.go degrades to
// "cert engine unwired" when this returns false so the visible
// deployment posture stays the same as the dark-launch shape
// (PR-C) until the operator sets both env vars.
func CertEngineWired() bool {
	return strings.TrimSpace(os.Getenv("FAAS_TLS_STORAGE_DIR")) != "" &&
		strings.TrimSpace(os.Getenv("FAAS_TLS_CONTACT_EMAIL")) != ""
}

// CertEngineStorageDir returns the certmagic leaf storage root.
// Returns "" when unset; callers must check CertEngineWired
// before reaching here.
func CertEngineStorageDir() string {
	return strings.TrimSpace(os.Getenv("FAAS_TLS_STORAGE_DIR"))
}

// CertEngineContactEmail returns the ACME account contact.
// Returns "" when unset.
func CertEngineContactEmail() string {
	return strings.TrimSpace(os.Getenv("FAAS_TLS_CONTACT_EMAIL"))
}

// CertEngineStaging reports whether the cert engine should
// drive LE staging instead of production. Staging is the
// default for non-prod envs (the EX44 staging fleet, the
// Lima-metal lab, and any developer box that doesn't have a
// stale prod DNS delegation). Production clusters opt-in to
// the prod CA via FAAS_TLS_STAGING=0 / false.
func CertEngineStaging() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_TLS_STAGING")))
	switch v {
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// DNS provider identifiers (PR-D cert engine). Constants are
// package-scoped so pkg/api/flags_test.go can assert against
// them by symbol — extracting also tames the golangci-lint
// goconst check (the package-wide occurrence counter flags any
// literal used 3+ times).
const (
	DNSProviderHetzner    = "hetzner"
	DNSProviderCloudflare = "cloudflare"
)

// CertEngineDNSProvider returns the DNS provider name. Defaults
// to cloudflare per ADR-024 §6. The token for the provider is
// supplied separately via the operator-sealed DNS token
// (FAAS_TLS_DNS_TOKEN — looked up through the DNSProviderFactory
// seam, not here).
func CertEngineDNSProvider() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_TLS_DNS_PROVIDER"))) {
	case DNSProviderHetzner:
		return DNSProviderHetzner
	}
	return DNSProviderCloudflare
}
