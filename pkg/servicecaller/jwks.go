package servicecaller

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	// MaxJWKSBytes bounds parsing and network reads of a caller-key document.
	MaxJWKSBytes = 4 << 20
	// MaxJWKSKeys prevents a malformed key document from allocating an
	// unbounded map even when it remains under the byte limit.
	MaxJWKSKeys = 20_000
	// MaxPublicKeyPEMBytes bounds the PEM form loaded from persistent state.
	MaxPublicKeyPEMBytes = 4 << 10
	// DefaultKeyFetchTimeout bounds a verifier bootstrap request.
	DefaultKeyFetchTimeout = 5 * time.Second
)

// JWK is the public Ed25519 signing key shape exposed by Gregale's caller-key
// document. No private or tenant-specific data is included.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	Kid string `json:"kid"`
	X   string `json:"x"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// JWKSet is a JSON Web Key Set containing the active and short-lived previous
// per-node public keys used to verify service-caller assertions.
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// KeyID derives the same stable identifier the node-local minter puts in JWT
// headers. It is the first 128 bits of SHA-256(public key), base64url encoded.
func KeyID(publicKey ed25519.PublicKey) string {
	if len(publicKey) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(publicKey)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// ParsePublicKeyPEM parses the PKIX PEM form stored for node keys.
func ParsePublicKeyPEM(raw []byte) (ed25519.PublicKey, error) {
	if len(raw) == 0 || len(raw) > MaxPublicKeyPEMBytes {
		return nil, fmt.Errorf("servicecaller: public key PEM size must be between 1 and %d bytes", MaxPublicKeyPEMBytes)
	}
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "-----BEGIN PUBLIC KEY-----") {
		return nil, errors.New("servicecaller: public key is not a single PUBLIC KEY PEM block")
	}
	block, rest := pem.Decode([]byte(trimmed))
	if block == nil || block.Type != "PUBLIC KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("servicecaller: public key is not a single PUBLIC KEY PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("servicecaller: parse public key: %w", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("servicecaller: public key is not Ed25519")
	}
	return publicKey, nil
}

// JWKSFromTrustedKeys renders a deterministic public key document. A key ID
// that does not fingerprint its public key is rejected rather than published
// under a misleading JWT kid.
func JWKSFromTrustedKeys(keys TrustedKeys) (JWKSet, error) {
	if len(keys) > MaxJWKSKeys {
		return JWKSet{}, fmt.Errorf("servicecaller: key set exceeds %d keys", MaxJWKSKeys)
	}
	kids := make([]string, 0, len(keys))
	for kid := range keys {
		kids = append(kids, kid)
	}
	sort.Strings(kids)

	set := JWKSet{Keys: make([]JWK, 0, len(kids))}
	for _, kid := range kids {
		publicKey := keys[kid]
		if len(publicKey) != ed25519.PublicKeySize || KeyID(publicKey) != kid {
			return JWKSet{}, fmt.Errorf("servicecaller: public key does not match kid %q", kid)
		}
		set.Keys = append(set.Keys, JWK{
			Kty: "OKP",
			Crv: "Ed25519",
			Kid: kid,
			X:   base64.RawURLEncoding.EncodeToString(publicKey),
			Alg: "EdDSA",
			Use: "sig",
		})
	}
	return set, nil
}

// TrustedKeysFromJWKS parses only the Ed25519 signing keys Gregale uses.
// Unknown JWK members are ignored for forward compatibility, but duplicate
// kids, other algorithms, and non-canonical key identifiers fail closed.
func TrustedKeysFromJWKS(raw []byte) (TrustedKeys, error) {
	if len(raw) == 0 || len(raw) > MaxJWKSBytes {
		return nil, fmt.Errorf("servicecaller: JWKS size must be between 1 and %d bytes", MaxJWKSBytes)
	}
	var set JWKSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("servicecaller: decode JWKS: %w", err)
	}
	if set.Keys == nil {
		return nil, errors.New("servicecaller: JWKS must contain a keys array")
	}
	if len(set.Keys) > MaxJWKSKeys {
		return nil, fmt.Errorf("servicecaller: key set exceeds %d keys", MaxJWKSKeys)
	}
	keys := make(TrustedKeys, len(set.Keys))
	for _, jwk := range set.Keys {
		if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" || jwk.Alg != "EdDSA" || jwk.Use != "sig" || jwk.Kid == "" {
			return nil, errors.New("servicecaller: JWKS contains an unsupported key")
		}
		if _, exists := keys[jwk.Kid]; exists {
			return nil, fmt.Errorf("servicecaller: JWKS contains duplicate kid %q", jwk.Kid)
		}
		publicKey, err := base64.RawURLEncoding.DecodeString(jwk.X)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || KeyID(ed25519.PublicKey(publicKey)) != jwk.Kid {
			return nil, fmt.Errorf("servicecaller: JWKS contains an invalid public key for kid %q", jwk.Kid)
		}
		keys[jwk.Kid] = ed25519.PublicKey(publicKey)
	}
	return keys, nil
}

// FetchTrustedKeys fetches a caller-key document with a bounded response size
// and timeout. HTTPS is required except for loopback URLs used by local tests.
// Redirects are refused so an HTTPS trust URL cannot silently downgrade or
// move to a different key issuer.
func FetchTrustedKeys(ctx context.Context, client *http.Client, endpoint string) (TrustedKeys, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("servicecaller: invalid JWKS URL")
	}
	if parsed.Host == "" {
		return nil, errors.New("servicecaller: invalid JWKS URL")
	}
	if parsed.User != nil {
		return nil, errors.New("servicecaller: invalid JWKS URL")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("servicecaller: invalid JWKS URL")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(parsed.Hostname()) {
			return nil, errors.New("servicecaller: JWKS URL must use HTTPS")
		}
	default:
		return nil, errors.New("servicecaller: JWKS URL must use HTTPS")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("servicecaller: create JWKS request: %w", err)
	}
	request.Header.Set("Accept", "application/jwk-set+json, application/json")

	fetchClient := http.Client{Timeout: DefaultKeyFetchTimeout}
	if client != nil {
		fetchClient = *client
		if fetchClient.Timeout <= 0 {
			fetchClient.Timeout = DefaultKeyFetchTimeout
		}
	}
	fetchClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := fetchClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("servicecaller: fetch JWKS: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("servicecaller: fetch JWKS: unexpected status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxJWKSBytes+1))
	if err != nil {
		return nil, fmt.Errorf("servicecaller: read JWKS: %w", err)
	}
	if len(body) > MaxJWKSBytes {
		return nil, fmt.Errorf("servicecaller: JWKS exceeds %d bytes", MaxJWKSBytes)
	}
	return TrustedKeysFromJWKS(body)
}

func isLoopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
