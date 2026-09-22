// JWT verifier wiring for gatewayd-internal (ADR-091 / issue #561,
// PR 5). Constructs the production pkg/edgejwks.Cache + Verifier
// and adapts them to the narrow pkg/gateway.JWTVerifier interface
// that handler.go::applyEdgeRuleJWT consumes. The adapter is what
// keeps the dep direction one-way: pkg/gateway never imports
// pkg/edgejwks; this file is the only place that does, on the
// cmd-side.
//
// The Cache is per-host scoped via the matcher's compile-time
// guarantee that MatchJWT only returns rules whose JWKSURL was
// already validated by apid-Validate (https:// + not
// private/loopback/link-local). A first-time request for an
// unknown URL triggers Register(ctx, url) lazily, so a cold cache
// doesn't fail a request — it just delays the first verify by one
// JWKS fetch round-trip.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/edgejwks"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// edgeJWKSAdapter adapts pkg/edgejwks.Verifier to
// pkg/gateway.JWTVerifier. cmd-side is the seam; this is the
// only place that knows both packages.
type edgeJWKSAdapter struct {
	cache edgejwks.Cache
	v     edgejwks.Verifier
	log   *slog.Logger
}

// newEdgeJWKSAdapter constructs the production JWKS adapter with
// the standard 5-minute refresh interval + 5-second fetch timeout.
// The cache uses http.DefaultClient because the JWKS endpoint is
// a public URL (no auth, no proxy); cmd/gatewayd-internal/main.go
// can swap the client if a future PR adds egress proxying.
func newEdgeJWKSAdapter(log *slog.Logger) *edgeJWKSAdapter {
	cache := edgejwks.NewCache(edgejwks.Options{
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		OnFetchErr: func(rawURL string, err error) {
			if log != nil {
				log.Warn("edgejwks: jwks fetch failed",
					"jwks_url", rawURL,
					"err", err.Error())
			}
		},
	})
	return &edgeJWKSAdapter{
		cache: cache,
		v:     edgejwks.NewVerifier(cache, edgejwks.DefaultSkew),
		log:   log,
	}
}

// Verify is the JWTVerifier shape. We lazy-Register the URL on
// first sight so a cold cache doesn't fail the request — it just
// costs one extra fetch.
func (a *edgeJWKSAdapter) Verify(ctx context.Context, rawToken string, rule *gateway.EdgeRuleJWTResolved) (*gateway.JWTClaims, error) {
	if rule == nil {
		return nil, edgejwks.ErrJWKSNotRegistered
	}
	if _, ok, _ := a.cache.Get(ctx, rule.JWKSURL, ""); !ok {
		if err := a.cache.Register(rule.JWKSURL); err != nil {
			return nil, err
		}
	}
	src, err := a.v.Verify(ctx, rawToken, edgejwks.VerifierRule{
		JWKSURL:        rule.JWKSURL,
		Issuer:         rule.Issuer,
		Audience:       rule.Audience,
		Algorithms:     rule.Algorithms,
		RequiredClaims: rule.RequiredClaims,
		ExtractClaims:  rule.ExtractClaims,
	})
	if err != nil {
		return nil, err
	}
	return &gateway.JWTClaims{
		Subject: src.Subject,
		Issuer:  src.Issuer,
		Aud:     src.Aud,
		Exp:     src.Exp,
		// Phase 3 (ADR-104, issue #881): thread the resolved
		// custom-claim map through to the gateway-side struct so
		// applyEdgeRuleThrottle can key on it. The verifier extracts
		// the bounded scalar subset independently of required_claims.
		Custom: src.Custom,
	}, nil
}

// Reset drops every JWKS registration. Mirrors
// pkg/gateway/EdgeRuleCache.Reset() wholesale-invalidation
// semantics — the edge_rule_changed pg_notify channel triggers
// both.
func (a *edgeJWKSAdapter) Reset() {
	a.cache.Reset()
}
