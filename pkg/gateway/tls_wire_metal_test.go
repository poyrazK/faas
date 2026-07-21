//go:build metal

// Metal-tagged ACME staging smoke tests for gatewayd (spec §4.1, §11). These
// tests dial the real Hetzner DNS API + acme-staging-v02.api.letsencrypt.org
// to mint a wildcard cert and an on-demand custom-domain cert. They are the
// load-bearing evidence that the certmagic wiring works against the live
// services end-to-end — the unit tests in tls_wire_test.go prove the wire
// shape, but only this metal run proves the DNS-01 propagation + ACME
// account registration + cert issuance chain holds.
//
// Operator opt-in:
//
//	export HETZNER_DNS_API_TOKEN=...                  # required, Hetzner DNS API token
//	export FAAS_METAL_TLS_ZONE=example.com            # zone the test mints under
//	export FAAS_METAL_TLS_APPS_DOMAIN=apps.example.com  # wildcard host + zone apex
//	export FAAS_METAL_TLS_CUSTOM_DOMAIN=on-demand.example.com  # for on-demand test
//	export FAAS_RUN_TLS_METAL=1                        # gate (tests skip without it)
//	export FAAS_METAL_TLS_PUBLIC_HTTP_LISTENER=1       # only needed for the on-demand test
//	export FAAS_METAL_TLS_CACHE_DIR=/tmp/faas-metal-tls  # default; reused across runs
//	export FAAS_METAL_TLS_FRESH_ACCOUNT=1             # opt out of cache (fresh ACME account per run)
//
// Skip gates:
//
//   - FAAS_SKIP_METAL_TESTS=1 (matches pkg/fcvm/manager_metal_test.go convention)
//   - HETZNER_DNS_API_TOKEN unset (the test would dial the real API without auth)
//   - FAAS_RUN_TLS_METAL != "1" (operator opt-in — these tests mint real
//     staging certs and consume rate-limit budget)
//   - FAAS_METAL_TLS_PUBLIC_HTTP_LISTENER != "1" for TestMetalCertMagic_OnDemandStaging
//     only (this test triggers an HTTP-01 challenge against the host's public
//     IP; without a running gatewayd on :80 answering /.well-known/acme-challenge,
//     the challenge hangs for 90 s and the test fails. The wildcard mint test
//     uses DNS-01 and does NOT need this gate.)
//
// Cache reuse: the metal tests use a stable StorageDir under
// FAAS_METAL_TLS_CACHE_DIR (default /tmp/faas-metal-tls) so subsequent runs
// reuse the registered ACME account. Without this, every run would burn a
// fresh staging account against the 50-accounts-per-IP-per-3h rate limit.
// Set FAAS_METAL_TLS_FRESH_ACCOUNT=1 to opt out and use t.TempDir instead —
// useful when asserting first-mint behavior or after rotating contact_email.
//
// These tests do NOT run under `make test` (no metal tag). Wire into
// `make test-metal` via the existing target; operator opts in by exporting
// FAAS_RUN_TLS_METAL=1 before running.

package gateway

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

// metalTLSEnv returns the zone + apps-domain + token the metal suite needs.
// Returns t.Skip() if any required env is missing.
func metalTLSEnv(t *testing.T) (token, zone, appsDomain, customDomain string) {
	t.Helper()
	if os.Getenv("FAAS_SKIP_METAL_TESTS") == "1" {
		t.Skip("FAAS_SKIP_METAL_TESTS=1")
	}
	if os.Getenv("FAAS_RUN_TLS_METAL") != "1" {
		t.Skip("set FAAS_RUN_TLS_METAL=1 to opt into the ACME staging smoke tests")
	}
	token = os.Getenv("HETZNER_DNS_API_TOKEN")
	zone = os.Getenv("FAAS_METAL_TLS_ZONE")
	appsDomain = os.Getenv("FAAS_METAL_TLS_APPS_DOMAIN")
	customDomain = os.Getenv("FAAS_METAL_TLS_CUSTOM_DOMAIN")
	if token == "" || zone == "" || appsDomain == "" || customDomain == "" {
		t.Skip("set HETZNER_DNS_API_TOKEN + FAAS_METAL_TLS_{ZONE,APPS_DOMAIN,CUSTOM_DOMAIN} to run the ACME staging smoke tests")
	}
	return token, zone, appsDomain, customDomain
}

// quietMetalLogger routes slog to io.Discard so the test output stays
// readable. certmagic's own INFO chatter goes to its silentZap and won't
// show up here.
func quietMetalLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// metalStorageDir returns a stable cache directory under
// $FAAS_METAL_TLS_CACHE_DIR (default /tmp/faas-metal-tls) unless the
// operator opts into a fresh account per run via FAAS_METAL_TLS_FRESH_ACCOUNT=1.
// Using a stable dir reuses the registered ACME account across runs so we
// don't burn through Let's Encrypt staging's 50-accounts-per-IP-per-3h
// rate limit. When the dir is reused the issued certs are also reused
// (skip the mint on subsequent runs unless the cert is near expiry).
//
// The fresh-account opt-in is the escape hatch for tests that need to
// assert first-mint behavior, or for operators who want to clear stale
// state (e.g. after rotating the contact_email).
func metalStorageDir(t *testing.T) string {
	t.Helper()
	if os.Getenv("FAAS_METAL_TLS_FRESH_ACCOUNT") == "1" {
		t.Log("FAAS_METAL_TLS_FRESH_ACCOUNT=1: using t.TempDir (fresh ACME account per run)")
		return t.TempDir()
	}
	dir := os.Getenv("FAAS_METAL_TLS_CACHE_DIR")
	if dir == "" {
		dir = "/tmp/faas-metal-tls"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create metal TLS cache dir %q: %v", dir, err)
	}
	return dir
}

