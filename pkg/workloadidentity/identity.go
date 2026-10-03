// Package workloadidentity mints the short-lived identity assertions that
// running applications use with cloud workload-identity federation and that
// Gregale host services use to identify an active disposable Run.
//
// This is deliberately separate from pkg/oidc: that package implements the
// customer-controlled CI deploy exchange and returns opaque fp_oidc_ bearer
// keys. App assertions identify a running app instance and use an audience
// selected by that app. Execution assertions identify a scheduler-owned Run
// lease and are host-to-host capability material.
package workloadidentity

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

const (
	// DefaultIssuer is the issuer used by a stock Gregale installation. The
	// issuer is configurable because operators normally expose it on their own
	// control-plane domain.
	DefaultIssuer = "https://identity.gregale.dev"
	// DefaultKeyID is included in the JWT header and JWKS so providers can
	// rotate keys without guessing which public key to use.
	DefaultKeyID = "gregale-workload-identity-1"
	// DefaultTokenTTL bounds replay of a token copied from a running process.
	DefaultTokenTTL = 5 * time.Minute
	maxAudienceLen  = 512
)

// Claims is the stable Gregale workload identity contract. Standard OIDC
// claims are embedded so AWS, GCP, and Cloudflare can validate the assertion
// without a provider-specific adapter.
type Claims struct {
	jwt.Claims
	AccountID  string `json:"account_id"`
	AppID      string `json:"app_id"`
	InstanceID string `json:"instance_id"`
}

// ExecutionClaims is the host-to-host identity for a claimed disposable Run.
// LeaseToken fences an assertion to the scheduler's current execution lease;
// callers must keep it out of guest payloads and guest-visible metadata.
type ExecutionClaims struct {
	jwt.Claims
	AccountID   string `json:"account_id"`
	ExecutionID string `json:"execution_id"`
	LeaseToken  string `json:"lease_token"`
}

// Token is the response returned to the guest metadata proxy.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// ExecutionToken is host-to-host capability material. Its JWT is private so
// this type cannot accidentally serialize into a guest-facing JSON response.
// Use BearerValue only when setting the internal outbound identity header.
type ExecutionToken struct{ jwt string }

func (t ExecutionToken) BearerValue() string { return t.jwt }

// Signer mints RS256 assertions and publishes the corresponding public JWKS.
// It is safe for concurrent use: jose's signer is immutable after creation.
type Signer struct {
	private *rsa.PrivateKey
	issuer  string
	kid     string
	ttl     time.Duration
	signer  jose.Signer
}

// NewSigner validates configuration and prepares a signer. RSA-2048 or
// stronger keys are accepted because AWS IAM's OIDC provider currently
// requires an RSA/ECDSA OIDC signature algorithm rather than EdDSA.
func NewSigner(private *rsa.PrivateKey, issuer, keyID string, ttl time.Duration) (*Signer, error) {
	if private == nil {
		return nil, errors.New("workload identity: private key is required")
	}
	if private.N.BitLen() < 2048 {
		return nil, errors.New("workload identity: RSA key must be at least 2048 bits")
	}
	issuer = strings.TrimSpace(issuer)
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("workload identity: issuer must be an https URL")
	}
	if keyID == "" {
		keyID = DefaultKeyID
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: private},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID),
	)
	if err != nil {
		return nil, fmt.Errorf("workload identity: create signer: %w", err)
	}
	return &Signer{private: private, issuer: issuer, kid: keyID, ttl: ttl, signer: signer}, nil
}

