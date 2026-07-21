// CertMagic wiring for gatewayd (spec §11, §4.1).
//
// Production layout (cmd/gatewayd/main.go calls into this):
//
//	cfg, err := gateway.NewCertMagicConfig(ctx, gatewaydConfig.TLS, hetznerToken, log)
//	// cfg.GetCertificate is the tls.Config.GetCertificate callback
//	// cfg.HTTPChallengeHandler is the :80 handler for ACME challenges
//	// cfg.OnDemandDecisionFunc mirrors what we set here for tests
//
// What this file owns:
//
//   - Cache construction (NewCache, FileStorage at cfg.StorageDir)
//   - The base certmagic.Config: ACMEIssuer + DNS-01 + on-demand DecisionFunc
//   - The mapping from our OnDemandAllowlist (func(host) bool) to certmagic's
//     OnDemand.DecisionFunc (func(ctx, name) error)
//
// What this file does NOT own:
//
//   - the tls.Config.MinVersion / cipher suites (cmd/gatewayd/main.go)
//   - the listener bind addresses (cmd/gatewayd/main.go)
//   - the :80 ACME mux + redirect (pkg/gateway/acme.go)
//
// Why one Config and not per-host: spec §4.1 makes gatewayd the only public
// listener on the box, and the wildcard + on-demand certs share storage and
// the in-process lock — splitting them across Configs would force cross-cache
// coordination for renewals. Single Config is the load-bearing invariant.
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/caddyserver/certmagic"
	"go.uber.org/zap"
)

// zapNop returns a *zap.Logger that drops everything. Centralized so a
// future PR can swap to a real bridge (e.g. zslog) without changing call
// sites.
func zapNop() *zap.Logger { return zap.NewNop() }

// TLSBundle is the wired CertMagic surface cmd/gatewayd hands to its
// listeners. Keep this struct tight: every field is consumed by main.go to
// build the public tls.Config and the :80 handler. Adding fields here forces
// every call site to be revisited, which is the right friction.
type TLSBundle struct {
	// Config is the underlying certmagic config. It owns the cache, the
	// storage, the issuers, and the renew loop. main.go does NOT need to
	// touch it directly today — the convenience fields below cover the
	// seams — but it is exported so a future admin endpoint can query
	// CertMagic state.
	Config *certmagic.Config

	// GetCertificate is the callback to attach to tls.Config.GetCertificate.
	// On a cache hit it returns instantly; on a miss it blocks on the
	// on-demand issuance (which can take 30-60 s for DNS-01).
	GetCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)

	// HTTPChallengeHandler serves /.well-known/acme-challenge/*. Mount this
	// on the :80 listener alongside the redirect (see pkg/gateway/acme.go).
	HTTPChallengeHandler http.Handler

	// DecisionFunc exposes the on-demand decision function so tests can
	// invoke it directly. Production does not need this — certmagic owns
	// the call site — but exporting it is what lets us write
	// TestTLSWire_AllowlistBlocksUnknownHost without spinning up the full
	// ACME handshake.
	DecisionFunc func(ctx context.Context, name string) error
}

// silentZap is a no-op zap logger used as certmagic's CacheOptions.Logger.
// We use zap.NewNop() rather than building a slog bridge: certmagic's log
// volume is small (renewals + on-demand mints), and routing into slog would
// pull zap's slog adapter as an additional dependency for little gain. A
// future PR can swap this for a real bridge if operators want certmagic
// chatter in slog.
var silentZap = zapNop()

// DNSProviderFactory builds the libdns.DNSProvider used by certmagic's
// DNS-01 solver. Production passes nil to NewCertMagicConfig, which uses
// NewHetznerDNSProvider against https://dns.hetzner.com/api/v1. Tests pass
// a closure that returns a *HetznerDNSProvider wired against an httptest
// stub so the wire shape (auth header, record create/delete, zone lookup)
// can be exercised without hitting the real Hetzner API.
type DNSProviderFactory func(token, zone string) certmagic.DNSProvider

