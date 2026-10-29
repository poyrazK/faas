package livekeys

import (
	"crypto/ed25519"

	"github.com/onebox-faas/faas/pkg/sealsig"
)

// Live keys and logins carry a writer signature (ADR 20261028161502): an age
// envelope does not say who sealed it, so apid signs each value it writes and
// livequeryd verifies the signature before it opens the value.

const (
	signatureDomain = "livequery"
	// SignatureKindKey and SignatureKindLogin name what a signature covers.
	SignatureKindKey   = "live-key"
	SignatureKindLogin = "live-login"
)

var (
	// ErrUnsigned is a value without a signature where one is required.
	ErrUnsigned = sealsig.ErrUnsigned
	// ErrBadSignature is a signature that no trusted key made for this row.
	ErrBadSignature = sealsig.ErrBadSignature
)

// SignatureMessage is what a live value's signature covers: the context, the
// domain, the kind of value, the row it was written for (database and, for a
// key, key ID), the recipient and the ciphertext.
func SignatureMessage(kind, databaseID, kid, recipient string, blob []byte) []byte {
	return sealsig.Message(signatureDomain, kind, []byte(databaseID), []byte(kid), []byte(recipient), blob)
}

// Signer signs the live values apid writes.
type Signer struct{ s *sealsig.Signer }

// NewSigner wraps an Ed25519 private key.
func NewSigner(key ed25519.PrivateKey) *Signer { return &Signer{s: sealsig.NewSigner(key)} }

// Sign signs a value of kind written for the row (databaseID, kid). A nil
// Signer signs nothing, so a release without the key keeps writing unsigned
// rows.
func (s *Signer) Sign(kind, databaseID, kid, recipient string, blob []byte) []byte {
	if s == nil {
		return nil
	}
	return s.s.Sign(SignatureMessage(kind, databaseID, kid, recipient, blob))
}

// Verifier checks live values' signatures against the trusted public keys.
type Verifier struct{ v *sealsig.Verifier }

// NewVerifier trusts keys; a signature by any of them verifies. require
// refuses an unsigned value (the contract step).
func NewVerifier(keys []ed25519.PublicKey, require bool) *Verifier {
	return &Verifier{v: sealsig.NewVerifier(keys, require)}
}

// Check reports whether a value may be opened. A nil Verifier checks
// nothing.
func (v *Verifier) Check(kind, databaseID, kid, recipient string, blob, sig []byte) error {
	if v == nil {
		return nil
	}
	return v.v.Check(SignatureMessage(kind, databaseID, kid, recipient, blob), sig)
}

// LoadSigningKey reads a PEM PKCS#8 Ed25519 private key.
func LoadSigningKey(path string) (ed25519.PrivateKey, error) { return sealsig.LoadSigningKey(path) }

// LoadVerifyKeys reads every PEM PUBLIC KEY block in a file.
func LoadVerifyKeys(path string) ([]ed25519.PublicKey, error) { return sealsig.LoadVerifyKeys(path) }

// WriteSigningKeyPair writes a new key pair; see sealsig.WriteSigningKeyPair.
func WriteSigningKeyPair(signPath, verifyPath string) error {
	return sealsig.WriteSigningKeyPair(signPath, verifyPath)
}
