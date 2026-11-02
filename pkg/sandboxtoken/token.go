// Package sandboxtoken signs and verifies sandbox session tokens
// (ADR-20261027090013 §3). apid mints a token for one session of one account
// and tenant; the data plane verifies it without reading Postgres, and treats
// a session it no longer routes as revoked. A token is short-lived and never
// names anything a caller could widen: a changed field fails the signature.
package sandboxtoken

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// prefix versions the token format.
const prefix = "sbx1"

// keyInfo separates the sandbox key from every other use of the key it is
// derived from.
const keyInfo = "gregale sandbox session v1"

// maxBytes bounds a token before any decoding.
const maxBytes = 1024

// ErrToken is a token that is malformed, forged, for another format or
// expired. The data plane answers it like an absent route.
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
}

// Placed reports whether the token routes to a booted session.
func (c Claims) Placed() bool { return c.NodeID != "" && c.InstanceID != "" }

// Signer signs and verifies sandbox tokens.
type Signer struct {
	key []byte
}

// NewSigner derives the sandbox key from secret, which every holder of the
// key shares, so each verifies the others' tokens.
func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < 32 {
		return nil, errors.New("sandboxtoken: signing secret must be at least 32 bytes")
	}
	key, err := hkdf.Key(sha256.New, secret, nil, keyInfo, 32)
	if err != nil {
		return nil, err
	}
	return &Signer{key: key}, nil
}

// Sign returns the token for c.
func (s *Signer) Sign(c Claims) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := encoding.EncodeToString(body)
	return prefix + "." + payload + "." + encoding.EncodeToString(s.mac(payload)), nil
}

// Verify checks a token's signature and expiry at now and returns what it
// grants.
func (s *Signer) Verify(token string, now time.Time) (Claims, error) {
	p, rest, ok := strings.Cut(token, ".")
	payload, sig, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 || p != prefix || len(token) > maxBytes {
		return Claims{}, ErrToken
	}
	mac, err := encoding.DecodeString(sig)
	if err != nil || !hmac.Equal(mac, s.mac(payload)) {
		return Claims{}, ErrToken
	}
	body, err := encoding.DecodeString(payload)
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

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(prefix + "." + payload))
	return m.Sum(nil)
}
