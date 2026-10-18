package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
	claims, err := a.verifyClaims(ctx, rawToken, policy, false)
	if err != nil {
		return "", err
	}
	return claims.Subject, nil
}

func (a *realtimeJWTAuthorizer) AuthorizeIdentity(ctx context.Context, rawToken string, policy realtime.AuthPolicy) (realtime.JWTIdentity, error) {
	claims, err := a.verifyClaims(ctx, rawToken, policy, true)
	if err != nil {
		return realtime.JWTIdentity{}, err
	}
	if claims.Exp.IsZero() || !time.Now().Before(claims.Exp) {
		return realtime.JWTIdentity{}, realtime.ErrUnauthorized
	}
	return realtime.JWTIdentity{Principal: claims.Subject, SessionID: claims.Custom["session_id"], ExpiresAt: claims.Exp}, nil
}

// AuthorizeClaims verifies a token for RLS-mode live queries (ADR-20261016151947) and
// returns its whole claims object, which the database reads as
// request.jwt.claims. The payload is read only after the signature, issuer,
// audience and lifetime have been verified; the token must expire.
func (a *realtimeJWTAuthorizer) AuthorizeClaims(ctx context.Context, rawToken string, policy realtime.AuthPolicy) (json.RawMessage, time.Time, error) {
	claims, err := a.verifyClaims(ctx, rawToken, policy, true)
	if err != nil {
		return nil, time.Time{}, err
	}
	if claims.Exp.IsZero() || !time.Now().Before(claims.Exp) {
		return nil, time.Time{}, realtime.ErrUnauthorized
	}
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, time.Time{}, fmt.Errorf("realtime: malformed jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > api.LiveQueryTokenMaxBytes {
		return nil, time.Time{}, fmt.Errorf("realtime: jwt payload unreadable")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, time.Time{}, fmt.Errorf("realtime: jwt payload is not an object")
	}
	return json.RawMessage(payload), claims.Exp, nil
}

func (a *realtimeJWTAuthorizer) verifyClaims(ctx context.Context, rawToken string, policy realtime.AuthPolicy, requireExpiry bool) (*edgejwks.Claims, error) {
	if a == nil || a.cache == nil || a.verify == nil {
		return nil, fmt.Errorf("realtime: jwt authorizer unavailable")
	}
	if _, registered, _ := a.cache.Get(ctx, policy.JWKSURL, ""); !registered {
		if err := a.cache.Register(policy.JWKSURL); err != nil {
			return nil, fmt.Errorf("realtime: register jwks: %w", err)
		}
	}
	claims, err := a.verify.Verify(ctx, rawToken, edgejwks.VerifierRule{
		JWKSURL:        policy.JWKSURL,
		Issuer:         policy.Issuer,
		Audience:       policy.Audience,
		Algorithms:     policy.Algorithms,
		RequiredClaims: policy.RequiredClaims,
		ExtractClaims:  []string{"session_id"},
		RequireExpiry:  requireExpiry,
	})
	if err != nil {
		return nil, err
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("realtime: jwt subject is missing")
	}
	return claims, nil
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
