package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/edgejwks"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/realtime"
)

type realtimeJWTAuthorizer struct {
	cache  edgejwks.Cache
	verify edgejwks.Verifier
}

func newRealtimeJWTAuthorizer(log *slog.Logger) realtime.JWTAuthorizer {
	cache := edgejwks.NewCache(edgejwks.Options{
		// auth_jwks_url is customer-supplied; fetch it through the §11
		// egress guard like the callback client (see newJWKSHTTPClient).
		HTTPClient: newJWKSHTTPClient(),
		OnFetchErr: func(rawURL string, err error) {
			if log != nil {
				log.Warn("realtime: jwks fetch failed", "jwks_url", rawURL, "err", err)
			}
		},
	})
	return &realtimeJWTAuthorizer{
		cache:  cache,
		verify: edgejwks.NewVerifier(cache, edgejwks.DefaultSkew),
	}
}

func (a *realtimeJWTAuthorizer) Authorize(ctx context.Context, rawToken string, policy realtime.AuthPolicy) (string, error) {
	if a == nil || a.cache == nil || a.verify == nil {
		return "", fmt.Errorf("realtime: jwt authorizer unavailable")
	}
	if _, registered, _ := a.cache.Get(ctx, policy.JWKSURL, ""); !registered {
		if err := a.cache.Register(policy.JWKSURL); err != nil {
			return "", fmt.Errorf("realtime: register jwks: %w", err)
		}
	}
	claims, err := a.verify.Verify(ctx, rawToken, edgejwks.VerifierRule{
		JWKSURL:        policy.JWKSURL,
		Issuer:         policy.Issuer,
		Audience:       policy.Audience,
		Algorithms:     policy.Algorithms,
		RequiredClaims: policy.RequiredClaims,
	})
	if err != nil {
		return "", err
	}
	if claims.Subject == "" {
		return "", fmt.Errorf("realtime: jwt subject is missing")
	}
	return claims.Subject, nil
}

// newJWKSHTTPClient fetches an endpoint's customer-supplied JWKS URL. The
// API validator rejects only a fixed list of private-address string
// prefixes, so a hostname resolving to a node-local or metadata address —
// or a redirect to one — needs the dial-time guard.
func newJWKSHTTPClient() *http.Client {
	client := oci.NewEgressHTTPClient()
	// Dev/test-only escape hatch, gated on FAAS_EGRESS_ALLOW_LOOPBACK=1.
	if loopback := oci.NewEgressHTTPClientAllowLoopback(); loopback != nil {
		client = loopback
	}
	client.Timeout = edgejwks.DefaultFetchTimeout
	return client
}
