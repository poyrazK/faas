// Package workloadidentity mints the short-lived identity assertions that
// running applications use with cloud workload-identity federation.
//
// This is deliberately separate from pkg/oidc: that package implements the
// customer-controlled CI deploy exchange and returns opaque fp_oidc_ bearer
// keys. Workload identity assertions are platform-issued JWTs whose subject is
// a running app instance and whose audience is selected by the application.
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

// Token is the response returned to the guest metadata proxy.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

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
