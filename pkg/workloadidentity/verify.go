package workloadidentity

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// FlagsAudience prevents a federation or service token being replayed to Flags.
const FlagsAudience = "gregale:flags"

type Verifier struct {
	keys   jose.JSONWebKeySet
	issuer string
}

func NewVerifier(keys jose.JSONWebKeySet, issuer string) (*Verifier, error) {
	if issuer == "" || len(keys.Keys) == 0 {
		return nil, errors.New("workload identity: issuer and public keys required")
	}
	seen := map[string]bool{}
	for _, key := range keys.Keys {
		rsaKey, ok := key.Key.(*rsa.PublicKey)
		if !ok || rsaKey == nil || rsaKey.N == nil || rsaKey.N.BitLen() < 2048 || rsaKey.E < 3 || rsaKey.E%2 == 0 || key.KeyID == "" || seen[key.KeyID] || key.Use != "sig" || key.Algorithm != "RS256" {
			return nil, errors.New("workload identity: expected unique RS256 public signing keys")
		}
		seen[key.KeyID] = true
	}
	return &Verifier{keys: keys, issuer: issuer}, nil
}
func LoadVerifier(path, issuer string) (*Verifier, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workload public keys: %w", err)
	}
	var keys jose.JSONWebKeySet
	if err = json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("decode workload public keys: %w", err)
	}
	return NewVerifier(keys, issuer)
}
func (v *Verifier) Verify(token, audience string, now time.Time) (Claims, error) {
	var c Claims
	if v == nil || len(token) > 8192 {
		return c, errors.New("workload identity: unavailable or oversized token")
	}
	parsed, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil || len(parsed.Headers) != 1 {
		return c, errors.New("workload identity: invalid signed token")
	}
	keys := v.keys.Key(parsed.Headers[0].KeyID)
	if len(keys) != 1 {
		return c, errors.New("workload identity: untrusted key")
	}
	if err = parsed.Claims(keys[0].Key, &c); err != nil {
		return c, errors.New("workload identity: invalid signature")
	}
	if err = c.ValidateWithLeeway(jwt.Expected{Issuer: v.issuer, AnyAudience: jwt.Audience{audience}, Time: now}, 5*time.Second); err != nil {
		return c, errors.New("workload identity: invalid claims")
	}
	if c.AccountID == "" || c.AppID == "" || c.InstanceID == "" || c.Subject != "app:"+c.AppID || c.ID == "" || c.Expiry == nil || c.IssuedAt == nil || c.Expiry.Time().Sub(c.IssuedAt.Time()) > DefaultTokenTTL || !c.Expiry.Time().After(c.IssuedAt.Time()) {
		return c, errors.New("workload identity: missing or unbounded workload claims")
	}
	return c, nil
}
