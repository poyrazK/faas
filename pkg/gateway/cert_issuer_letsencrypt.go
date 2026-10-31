// pkg/gateway/cert_issuer_letsencrypt.go — the production
// certmagic-based issuer for the ADR-100 tenant-surface cert
// engine (PR-D cert-engine-real-mint).
//
// Surface-level state machine lives in
// cert_issuer_tenant_surface.go (the wrapper that loads the
// surface + verified hostnames, validates inputs, writes back
// the cert_state transition). This file is the CA-side shell —
// it owns the certmagic.Config + per-host cache key and the
// Obtain → write-to-disk → parse-not-after flow.
//
// One LetsEncryptCertIssuer per daemon process. The certmagic
// Cache is keyed on the primary hostname — different from the
// wildcard Config in tls_wire.go because the wildcard uses
// the zone as the key.
//
// San-set semantics (PR-D: deviates from the ADR-100
// "per_host_san bundles up to 100 hostnames as SANs against
// one LE order" wording):
//
//	certmagic v0.25.4's public Obtain path only generates a
//	CSR for a single `name` per call (config.go:640,
//	generateCSR(privKey, []string{name}, false)). It does not
//	support multi-SAN CSRs in the synchronous ObtainCertSync
//	path — multi-SAN orders are only reachable through the
//	on-demand TLS-handshake code path, which is the wrong
//	seam for our background "remint on pg_notify" engine.
//
//	PR-D mints one cert per hostname. The cert_kind value
//	"per_host_san" is kept for schema forward compat with
//	ADR-100, but the implementation is per-host — each verified
//	hostname gets its own LE cert. Per-host certs are
//	individually renewable via the renewer (cert_renewer.go)
//	and individually observable via the gateway_tls_cert_expiry
//	_by_host_seconds{kind="per_host_san"} gauge. A future
//	ADR-114 wires the multi-SAN bundling once certmagic
//	exposes a synchronous multi-SAN Obtain API.
//
// LE hard cap (MaxSANPerCert=100) is enforced in the wrapper
// before reaching the issuer; the per-host path doesn't need
// it but the cap stays in pkg/api/limits.go so a future SAN
// bundler is bounded.
//
// CA dispatch: production = LetsEncryptProductionCA,
// staging = LetsEncryptStagingCA. Tests pin staging so a
// misconfigured DNS delegation never burns the prod rate
// limit.
//
// Errors are NOT terminal: the next pg_notify retry
// re-invokes RequestCertForSurface. The notify subscriber
// in cmd/gatewayd-internal/backend.go::handleInvalidation
// logs and swallows so a transient CA outage can't block the
// edge.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
)

// LetsEncryptCertIssuer is the production CA wrapper. It owns a
// single certmagic.Config that drives the per-host Obtain path.
// certmagic requires a certificate cache for every Config, but this
// issuer never loads certificates into it: the per-host engine mints
// one cert per hostname and writes each leaf to its own directory,
// and the renewer goroutine drives renewal through Issue, so the
// cache's own maintenance loop has nothing to renew.
type LetsEncryptCertIssuer struct {
	// storageDir is the parent directory; certmagic writes to
	// <storageDir>/certificates/<issuerKey>/<primary>/. Mode 0700
	// is enforced by ensureStorageDir at boot — the issuer
	// assumes the dir already exists with the right perms.
	storageDir string
	// staging toggles LE staging vs production. Tests force
	// staging so a misconfigured DNS delegation doesn't burn
	// the prod rate limit.
	staging bool
	// contactEmail is the ACME account contact (per RFC 8555).
	// Required by LE; an empty string panics inside
	// certmagic.NewACMEIssuer.
	contactEmail string
	// dnsProvider is the libdns adapter for the DNS-01 solver.
	// nil-safe: a nil provider short-circuits the certmagic
	// on-demand path. PR-D uses DNS-01 because HTTP-01 can't
	// cover per-host SAN certs (no per-host :80 listener).
	dnsProvider certmagic.DNSProvider
	// log is the per-daemon slog. nil defaults to slog.Default().
	log *slog.Logger
	// initOnce guards the lazy Config construction. The
	// Config can be built on first Issue so the dnsProvider
	// can be wrapped by the issuer constructor without
	// re-entering initCache from the test seam.
	initOnce sync.Once
	cfg      *certmagic.Config
	// initErr caches any error from initConfig so Issue can
	// surface it without panicking.
	initErr error
}

