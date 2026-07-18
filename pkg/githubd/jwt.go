// jwt.go — minimal RS256 signer for the GitHub App JWT flow (slice 8).
//
// We deliberately avoid adding github.com/golang-jwt/jwt to the
// dependency tree: the only JWT shape githubd produces is the
// App JWT (iss=<app id>, iat=…, exp=…, alg=RS256), and the
// github-jwt format is well-defined enough to do in 30 lines.
//
// The verifier side is never needed (api.github.com signs these;
// we don't verify inbound JWTs).
package githubd

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// jwtSignRS256 signs an app JWT with the given key. The resulting
// token is base64url(headers) + "." + base64url(payload) + "." +
// base64url(signature).
func jwtSignRS256(appID string, key *rsa.PrivateKey, iat, exp time.Time) (string, error) {
	if key == nil {
		return "", errors.New("githubd: nil RSA key")
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	payload := map[string]any{
		"iat": iat.Unix(),
		"exp": exp.Unix(),
		"iss": appID,
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerB64 + "." + payloadB64

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("githubd: rsa sign: %w", err)
	}
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sigB64, nil
}

// parseRSAPrivateKeyPEM decodes a PEM block carrying an RSA private
// key (PKCS#1 or PKCS#8). GitHub's App installer hands out PKCS#1 by
// default but older installs sometimes serve PKCS#8; both work.
func parseRSAPrivateKeyPEM(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("githubd: no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubd: parse pkcs8: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("githubd: pkcs8 key is not RSA (%T)", parsed)
	}
	return key, nil
}

// _ = big.Int{} is a compile-time guard so an over-eager goimports
// tool doesn't strip the import on a future edit.
var _ = big.NewInt
