package authsettings

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Each ES256 key is its own sealed secret, AUTH_JWT_KEY_<ID>. The newest
// AUTH_JWT_SIGNER_<ID> (its value is the activation's Unix time) names the
// key that signs; the others only verify. App secrets are write-only, so the
// CLI and the dashboard read key state from secret names and from the running
// app's public /settings/v1, and never need a private key back.
const (
	SigningKeyPrefix = "AUTH_JWT_KEY_"
	SignerPrefix     = "AUTH_JWT_SIGNER_"
)

var (
	SigningKeyName = regexp.MustCompile(`^AUTH_JWT_KEY_[0-9A-F]{8}$`)
	SignerName     = regexp.MustCompile(`^AUTH_JWT_SIGNER_[0-9A-F]{8}$`)
	KeyID          = regexp.MustCompile(`^[0-9A-F]{8}$`)
)

// KeyKID is the JWT kid of key id.
func KeyKID(id string) string { return "gregale-" + strings.ToLower(id) }

// NewSigningKey returns a key ID and one ES256 private JWK for the
// AUTH_JWT_KEY_<ID> secret. The key leaves the caller only as a sealed app
// secret and is never printed or logged.
func NewSigningKey() (string, string, error) {
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate signing key: %w", err)
	}
	point := private.PublicKey().Bytes() // 0x04 || X || Y
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fmt.Errorf("generate key id: %w", err)
	}
	id := strings.ToUpper(hex.EncodeToString(raw[:]))
	b64 := base64.RawURLEncoding.EncodeToString
	jwk, err := json.Marshal(map[string]string{
		"kty": "EC", "crv": "P-256", "alg": "ES256", "kid": KeyKID(id),
		"x": b64(point[1:33]), "y": b64(point[33:65]), "d": b64(private.Bytes()),
	})
	if err != nil {
		return "", "", fmt.Errorf("encode signing key: %w", err)
	}
	return id, string(jwk), nil
}

// SealedKey is a signing key as app secret names show it: its ID, when its
// secret was created, and whether an AUTH_JWT_SIGNER_<ID> marker names it.
type SealedKey struct {
	ID        string
	CreatedAt string
	Signer    bool
}

// SealedKeys reads the signing keys from secret names, oldest first.
// created maps each secret name to its creation time.
func SealedKeys(created map[string]string) []SealedKey {
	keys := []SealedKey{}
	for name, at := range created {
		if SigningKeyName.MatchString(name) {
			id := strings.TrimPrefix(name, SigningKeyPrefix)
			_, signer := created[SignerPrefix+id]
			keys = append(keys, SealedKey{ID: id, CreatedAt: at, Signer: signer})
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CreatedAt+keys[i].ID < keys[j].CreatedAt+keys[j].ID })
	return keys
}

// SignerMarkers lists the AUTH_JWT_SIGNER_<ID> secrets among names.
func SignerMarkers(names []string) []string {
	markers := []string{}
	for _, name := range names {
		if SignerName.MatchString(name) {
			markers = append(markers, name)
		}
	}
	return markers
}

// SAMLKeySecret holds the RSA key GoTrue's SAML service provider signs with
// and publishes in its metadata. It is created once, when SSO is first turned
// on, and kept when SSO is turned off: a new key would make every identity
// provider distrust the app.
const SAMLKeySecret = "AUTH_SAML_PRIVATE_KEY"

// NewSAMLKey returns a new SAML signing key in the form GoTrue requires:
// base64 PKCS#1 DER, RSA 2048 with exponent 65537.
func NewSAMLKey() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", fmt.Errorf("generate SAML key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(key)), nil
}