// NewLetsEncryptCertIssuer constructs a production issuer.
// storageDir MUST be writable by the daemon (faas:faas, mode 0700).
// staging=true pins the CA to LE staging (used by tests + the
// cluster's pre-prod validation flow).
func NewLetsEncryptCertIssuer(storageDir, contactEmail string, staging bool, dns certmagic.DNSProvider, log *slog.Logger) (*LetsEncryptCertIssuer, error) {
	if storageDir == "" {
		return nil, errors.New("gateway: empty storage dir")
	}
	if log == nil {
		log = slog.Default()
	}
	return &LetsEncryptCertIssuer{
		storageDir:   storageDir,
		staging:      staging,
		contactEmail: contactEmail,
		dnsProvider:  dns,
		log:          log,
	}, nil
}

// initConfig lazily builds the certmagic Config on first Issue.
//
// Chicken-and-egg dance (load-bearing — code review caught a
// nil-cfg panic in the v1 version): certmagic.NewACMEIssuer
// takes a non-nil *Config (acmeissuer.go:188 panics on nil)
// AND the Config struct itself holds the Issuer list. The
// accepted seam is to build the Config in two passes:
//
//  1. Build the *Config with Issuers set to nil so the
//     d := &cfg in NewACMEIssuer sees a populated magic.
//  2. NewACMEIssuer(&cfg, template) — magic is now a real
//     *Config, not nil.
//  3. Assign magic.Issuers = []Issuer{issuer} post-construction.
//
// This is the same pattern certmagic uses internally at
// config.go:250-256 (Default.Issuers fallback). The TLS wildcard
// path uses certmagic.NewCache with a GetConfigForCert closure
// (tls_wire.go:138-143) — a heavier indirection that the
// per-host issuer does not need because each cert has its own
// (issuer-key, primary-host) storage path.
func (l *LetsEncryptCertIssuer) initConfig() error {
	l.initOnce.Do(func() {
		// certmagic prefixes its own "certificates/" key, so the
		// storage root is storageDir itself; rooting it at
		// storageDir/certificates wrote every leaf one level below
		// where leafPath and the expiry refresher read it.
		storage := &certmagic.FileStorage{Path: l.storageDir}
		ca := certmagic.LetsEncryptProductionCA
		if l.staging {
			ca = certmagic.LetsEncryptStagingCA
		}
		// certmagic.New panics on a nil cache, so the first
		// issuance would take gatewayd-internal down.
		var magic *certmagic.Config
		cache := certmagic.NewCache(certmagic.CacheOptions{
			GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return magic, nil },
			Logger:           silentZap,
		})
		magic = certmagic.New(cache, certmagic.Config{
			Storage: storage,
			Logger:  silentZap,
		})
		issuer := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
			Email:       l.contactEmail,
			CA:          ca,
			Agreed:      true,
			Logger:      silentZap,
			DNS01Solver: &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: l.dnsProvider}},
		})
		magic.Issuers = []certmagic.Issuer{issuer}
		l.cfg = magic
	})
	return l.initErr
}