// TestMetalCertMagic_StagingE2E — mint the wildcard *.apps.example.com cert
// against acme-staging-v02.api.letsencrypt.org. Asserts ManageSync returns
// nil within 90 s — the production timeout the operator runbook budgets for.
//
// Side effects:
//   - Registers an ACME account under the configured contact_email
//   - Writes _acme-challenge TXT records into the configured zone
//   - Stores the issued cert under cfg.StorageDir
//
// The next metal run with the same token reuses the ACME account (cached in
// storage) so we don't accumulate Let's Encrypt staging accounts.
func TestMetalCertMagic_StagingE2E(t *testing.T) {
	token, zone, appsDomain, _ := metalTLSEnv(t)
	storageDir := metalStorageDir(t)
	cfg := TLSConfig{
		Disabled:           false,
		WildcardCertDomain: appsDomain,
		HetznerZone:        zone,
		StorageDir:         storageDir,
		UseStagingCA:       true,
		ContactEmail:       os.Getenv("FAAS_METAL_TLS_CONTACT_EMAIL"), // may be empty → fallback in constructor
		// OnDemandHTTP01Allowlist is required by Validate but the wildcard
		// mint doesn't exercise it. Use StaticAllowlist() so the allowlist
		// gate is satisfied without granting any real domains.
		OnDemandHTTP01Allowlist: StaticAllowlist(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, token, quietMetalLogger(), nil)
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	if bundle.Config == nil {
		t.Fatal("bundle.Config is nil")
	}
	// ManageSync was already called by NewCertMagicConfig (the wildcard is
	// obtained eagerly on startup). Assert the renew-loop is alive by
	// querying the cache: if the wildcard failed to mint, the cache won't
	// have an entry for it. We don't check the exact cert bytes — the unit
	// suite owns cert-shape assertions; here we only assert the end-to-end
	// DNS-01 + ACME chain completed without error.
	if err := bundle.Config.ManageSync(ctx, []string{cfg.WildcardCertDomain}); err != nil {
		t.Errorf("ManageSync returned %v (DNS-01 / ACME chain broken?)", err)
	}
}

// TestMetalCertMagic_OnDemandStaging — mint an on-demand custom-domain cert
// against acme-staging-v02.api.letsencrypt.org, gated by the allowlist. This
// is the second half of the spec §11 closure: the wildcard proves DNS-01
// works; this test proves HTTP-01 (the on-demand path) works.
//
// Side effects:
//   - The first HTTP-01 challenge for customDomain lands on the operator's
//     box at /.well-known/acme-challenge/<token>. The test doesn't run an
//     HTTP listener — it relies on a separate LetsEncrypt validation
//     traffic being routed to the EX44 public IP. Operators must run this
//     only on a host that already answers HTTP-01 challenges for the
//     customDomain (typically: a running gatewayd with the same allowlist).
//   - Stores the issued cert under cfg.StorageDir.
func TestMetalCertMagic_OnDemandStaging(t *testing.T) {
	token, zone, appsDomain, customDomain := metalTLSEnv(t)
	storageDir := metalStorageDir(t)
	// HTTP-01 challenge lands on the host's public IP:80. Without a real
	// gatewayd listener answering /.well-known/acme-challenge for
	// customDomain, the challenge hangs for the full 90 s timeout and the
	// test fails with a misleading context-deadline error. Gate this test
	// separately so operators running the suite on a non-public host only
	// exercise the DNS-01 wildcard path (TestMetalCertMagic_StagingE2E).
	if os.Getenv("FAAS_METAL_TLS_PUBLIC_HTTP_LISTENER") != "1" {
		t.Skip("set FAAS_METAL_TLS_PUBLIC_HTTP_LISTENER=1 to run the HTTP-01 on-demand test (requires a public :80 listener)")
	}
	cfg := TLSConfig{
		Disabled:           false,
		WildcardCertDomain: appsDomain,
		HetznerZone:        zone,
		StorageDir:         storageDir,
		UseStagingCA:       true,
		ContactEmail:       os.Getenv("FAAS_METAL_TLS_CONTACT_EMAIL"),
		// Permit the custom-domain so the on-demand allowlist doesn't deny
		// the mint. In production this is custom_domains verified via the
		// Postgres allowlist (NewPGAllowlist); for the metal smoke test we
		// grant only the test's custom domain.
		OnDemandHTTP01Allowlist: StaticAllowlist(customDomain),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, token, quietMetalLogger(), nil)
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	// Trigger the on-demand path explicitly so we don't wait for first
	// request to land. ObtainCertSync will block until the challenge is
	// satisfied (operator must have the HTTP-01 listener answering for
	// customDomain on port 80) or fail with a context-deadline error.
	if err := bundle.Config.ObtainCertSync(ctx, customDomain); err != nil {
		t.Errorf("ObtainCertSync(%q) returned %v (HTTP-01 chain broken?)", customDomain, err)
	}
}