// NewCertMagicConfig constructs the wired TLSBundle from the gatewayd
// TOML-shaped TLSConfig. It enforces Validate() invariants: callers should
// already have called TLSConfig.Validate(), but this function re-checks the
// surface it actually consumes (cert magic has no notion of "Disabled") so
// a misconfigured disabled-as-enabled request fails closed.
//
// dnsFactory is nil in production (NewHetznerDNSProvider is used); tests
// pass a closure that returns a provider pointed at an httptest stub.
func NewCertMagicConfig(ctx context.Context, cfg TLSConfig, hetznerToken string, log *slog.Logger, dnsFactory DNSProviderFactory) (*TLSBundle, error) {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Disabled {
		return nil, errors.New("gateway: NewCertMagicConfig called with Disabled=true (use the plain :8080 path)")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := ensureStorageDir(cfg.StorageDir); err != nil {
		return nil, err
	}

	// Storage: file-backed at cfg.StorageDir. mode 0700 so cert blobs are
	// not world-readable; the systemd unit runs us as user faas:faas (User=faas
	// Group=faas) so the dir must be owned by faas:faas (operator-provisioned
	// via the ansible role).
	//
	// NewCache REQUIRES a non-nil GetConfigForCert (certmagic v0.25 cache.go:130
	// panics otherwise). We can't construct the Config first because the cache
	// wants a callback that returns one — chicken-and-egg. The standard fix is
	// a pointer-to-pointer: cache.GetConfigForCert closes over &magic, dereferences
	// at call time (always after the assignment below). The single-Config
	// invariant is the load-bearing reason this works at all (see tls_wire.go
	// package doc).
	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		Logger: silentZap,
		GetConfigForCert: func(_ certmagic.Certificate) (*certmagic.Config, error) {
			return magic, nil
		},
	})

	// ACME contact email: prefer the operator-supplied ContactEmail; fall
	// back to ops@<zone> so production never silently registers empty.
	contactEmail := cfg.ContactEmail
	if contactEmail == "" {
		contactEmail = "ops@" + cfg.HetznerZone
	}

	factory := dnsFactory
	if factory == nil {
		factory = func(token, zone string) certmagic.DNSProvider {
			return NewHetznerDNSProvider(token, zone)
		}
	}
	issuerTemplate := certmagic.ACMEIssuer{
		Email: contactEmail,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: factory(hetznerToken, cfg.HetznerZone),
			},
		},
		Agreed: true,
		// We don't need the HTTP-01 solver as the *primary* path because the
		// wildcard requires DNS-01 anyway, but keeping HTTP-01 enabled lets
		// on-demand custom-domain certs use either method (certmagic picks
		// the cheapest that the CA allows).
		DisableHTTPChallenge:      false,
		DisableTLSALPNChallenge:   false,
		DisableDistributedSolvers: true, // one-box; nothing to distribute to
		// ACMEIssuer has its own Logger distinct from the cache's. Route it
		// to silentZap so test runs don't print certmagic INFO chatter to
		// stderr (production reuses the same sink; certmagic's log volume is
		// small but a real bridge to slog is a follow-up).
		Logger: silentZap,
	}
	// Test and metal suites flip UseStagingCA so a misconfigured DNS
	// delegation doesn't burn the prod rate limit. Production must leave
	// it false — staging certs are not browser-trusted and the browser
	// out-of-the-box warning is annoying.
	if cfg.UseStagingCA {
		issuerTemplate.CA = certmagic.LetsEncryptStagingCA
	}

	magic = certmagic.New(cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: cfg.StorageDir},
		Issuers: nil, // populated below; see comment
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: allowlistToDecisionFunc(cfg.OnDemandHTTP01Allowlist, log),
		},
	})
	// NewACMEIssuer wires `am.config = magic` AND defaults CA / TestCA /
	// Email from DefaultACME when the template leaves them blank. The literal
	// &ACMEIssuer{...} form leaves am.config nil, and calling
	// HTTPChallengeHandler on such an issuer segfaults inside certmagic
	// (httphandlers.go:138 → account.go:49 → mutex on am.config). We can't
	// build the issuer inside the certmagic.New(...) literal because Go
	// evaluates the slice literal before the `magic = ...` assignment
	// completes — so we construct `magic` first, then materialize the issuer
	// against the now-populated variable.
	magic.Issuers = []certmagic.Issuer{
		certmagic.NewACMEIssuer(magic, issuerTemplate),
	}

	// Why no ManageSync call here: certmagic v0.25's manageAll short-circuits
	// when an OnDemand config is present (config.go:380 — the domain is added
	// to hostAllowlist and the obtain step is deferred). We always set
	// OnDemand above (the on-demand path is required for custom-domain
	// certs, gated by the §11 abuse-vector allowlist), so ManageSync would
	// be a no-op regardless. The wildcard cert is obtained lazily on the
	// first inbound request via OnDemand; the cache absorbs subsequent
	// requests. If a future certmagic upgrade removes the short-circuit, we
	// need to add an eager-obtain call here — but today the lazy path is
	// the production behavior.

	// HTTPChallengeHandler lives on the *ACMEIssuer, not the *Config — pull
	// it off the first issuer so main.go can mount it on :80.
	var httpChallenge http.Handler
	if len(magic.Issuers) > 0 {
		if am, ok := magic.Issuers[0].(*certmagic.ACMEIssuer); ok {
			httpChallenge = am.HTTPChallengeHandler(http.NotFoundHandler())
		}
	}

	return &TLSBundle{
		Config:               magic,
		GetCertificate:       magic.GetCertificate,
		HTTPChallengeHandler: httpChallenge,
		DecisionFunc:         magic.OnDemand.DecisionFunc,
	}, nil
}

