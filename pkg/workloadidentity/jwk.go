package workloadidentity

import (
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"fmt"

	"github.com/go-jose/go-jose/v4"
)

// PublicJWK is the RS256 signing JWK for pub with its RFC 7638 thumbprint as
// key id (ADR-1141), so every cluster key has a distinct, reproducible id that
// vmmd's key id and apid's JWKS agree on.
func PublicJWK(pub *rsa.PublicKey) (jose.JSONWebKey, error) {
	jwk := jose.JSONWebKey{Key: pub, Use: "sig", Algorithm: string(jose.RS256)}
	thumb, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return jose.JSONWebKey{}, fmt.Errorf("workload identity: key thumbprint: %w", err)
	}
	jwk.KeyID = base64.RawURLEncoding.EncodeToString(thumb)
	return jwk, nil
}