// Mint creates a short-lived assertion for one running app instance.
func (s *Signer) Mint(now time.Time, accountID, appID, instanceID, audience string) (Token, error) {
	if s == nil || s.signer == nil {
		return Token{}, errors.New("workload identity: signer is not configured")
	}
	if accountID == "" || appID == "" || instanceID == "" {
		return Token{}, errors.New("workload identity: account_id, app_id, and instance_id are required")
	}
	if err := validateAudience(audience); err != nil {
		return Token{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	expires := now.Add(s.ttl)
	var jtiBytes [16]byte
	if _, err := rand.Read(jtiBytes[:]); err != nil {
		return Token{}, fmt.Errorf("workload identity: generate jti: %w", err)
	}
	claims := Claims{
		Claims: jwt.Claims{
			Issuer:    s.issuer,
			Subject:   "app:" + appID,
			Audience:  jwt.Audience{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			Expiry:    jwt.NewNumericDate(expires),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Second)),
			ID:        hex.EncodeToString(jtiBytes[:]),
		},
		AccountID:  accountID,
		AppID:      appID,
		InstanceID: instanceID,
	}
	raw, err := jwt.Signed(s.signer).Claims(claims).Serialize()
	if err != nil {
		return Token{}, fmt.Errorf("workload identity: serialize token: %w", err)
	}
	return Token{AccessToken: raw, TokenType: "Bearer", ExpiresIn: int64(s.ttl / time.Second)}, nil
}

// MintExecution creates a short-lived assertion for a claimed disposable Run.
// Unlike Mint, this subject is an execution and is never a guest-facing
// workload identity. The outbound broker uses the lease token to reject stale
// VMs after a claim is completed, cancelled, or reclaimed.
func (s *Signer) MintExecution(now time.Time, accountID, executionID, leaseToken, audience string) (ExecutionToken, error) {
	if s == nil || s.signer == nil {
		return ExecutionToken{}, errors.New("workload identity: signer is not configured")
	}
	if accountID == "" || executionID == "" || leaseToken == "" {
		return ExecutionToken{}, errors.New("workload identity: account_id, execution_id, and lease_token are required")
	}
	if _, err := uuid.Parse(accountID); err != nil {
		return ExecutionToken{}, errors.New("workload identity: account_id is invalid")
	}
	if _, err := uuid.Parse(executionID); err != nil {
		return ExecutionToken{}, errors.New("workload identity: execution_id is invalid")
	}
	if _, err := uuid.Parse(leaseToken); err != nil {
		return ExecutionToken{}, errors.New("workload identity: lease_token is invalid")
	}
	if err := validateAudience(audience); err != nil {
		return ExecutionToken{}, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	ttl := s.ttl
	if ttl > DefaultTokenTTL {
		ttl = DefaultTokenTTL
	}
	expires := now.Add(ttl)
	var jtiBytes [16]byte
	if _, err := rand.Read(jtiBytes[:]); err != nil {
		return ExecutionToken{}, fmt.Errorf("workload identity: generate jti: %w", err)
	}
	claims := ExecutionClaims{
		Claims: jwt.Claims{
			Issuer:    s.issuer,
			Subject:   "execution:" + executionID,
			Audience:  jwt.Audience{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			Expiry:    jwt.NewNumericDate(expires),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Second)),
			ID:        hex.EncodeToString(jtiBytes[:]),
		},
		AccountID: accountID, ExecutionID: executionID, LeaseToken: leaseToken,
	}
	raw, err := jwt.Signed(s.signer).Claims(claims).Serialize()
	if err != nil {
		return ExecutionToken{}, fmt.Errorf("workload identity: serialize execution token: %w", err)
	}
	return ExecutionToken{jwt: raw}, nil
}

// JWKS returns the public key set suitable for an OIDC discovery endpoint.
func (s *Signer) JWKS() jose.JSONWebKeySet {
	if s == nil || s.private == nil {
		return jose.JSONWebKeySet{}
	}
	return jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key:       &s.private.PublicKey,
		KeyID:     s.kid,
		Use:       "sig",
		Algorithm: string(jose.RS256),
	}}}
}

// ParseRSAPrivateKeyPEM accepts PKCS#1 and PKCS#8 PEM files, the two formats
// used by the deployment tooling. The returned key is checked by NewSigner.
func ParseRSAPrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("workload identity: PEM block not found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("workload identity: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("workload identity: private key is %T, want RSA", parsed)
	}
	return key, nil
}

func validateAudience(audience string) error {
	if audience == "" || len(audience) > maxAudienceLen {
		return fmt.Errorf("workload identity: audience must be 1-%d characters", maxAudienceLen)
	}
	for _, r := range audience {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("workload identity: audience contains whitespace or control characters")
		}
	}
	return nil
}