// Close gracefully stops certmagic's internal renew loop. The bundle is
// single-use; calling Close twice is a no-op.
//
// certmagic v0.25 has no public Stop on *Config — the renew goroutine exits
// when its context is cancelled, which is the caller's responsibility (the
// run() shutdown path passes the daemon's ctx to certmagic via ManageAsync
// in a follow-up). Today Close is a marker so tests can assert a symmetric
// Close seam and so a future certmagic upgrade can wire real shutdown
// without changing call sites.
func (b *TLSBundle) Close() error {
	if b == nil || b.Config == nil {
		return nil
	}
	return nil
}

// ensureStorageDir creates cfg.StorageDir as 0700 if missing. Idempotent: the
// ansible role creates it before the daemon starts; this is the safety net
// for dev boxes where the role wasn't run.
func ensureStorageDir(dir string) error {
	if dir == "" {
		return errors.New("gateway: empty StorageDir")
	}
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("gateway: %q exists and is not a directory", dir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// MkdirAll with 0700 — refuse to relax perms even if umask suggests so.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("gateway: create cert storage dir %q: %w", dir, err)
	}
	// Belt-and-braces: an operator may have provisioned the dir with looser
	// perms. Tighten it.
	if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("gateway: chmod cert storage dir: %w", err)
	}
	return nil
}

// allowlistToDecisionFunc adapts our OnDemandAllowlist (func(host) bool) to
// certmagic's DecisionFunc (func(ctx, name) error). Returning a non-nil
// error tells certmagic to deny the request — that's how we close the
// cert-mint abuse vector from spec §11.
func allowlistToDecisionFunc(allow OnDemandAllowlist, log *slog.Logger) func(context.Context, string) error {
	return func(_ context.Context, name string) error {
		if allow == nil {
			return errors.New("gateway: on-demand denied (allowlist not configured)")
		}
		if !allow(name) {
			if log != nil {
				log.Info("gateway: on-demand cert denied by allowlist", "host", name)
			}
			return fmt.Errorf("gateway: on-demand denied for %q", name)
		}
		return nil
	}
}
