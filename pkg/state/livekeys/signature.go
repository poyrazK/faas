package livekeys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
)

// Live keys and logins carry a writer signature (ADR 20261028161502): an age
// envelope does not say who sealed it, so apid signs each value it writes and
// livequeryd verifies the signature before it opens the value.

const (
	signatureContext = "gregale-sealed-value-signature-v1"
	signatureDomain  = "livequery"
	// SignatureKindKey and SignatureKindLogin name what a signature covers.
	SignatureKindKey   = "live-key"
	SignatureKindLogin = "live-login"
)

var (
	// ErrUnsigned is a value without a signature where one is required.
	ErrUnsigned = errors.New("livekeys: the value carries no writer signature")
	// ErrBadSignature is a signature that no trusted key made for this row.
	ErrBadSignature = errors.New("livekeys: the writer signature does not verify for this row")
)

// SignatureMessage is what a live value's signature covers: the context, the
// domain, the kind of value, the row it was written for (database and, for a
// key, key ID), the recipient and the ciphertext. Each part is
// length-prefixed, so no part can borrow bytes from the next.
func SignatureMessage(kind, databaseID, kid, recipient string, blob []byte) []byte {
	var b bytes.Buffer
	for _, part := range [][]byte{[]byte(signatureContext), []byte(signatureDomain), []byte(kind), []byte(databaseID), []byte(kid), []byte(recipient), blob} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(part))) //nolint:gosec // parts are bounded by the columns' CHECKs
		b.Write(n[:])
		b.Write(part)
	}
	return b.Bytes()
}

// Signer signs the live values apid writes.
type Signer struct{ key ed25519.PrivateKey }

// NewSigner wraps an Ed25519 private key.
func NewSigner(key ed25519.PrivateKey) *Signer { return &Signer{key: key} }

// Sign signs a value of kind written for the row (databaseID, kid). A nil
// Signer signs nothing, so a release without the key keeps writing unsigned
// rows.
func (s *Signer) Sign(kind, databaseID, kid, recipient string, blob []byte) []byte {
	if s == nil {
		return nil
	}
	return ed25519.Sign(s.key, SignatureMessage(kind, databaseID, kid, recipient, blob))
}

// Verifier checks live values' signatures against the trusted public keys.
type Verifier struct {
	keys []ed25519.PublicKey
	// Require refuses an unsigned value (the contract step); otherwise an
	// unsigned value passes and only a signature that does not verify fails.
	Require bool
}

// NewVerifier trusts keys; a signature by any of them verifies.
func NewVerifier(keys []ed25519.PublicKey, require bool) *Verifier {
	return &Verifier{keys: keys, Require: require}
}

// Check reports whether a value may be opened. A nil Verifier checks
// nothing.
func (v *Verifier) Check(kind, databaseID, kid, recipient string, blob, sig []byte) error {
	if v == nil {
		return nil
	}
	if len(sig) == 0 {
		if v.Require {
			return ErrUnsigned
		}
		return nil
	}
	msg := SignatureMessage(kind, databaseID, kid, recipient, blob)
	for _, key := range v.keys {
		if ed25519.Verify(key, msg, sig) {
			return nil
		}
	}
	return ErrBadSignature
}

// LoadSigningKey reads a PEM PKCS#8 Ed25519 private key.
func LoadSigningKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-provisioned credential named by configuration
	if err != nil {
		return nil, fmt.Errorf("livekeys: read signing key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("livekeys: signing key is not a PEM PRIVATE KEY")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("livekeys: parse signing key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("livekeys: signing key is not Ed25519")
	}
	return key, nil
}

// LoadVerifyKeys reads every PEM PUBLIC KEY block in a file. A rotation
// lists the old and the new key.
func LoadVerifyKeys(path string) ([]ed25519.PublicKey, error) {
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-provisioned file named by configuration
	if err != nil {
		return nil, fmt.Errorf("livekeys: read verify keys: %w", err)
	}
	var keys []ed25519.PublicKey
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("livekeys: verify key file holds a %s block", block.Type)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("livekeys: parse verify key: %w", err)
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("livekeys: verify key is not Ed25519")
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("livekeys: verify key file holds no key")
	}
	return keys, nil
}

// WriteSigningKeyPair writes a new key pair: the PKCS#8 private key (0600)
// and the PKIX public key (0644). Neither file may exist yet.
func WriteSigningKeyPair(signPath, verifyPath string) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return err
	}
	if err := writeNew(signPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		return err
	}
	return writeNew(verifyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0o644)
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:forbidigo // operator command writing a key it generated
	if err != nil {
		return fmt.Errorf("livekeys: create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("livekeys: write %s: %w", path, err)
	}
	return f.Close()
}
