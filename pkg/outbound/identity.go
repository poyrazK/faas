package outbound

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const (
	// WorkloadIdentityHeader carries a vmmd-signed assertion to outboundd.
	// It is never forwarded to a provider.
	WorkloadIdentityHeader = "X-Gregale-Workload-Identity"
	identityAudiencePrefix = "gregale:outbound:"
	maxIdentityTokenBytes  = 8192
	maxIdentityTTL         = 5 * time.Minute
	identityClockSkew      = 5 * time.Second
)

var ErrInvalidWorkloadIdentity = errors.New("invalid outbound workload identity")

// WorkloadIdentity is the authenticated caller, not a guest-supplied app ID.
type WorkloadIdentity struct {
	AccountID  string
	AppID      string
	InstanceID string
}

type IdentityVerifier interface {
	Verify(rawToken, integrationID string) (WorkloadIdentity, error)
}

// WorkloadIdentityVerifier trusts a locally provisioned public JWKS. No
// caller-controlled URL or network fetch participates in authentication.
type WorkloadIdentityVerifier struct {
	keys   map[string]*rsa.PublicKey
	issuer string
	now    func() time.Time
}

func NewWorkloadIdentityVerifier(jwksJSON []byte, issuer string) (*WorkloadIdentityVerifier, error) {
	parsedIssuer, err := url.Parse(issuer)
	if err != nil || parsedIssuer.Scheme != "https" || parsedIssuer.Host == "" || parsedIssuer.User != nil || parsedIssuer.RawQuery != "" || parsedIssuer.Fragment != "" || strings.TrimSpace(issuer) != issuer {
		return nil, errors.New("outbound workload identity issuer must be an HTTPS URL")
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(jwksJSON, &set); err != nil {
		return nil, fmt.Errorf("outbound workload identity JWKS: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("outbound workload identity JWKS has no keys")
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, key := range set.Keys {
		public, ok := key.Key.(*rsa.PublicKey)
		if !ok || !key.IsPublic() || !key.Valid() || key.KeyID == "" || key.Algorithm != string(jose.RS256) || key.Use != "sig" || public.N.BitLen() < 2048 {
			return nil, errors.New("outbound workload identity JWKS contains an invalid signing key")
		}
		if _, exists := keys[key.KeyID]; exists {
			return nil, errors.New("outbound workload identity JWKS contains duplicate key IDs")
		}
		keys[key.KeyID] = public
	}
	return &WorkloadIdentityVerifier{keys: keys, issuer: issuer, now: time.Now}, nil
}

func (v *WorkloadIdentityVerifier) Verify(rawToken, integrationID string) (WorkloadIdentity, error) {
	if v == nil || len(rawToken) == 0 || len(rawToken) > maxIdentityTokenBytes || integrationID == "" {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	token, err := jwt.ParseSigned(rawToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(token.Headers) != 1 {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	header := token.Headers[0]
	if header.Algorithm != string(jose.RS256) || header.KeyID == "" {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	key, ok := v.keys[header.KeyID]
	if !ok {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	var claims workloadidentity.Claims
	if err := token.Claims(key, &claims); err != nil {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	now := v.now()
	audience := identityAudiencePrefix + integrationID
	if len(claims.Audience) != 1 || claims.Audience[0] != audience || claims.AccountID == "" || claims.AppID == "" || claims.InstanceID == "" || claims.Subject != "app:"+claims.AppID || claims.ID == "" || claims.IssuedAt == nil || claims.NotBefore == nil || claims.Expiry == nil {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	if claims.Expiry.Time().Before(claims.IssuedAt.Time()) || claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > maxIdentityTTL || claims.NotBefore.Time().After(claims.IssuedAt.Time()) {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	if err := claims.ValidateWithLeeway(jwt.Expected{
		Issuer: v.issuer, Subject: "app:" + claims.AppID,
		AnyAudience: jwt.Audience{audience}, Time: now,
	}, identityClockSkew); err != nil {
		return WorkloadIdentity{}, ErrInvalidWorkloadIdentity
	}
	return WorkloadIdentity{AccountID: claims.AccountID, AppID: claims.AppID, InstanceID: claims.InstanceID}, nil
}

var _ IdentityVerifier = (*WorkloadIdentityVerifier)(nil)
