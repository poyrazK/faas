// Package sandboxtoken signs and verifies sandbox session tokens
// (ADR-20261027090013 §3). apid mints a token for one session of one account
// and tenant; the data plane verifies it without reading Postgres, and treats
// a session it no longer routes as revoked. A token is short-lived and never
// names anything a caller could widen: a changed field fails the signature.
//
// Tokens are Ed25519-signed. Only apid holds the private key, derived from
// its session key; gateways hold the public key they fetch from apid, so no
// gateway can mint a token (ADR-20261102191500).
package sandboxtoken

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

// prefix versions the token format: sbx2.<key id>.<payload>.<signature>.
const prefix = "sbx2"

// keyInfo separates the sandbox signing key from every other use of the key
// it is derived from.
const keyInfo = "gregale sandbox session ed25519 v2"

// maxBytes bounds a token before any decoding.
const maxBytes = 1024

// ErrToken is a token that is malformed, forged, for another format or key,
// or expired. The data plane answers it like an absent route.
var ErrToken = errors.New("sandboxtoken: invalid sandbox token")

// Claims is what a token grants: data-plane calls on one session. A token
// minted once the session is placed also names its node and instance, which
// the data plane routes by without reading the control plane, and the plan's
// command caps, which it applies.
type Claims struct {
	SandboxID  string    `json:"s"`
	AccountID  string    `json:"a"`
	TenantID   string    `json:"t,omitempty"`
	NodeID     string    `json:"n,omitempty"`
	InstanceID string    `json:"v,omitempty"`
	IssuedAt   time.Time `json:"i"`
	ExpiresAt  time.Time `json:"x"`
	// ExecTimeoutMaxMillis and ExecOutputMaxBytes bound one command.
	ExecTimeoutMaxMillis int64 `json:"et,omitempty"`
	ExecOutputMaxBytes   int64 `json:"eo,omitempty"`
	// WorkspaceMaxBytes bounds what one workspace archive unpacks
	// (SandboxWorkspaceMaxMB).
	WorkspaceMaxBytes int64 `json:"wb,omitempty"`
}

// Placed reports whether the token routes to a booted session.
func (c Claims) Placed() bool { return c.NodeID != "" && c.InstanceID != "" }

// PublicKey is one verification key and the ID tokens name it by.
type PublicKey struct {
	ID  string
	Key ed25519.PublicKey
}

// KeyID names a public key: the first 8 bytes of its SHA-256.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return encoding.EncodeToString(sum[:8])
}

// Issuer signs tokens. apid alone holds one.
type Issuer struct {
	priv ed25519.PrivateKey
	pub  PublicKey
}

// NewIssuer derives the signing key from secret, apid's session key.
func NewIssuer(secret []byte) (*Issuer, error) {
	if len(secret) < 32 {
		return nil, errors.New("sandboxtoken: signing secret must be at least 32 bytes")
	}
	seed, err := hkdf.Key(sha256.New, secret, nil, keyInfo, ed25519.SeedSize)
	if err != nil {
		return nil, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &Issuer{priv: priv, pub: PublicKey{ID: KeyID(pub), Key: pub}}, nil
}

// PublicKey is the key that verifies this issuer's tokens.
func (i *Issuer) PublicKey() PublicKey { return i.pub }

// Sign returns the token for c.
func (i *Issuer) Sign(c Claims) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signed := prefix + "." + i.pub.ID + "." + encoding.EncodeToString(body)
	return signed + "." + encoding.EncodeToString(ed25519.Sign(i.priv, []byte(signed))), nil
}

// Verifier checks tokens against the public keys it was given, which
// SetKeys replaces as apid's keys rotate. The zero value accepts nothing.
type Verifier struct {
	keys atomic.Pointer[map[string]ed25519.PublicKey]
}

// NewVerifier returns a verifier for keys.
func NewVerifier(keys ...PublicKey) *Verifier {
	v := &Verifier{}
	v.SetKeys(keys)
	return v
}

// SetKeys replaces the verification keys. A key whose ID does not match its
// bytes is dropped.
func (v *Verifier) SetKeys(keys []PublicKey) {
	m := make(map[string]ed25519.PublicKey, len(keys))
	for _, k := range keys {
		if len(k.Key) == ed25519.PublicKeySize && k.ID == KeyID(k.Key) {
			m[k.ID] = k.Key
		}
	}
	v.keys.Store(&m)
}

// Ready reports whether the verifier holds a key.
func (v *Verifier) Ready() bool {
	m := v.keys.Load()
	return m != nil && len(*m) > 0
}

// Verify checks a token's signature and expiry at now and returns what it
// grants.
func (v *Verifier) Verify(token string, now time.Time) (Claims, error) {
	if len(token) > maxBytes {
		return Claims{}, ErrToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != prefix {
		return Claims{}, ErrToken
	}
	m := v.keys.Load()
	if m == nil {
		return Claims{}, ErrToken
	}
	pub, ok := (*m)[parts[1]]
	if !ok {
		return Claims{}, ErrToken
	}
	sig, err := encoding.DecodeString(parts[3])
	if err != nil || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]+"."+parts[2]), sig) {
		return Claims{}, ErrToken
	}
	body, err := encoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrToken
	}
	var c Claims
	if json.Unmarshal(body, &c) != nil || c.SandboxID == "" || c.AccountID == "" || !now.Before(c.ExpiresAt) {
		return Claims{}, ErrToken
	}
	return c, nil
}

// encoding decodes strictly, so a token has one spelling: unused bits in a
// final character are refused instead of ignored.
var encoding = base64.RawURLEncoding.Strict()
