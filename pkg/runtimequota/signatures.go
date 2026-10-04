package runtimequota

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	Directory  = "directory"
	Cell       = "cell"
	grantKind  = "grant"
	retireKind = "retirement"
	drainKind  = "drain"
)

// Envelope is canonical raw-URL base64. Payload spelling is signed, and its
// canonical JSON must match after strict decoding: duplicate and unknown fields,
// alternative number spellings and ambiguous whitespace are rejected.
type Envelope struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Decode bounds and strictly validates the transport object before verifying
// its signed payload. Canonical spelling rejects duplicate envelope fields.
func Decode(raw []byte) (Envelope, error) {
	var e Envelope
	if len(raw) > api.RegionalRuntimeQuotaEnvelopeMaxBytes {
		return e, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&e) != nil || d.Decode(&struct{}{}) != io.EOF {
		return Envelope{}, ErrInvalid
	}
	canonical, err := json.Marshal(e)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Envelope{}, ErrInvalid
	}
	return e, nil
}

type message[T any] struct {
	Version  int    `json:"version"`
	Kind     string `json:"kind"`
	IssuerID string `json:"issuer_id"`
	Claims   T      `json:"claims"`
}

type Signer struct {
	issuerID, keyID string
	private         ed25519.PrivateKey
}

func publicKeyID(public ed25519.PublicKey) string {
	digest := sha256.Sum256(public)
	return hex.EncodeToString(digest[:])
}

func NewSigner(issuerID string, private ed25519.PrivateKey) (Signer, error) {
	if !validID(issuerID) || len(private) != ed25519.PrivateKeySize || !bytes.Equal(ed25519.NewKeyFromSeed(private[:ed25519.SeedSize]), private) {
		return Signer{}, ErrInvalid
	}
	return Signer{issuerID: issuerID, keyID: publicKeyID(private.Public().(ed25519.PublicKey)), private: append(ed25519.PrivateKey(nil), private...)}, nil
}

func (s Signer) IssuerID() string { return s.issuerID }
func (s Signer) KeyID() string    { return s.keyID }
func (s Signer) Available() bool {
	return validID(s.issuerID) && len(s.private) == ed25519.PrivateKeySize
}

func signedBytes(kind, keyID string, payload []byte) []byte {
	return append([]byte("gregale.runtime-quota.v1\x00"+kind+"\x00"+keyID+"\x00"), payload...)
}

func sign[T any](s Signer, kind string, claims T) (Envelope, error) {
	if !s.Available() {
		return Envelope{}, ErrTrust
	}
	payload, err := json.Marshal(message[T]{Version: 1, Kind: kind, IssuerID: s.issuerID, Claims: claims})
	if err != nil || len(payload) > api.RegionalRuntimeQuotaPayloadMaxBytes {
		return Envelope{}, ErrInvalid
	}
	return Envelope{KeyID: s.keyID, Payload: base64.RawURLEncoding.EncodeToString(payload), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.private, signedBytes(kind, s.keyID, payload)))}, nil
}

func (s Signer) SignGrant(g Grant) (Envelope, error) {
	if g.Validate() != nil || g.IssuerID != s.issuerID {
		return Envelope{}, ErrInvalid
	}
	return sign(s, grantKind, g)
}
func (s Signer) SignRetirement(r Retirement) (Envelope, error) {
	if r.Validate() != nil || r.Grant.IssuerID != s.issuerID {
		return Envelope{}, ErrInvalid
	}
	return sign(s, retireKind, r)
}
func (s Signer) SignDrain(d Drain) (Envelope, error) {
	if d.Validate() != nil || d.Retirement.Grant.Binding.CellID != s.issuerID {
		return Envelope{}, ErrInvalid
	}
	return sign(s, drainKind, d)
}

// Anchors are operator-configured public keys. Key IDs are derived from public
// keys, and each key has exactly one authority role and issuer identity.
type Anchor struct {
	Role, IssuerID string
	Public         ed25519.PublicKey
}
type Trust struct{ keys map[string]Anchor }

func NewTrust(anchors []Anchor) (Trust, error) {
	t := Trust{keys: make(map[string]Anchor, len(anchors))}
	for _, a := range anchors {
		if (a.Role != Directory && a.Role != Cell) || !validID(a.IssuerID) || len(a.Public) != ed25519.PublicKeySize {
			return Trust{}, ErrInvalid
		}
		id := publicKeyID(a.Public)
		if old, ok := t.keys[id]; ok && (old.Role != a.Role || old.IssuerID != a.IssuerID) {
			return Trust{}, ErrTrust
		}
		a.Public = append(ed25519.PublicKey(nil), a.Public...)
		t.keys[id] = a
	}
	return t, nil
}

func verify[T any](t Trust, envelope Envelope, kind, role string) (message[T], error) {
	var out message[T]
	// Bound transport spelling before base64 decoding or allocating the payload.
	if len(envelope.KeyID)+len(envelope.Payload)+len(envelope.Signature) > api.RegionalRuntimeQuotaEnvelopeMaxBytes {
		return out, ErrInvalid
	}
	a, ok := t.keys[envelope.KeyID]
	if !ok || a.Role != role {
		return out, ErrTrust
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Payload)
	if err != nil || len(payload) > api.RegionalRuntimeQuotaPayloadMaxBytes || base64.RawURLEncoding.EncodeToString(payload) != envelope.Payload {
		return out, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.RawURLEncoding.EncodeToString(signature) != envelope.Signature || !ed25519.Verify(a.Public, signedBytes(kind, envelope.KeyID, payload), signature) {
		return out, ErrTrust
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(&struct{}{}) != io.EOF || out.Version != 1 || out.Kind != kind || out.IssuerID != a.IssuerID {
		return message[T]{}, ErrInvalid
	}
	canonical, err := json.Marshal(out)
	if err != nil || !bytes.Equal(canonical, payload) {
		return message[T]{}, ErrInvalid
	}
	return out, nil
}

func (t Trust) VerifyGrant(e Envelope, expected Binding) (Grant, error) {
	m, err := verify[Grant](t, e, grantKind, Directory)
	if err != nil {
		return Grant{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.IssuerID != m.IssuerID {
		return Grant{}, ErrInvalid
	}
	if m.Claims.Binding != expected {
		return Grant{}, ErrBinding
	}
	return m.Claims, nil
}
func (t Trust) VerifyRetirement(e Envelope, expected Binding) (Retirement, error) {
	m, err := verify[Retirement](t, e, retireKind, Directory)
	if err != nil {
		return Retirement{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Grant.IssuerID != m.IssuerID {
		return Retirement{}, ErrInvalid
	}
	if m.Claims.Grant.Binding != expected {
		return Retirement{}, ErrBinding
	}
	return m.Claims, nil
}
func (t Trust) VerifyDrain(e Envelope) (Drain, error) {
	m, err := verify[Drain](t, e, drainKind, Cell)
	if err != nil {
		return Drain{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Retirement.Grant.Binding.CellID != m.IssuerID {
		return Drain{}, ErrInvalid
	}
	return m.Claims, nil
}
