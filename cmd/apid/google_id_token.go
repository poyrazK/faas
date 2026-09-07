package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/edgejwks"
)

const googleIDTokenJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"

// googleIDTokenVerifier is the narrow seam used by the Google callback. The
// production implementation verifies the JWS against Google's rotating JWKS
// and checks the OAuth client, issuer, expiry, and per-login nonce. Tests can
// inject a deterministic implementation without making a network request.
type googleIDTokenVerifier interface {
	Verify(ctx context.Context, rawToken, clientID, nonce string) (*edgejwks.Claims, error)
}

type googleJWKSVerifier struct {
	cache   edgejwks.Cache
	verify  edgejwks.Verifier
	jwksURL string
}

func newGoogleIDTokenVerifier(log *slog.Logger) googleIDTokenVerifier {
	cache := edgejwks.NewCache(edgejwks.Options{
		OnFetchErr: func(rawURL string, err error) {
			if log != nil {
				log.Warn("google oauth: jwks fetch failed", "jwks_url", rawURL, "err", err.Error())
			}
		},
	})
	return &googleJWKSVerifier{
		cache:   cache,
		verify:  edgejwks.NewVerifier(cache, edgejwks.DefaultSkew),
		jwksURL: googleIDTokenJWKSURL,
	}
}

func (v *googleJWKSVerifier) Verify(ctx context.Context, rawToken, clientID, nonce string) (*edgejwks.Claims, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, errors.New("google oauth: id token is empty")
	}
	if strings.TrimSpace(clientID) == "" {
		return nil, errors.New("google oauth: client id is empty")
	}
	if nonce == "" {
		return nil, errors.New("google oauth: nonce is empty")
	}

	// Register lazily so a verifier constructed for a unit test or a long-
	// lived apid process does not fetch keys until the first callback.
	if _, registered, _ := v.cache.Get(ctx, v.jwksURL, ""); !registered {
		if err := v.cache.Register(v.jwksURL); err != nil {
			return nil, fmt.Errorf("google oauth: register jwks: %w", err)
		}
	}
	claims, err := v.verify.Verify(ctx, rawToken, edgejwks.VerifierRule{
		JWKSURL:        v.jwksURL,
		Audience:       []string{clientID},
		Algorithms:     []string{"RS256"},
		RequiredClaims: map[string]string{"nonce": nonce},
	})
	if err != nil {
		return nil, err
	}
	if claims.Exp.IsZero() {
		return nil, errors.New("google oauth: id token expiry is missing")
	}
	if claims.Subject == "" {
		return nil, errors.New("google oauth: id token subject is missing")
	}
	// Google documents both spellings for the issuer. Keep the allowlist
	// closed while accepting the exact variants emitted by Google.
	if claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com" {
		return nil, errors.New("google oauth: unexpected issuer")
	}
	return claims, nil
}