// Issue mints a certificate covering `primary` (one cert per
// hostname — see the file header for the SAN-set rationale).
// Returns the leaf's NotAfter so the caller can write
// cert_not_after back to the surface row. The caller
// (TenantSurfaceCertIssuer) is responsible for the input
// validation — this function trusts the inputs because they're
// already verified by the wrapper.
//
// The MaxSANPerCert cap is enforced in the wrapper, not here —
// this function is single-name only. A future SAN-bundling
// ADR-114 adds the cap check inside Issue when the API
// supports it.
func (l *LetsEncryptCertIssuer) Issue(ctx context.Context, primary string) (time.Time, error) {
	if primary == "" {
		return time.Time{}, errors.New("gateway: empty primary hostname")
	}
	if err := l.initConfig(); err != nil {
		return time.Time{}, err
	}
	// cfg.ObtainCertSync is the synchronous obtain path: it
	// blocks until the cert is in storage, generates the CSR
	// for `primary`, drives the ACME order via the DNS-01
	// solver, and stows the cert. It does NOT return the
	// IssuedCertificate — we re-parse the on-disk leaf to
	// recover NotAfter.
	if err := l.cfg.ObtainCertSync(ctx, primary); err != nil {
		return time.Time{}, fmt.Errorf("gateway: certmagic ObtainCertSync %s: %w", primary, err)
	}
	// ObtainCertSync is a no-op whenever storage already holds a
	// certificate for primary, expiring or not, so on its own the
	// renewer's re-mint would restamp the old NotAfter until the
	// certificate lapsed. RenewCertSync replaces it only inside
	// certmagic's renewal window (a third of its lifetime by
	// default); otherwise it returns without contacting the CA.
	if err := l.cfg.RenewCertSync(ctx, primary, false); err != nil {
		return time.Time{}, fmt.Errorf("gateway: certmagic RenewCertSync %s: %w", primary, err)
	}
	// Certmagic writes the leaf to
	// <storageDir>/certificates/<issuerKey>/<primary>/<primary>.crt.
	// Parse the freshly-written file and return the NotAfter
	// timestamp so the caller can stamp cert_not_after on the
	// surface row. parseCertNotAfter is the same helper the
	// cert-expiry refresher uses (cert_expiry.go:481).
	leafPath, err := l.leafPath(primary)
	if err != nil {
		return time.Time{}, fmt.Errorf("gateway: leaf path for %s: %w", primary, err)
	}
	notAfter, err := parseCertNotAfter(leafPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("gateway: parse freshly-issued leaf at %s: %w", leafPath, err)
	}
	return notAfter, nil
}

// IssueSet mints per-host certs for every hostname in primarySANS
// and returns the soonest NotAfter across the issued set. The
// returned time is what the surface's cert_not_after is stamped
// to — the surface-level expiry tracks the soonest cert in the
// set so a renewer tick that hits the threshold re-mints the
// whole set.
//
// Fail-fast semantics: the first cert to fail aborts IssueSet
// and returns the partial failure. The caller (the
// TenantSurfaceCertIssuer wrapper) marks the surface
// cert_state=failed and the renewer re-mints on the next tick
// — there's no partial-success state because a customer-facing
// dashboard rendering "issued" against a half-minted set is
// worse than "failed".
func (l *LetsEncryptCertIssuer) IssueSet(ctx context.Context, primarySANS []string) (time.Time, error) {
	if len(primarySANS) == 0 {
		return time.Time{}, errors.New("gateway: empty hostname set")
	}
	soonest := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range primarySANS {
		na, err := l.Issue(ctx, name)
		if err != nil {
			return time.Time{}, fmt.Errorf("gateway: IssueSet partial-failure on %s: %w", name, err)
		}
		if na.Before(soonest) {
			soonest = na
		}
	}
	return soonest, nil
}

// leafPath returns the on-disk path certmagic writes the leaf
// cert to. Certmagic's FileStorage layout is
// <StorageDir>/certificates/<issuerKey>/<domain>/<domain>.crt
// (storage.go:357 prefixCerts = "certificates"). The issuerKey
// is derived from the CA directory URL, so production and
// staging certificates live under different keys.
//
// parseCertNotAfter (cert_expiry.go:481) accepts the
// "first PEM block = leaf" invariant — the standard
// FileStorage layout has leaf first.
func (l *LetsEncryptCertIssuer) leafPath(primary string) (string, error) {
	if primary == "" {
		return "", errors.New("gateway: empty primary hostname for leaf path")
	}
	return filepath.Join(l.storageDir, "certificates", l.issuerKey(), primary, primary+".crt"), nil
}

// issuerKey is the storage key of the configured CA: the ACME issuer's own
// key once the Config is built, else the key certmagic derives for the
// Let's Encrypt directory this issuer targets.
func (l *LetsEncryptCertIssuer) issuerKey() string {
	if l.cfg != nil && len(l.cfg.Issuers) > 0 {
		return l.cfg.Issuers[0].IssuerKey()
	}
	if l.staging {
		return "acme-staging-v02.api.letsencrypt.org-directory"
	}
	return "acme-v02.api.letsencrypt.org-directory"
}
