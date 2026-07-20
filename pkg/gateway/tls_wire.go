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

// NewCertMagicConfig constructs the wired TLSBundle from the gatewayd
// TOML-shaped TLSConfig. It enforces Validate() invariants: callers should
// already have called TLSConfig.Validate(), but this function re-checks the
// surface it actually consumes (cert magic has no notion of "Disabled") so
// a misconfigured disabled-as-enabled request fails closed.
func NewCertMagicConfig(ctx context.Context, cfg TLSConfig, hetznerToken string, log *slog.Logger) (*TLSBundle, error) {
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
	// not world-readable; the systemd unit runs us as user faas:root so the
	// dir must be owned by root (operator-provisioned via the ansible role).
	cache := certmagic.NewCache(certmagic.CacheOptions{
		// GetConfigForCert left nil — a single Config governs every cert in
		// this process, so certmagic's "find Config for cert" path collapses
		// to "use the only Config". See tls_wire.go package doc.
		Logger: silentZap,
	})

	// ACME contact email: prefer the operator-supplied ContactEmail; fall
	// back to ops@<zone> so production never silently registers empty.
	contactEmail := cfg.ContactEmail
	if contactEmail == "" {
		contactEmail = "ops@" + cfg.HetznerZone
	}

	issuer := &certmagic.ACMEIssuer{
		Email: contactEmail,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: NewHetznerDNSProvider(hetznerToken, cfg.HetznerZone),
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
	}
	// Test and metal suites flip UseStagingCA so a misconfigured DNS
	// delegation doesn't burn the prod rate limit. Production must leave
	// it false — staging certs are not browser-trusted and the browser
	// out-of-the-box warning is annoying.
	if cfg.UseStagingCA {
		issuer.CA = certmagic.LetsEncryptStagingCA
	}

	magic := certmagic.New(cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: cfg.StorageDir},
		Issuers: []certmagic.Issuer{
			issuer,
		},
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: allowlistToDecisionFunc(cfg.OnDemandHTTP01Allowlist, log),
		},
	})

	// Eagerly obtain the wildcard on startup so the first request doesn't
	// pay the 30-60 s DNS-01 propagation cost. We tolerate failure: a
	// transient Hetzner outage shouldn't block the daemon — the wildcard
	// will be obtained lazily on the first request.
	if err := magic.ManageSync(ctx, []string{cfg.WildcardCertDomain}); err != nil {
		log.Warn("gateway: wildcard cert not obtained at startup; will retry on first request",
			"wildcard", cfg.WildcardCertDomain, "err", err)
	}

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