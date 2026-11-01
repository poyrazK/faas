// OIDC token verifier — the JWKS-aware adapter that converts an
// IdP-issued JWT into the claim set pkg/oidc needs. Mirrors the
// cmd/gatewayd-internal/edge_rules_jwks.go::edgeJWKSAdapter shape
// (per-process globally-shared Cache + Verifier, lazy-Register on
// first sight) but is a leaf package here so the adapter lives
// alongside the rest of pkg/oidc.
//
// Why a leaf-package adapter and not a cmd-side one: pkg/oidc wants
// to be testable in isolation (pkg/oidc/handler_test.go stamps the
// adapter into the HTTP handler with a fake Cache), and importing
// pkg/oidc from cmd/apid already carries the dep — there's no reason
// to push the adapter up.
//
// The cache is constructed per Verifier instance (one per cmd/apid
// process), shared across all exchange requests. Each trust policy
// gets its own JWKS URL keyed entry in the cache; cold paths pay one
// fetch per URL, hot paths hit the cached set.
package oidc

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/edgejwks"
)

// Verifier validates IdP-issued JWTs against the per-policy trust
// rule. Returned Claims is the subset pkg/oidc stores on
// ExchangedToken + emits in the auth.token.exchanged audit row.
type Verifier interface {
	// Verify parses rawToken against the trust policy and returns the
	// standard claim subset. Sentinel errors map 1:1 to edgejwks
	// (ErrJWTMissingToken / ErrJWTBadSignature / ErrJWTExpired /
	// ErrJWTWrongIssuer / ErrJWTWrongAudience / ErrJWTMissingClaim).
	Verify(ctx context.Context, rawToken string, p *OIDCTrustPolicy) (*Claims, error)
}

// Claims is the parsed subset pkg/oidc needs from the IdP JWT.
// Subject / Issuer / Audience are the audit row inputs; Exp is the
// upper bound for the exchanged bearer TTL (we use min(exp, now +
// OIDCBearerTTL) so a near-expiry IdP token doesn't outlive its
// origin).
type Claims struct {
	Subject string
	Issuer  string
	Aud     []string
	Exp     time.Time
	JTI     string // "" if the IdP omits the jti claim
	// Ref and EventName are GitHub Actions' ref and event_name claims
	// ("" from other issuers); the exchange decides from them what a
	// workflow may deploy (ADR-20261101100000).
	Ref       string
	EventName string
}

// edgeJWKSVerifier is the production impl. Composition over
// pkg/edgejwks keeps the jose import + JWKS cache in the leaf
// package — pkg/oidc never imports go-jose directly.
type edgeJWKSVerifier struct {
	cache edgejwks.Cache
	v     edgejwks.Verifier
	log   *slog.Logger
}

// NewVerifier constructs a production verifier. The cache uses
// http.DefaultClient because the JWKS endpoint is a public URL (no
// auth, no proxy); a future PR can swap the client if the platform
// adds egress proxying. OnFetchErr is wired to slog+audit JWKS fetch
// failures without coupling edgejwks to slog (the cmd-side owns the
// logger; pkg/oidc takes a non-nil logger so the Warn path runs).
func NewVerifier(log *slog.Logger) Verifier {
	cache := edgejwks.NewCache(edgejwks.Options{
		HTTPClient: &http.Client{Timeout: edgejwks.DefaultFetchTimeout},
		OnFetchErr: func(rawURL string, err error) {
			if log != nil {
				log.Warn("oidc: jwks fetch failed",
					"jwks_url", rawURL,
					"err", err.Error())
			}
		},
	})
	return &edgeJWKSVerifier{
		cache: cache,
		v:     edgejwks.NewVerifier(cache, edgejwks.DefaultSkew),
		log:   log,
	}
}

// Verify is the Verifier shape. Lazy-Register the JWKS URL on first
// sight so a cold cache doesn't fail the request — it just costs one
// extra fetch round-trip (same posture as edge_rules_jwks.go:67).
func (a *edgeJWKSVerifier) Verify(ctx context.Context, rawToken string, p *OIDCTrustPolicy) (*Claims, error) {
	if p == nil {
		return nil, fmt.Errorf("oidc: nil trust policy")
	}
	if _, ok, _ := a.cache.Get(ctx, p.JWKSURL, ""); !ok {
		if err := a.cache.Register(p.JWKSURL); err != nil {
			return nil, err
		}
	}
	src, err := a.v.Verify(ctx, rawToken, edgejwks.VerifierRule{
		JWKSURL:               p.JWKSURL,
		Issuer:                p.IssuerURL,
		Audience:              p.Audience,
		Algorithms:            p.Algorithms,
		RequiredClaims:        p.RequiredClaims,
		RequiredClaimPatterns: subjectPatternAsClaim(p.SubjectPattern),
		ExtractClaims:         []string{"ref", "event_name"},
	})
	if err != nil {
		return nil, err
	}
	return &Claims{
		Subject:   src.Subject,
		Issuer:    src.Issuer,
		Aud:       src.Aud,
		Exp:       src.Exp,
		Ref:       src.Custom["ref"],
		EventName: src.Custom["event_name"],
		// JTI is extracted at the caller from the raw payload because
		// pkg/edgejwks.Claims doesn't carry it — the standard claim
		// is non-required and most IdPs omit it. The handler decodes
		// the payload a second time (cheap; the JWS is already verified).
	}, nil
}

// subjectPatternAsClaim projects the trust policy's subject_pattern
// into the edgejwks VerifierRule's RequiredClaimPatterns map so the
// existing loop (verifier.go:193+) handles the regex match. An empty
// pattern skips the check (matches the "permissive default" auto-
// create policy where every CI job for this account is admitted).
func subjectPatternAsClaim(pattern string) map[string]string {
	if pattern == "" {
		return nil
	}
	return map[string]string{"sub": pattern}
}
