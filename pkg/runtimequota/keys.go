package runtimequota

// adr: 632
import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	PrivateKeyFilename = "key.pem"
	TrustFilename      = "trust.json"
)

// KeyConfig is optional operator configuration. A partial configuration is an
// error. The issuer and role come from the owner, never from a private key file.
type KeyConfig struct {
	PrivateKeyPath  string `toml:"private_key_path"`
	TrustBundlePath string `toml:"trust_bundle_path"`
}

func (c KeyConfig) Configured() bool { return c.PrivateKeyPath != "" || c.TrustBundlePath != "" }

type AuthorityKeys struct {
	Signer Signer
	Trust  Trust
}

type publicAnchor struct {
	Role      string `json:"role"`
	IssuerID  string `json:"issuer_id"`
	KeyID     string `json:"key_id"`
	PublicKey string `json:"public_key"`
}

type trustBundle struct {
	Version int            `json:"version"`
	Anchors []publicAnchor `json:"anchors"`
}

// PublicAnchor returns an independent copy of public material only.
func (s Signer) PublicAnchor(role string) (Anchor, error) {
	if !s.Available() || (role != Directory && role != Cell) {
		return Anchor{}, ErrTrust
	}
	return Anchor{Role: role, IssuerID: s.IssuerID(), Public: append(ed25519.PublicKey(nil), s.private.Public().(ed25519.PublicKey)...)}, nil
}

func EncodeTrustBundle(anchors []Anchor) ([]byte, error) {
	if len(anchors) == 0 || len(anchors) > api.RegionalRuntimeTrustAnchorsMax {
		return nil, ErrInvalid
	}
	if _, err := NewTrust(anchors); err != nil {
		return nil, err
	}
	b := trustBundle{Version: 1, Anchors: make([]publicAnchor, 0, len(anchors))}
	seen := make(map[string]bool, len(anchors))
	for _, a := range anchors {
		id := publicKeyID(a.Public)
		if seen[id] {
			return nil, ErrTrust
		}
		seen[id] = true
		b.Anchors = append(b.Anchors, publicAnchor{Role: a.Role, IssuerID: a.IssuerID, KeyID: id, PublicKey: base64.RawURLEncoding.EncodeToString(a.Public)})
	}
	raw, err := json.Marshal(b)
	if err != nil || len(raw) > api.RegionalRuntimeTrustBundleMaxBytes {
		return nil, ErrInvalid
	}
	return append(raw, '\n'), nil
}

func decodeTrustBundle(raw []byte) ([]Anchor, error) {
	if len(raw) > api.RegionalRuntimeTrustBundleMaxBytes {
		return nil, ErrInvalid
	}
	var b trustBundle
	if err := decodeCanonicalObject(bytes.TrimSuffix(raw, []byte("\n")), &b, api.RegionalRuntimeTrustBundleMaxBytes); err != nil || b.Version != 1 || len(b.Anchors) == 0 || len(b.Anchors) > api.RegionalRuntimeTrustAnchorsMax {
		return nil, ErrInvalid
	}
	anchors := make([]Anchor, 0, len(b.Anchors))
	seen := make(map[string]bool, len(b.Anchors))
	for _, a := range b.Anchors {
		public, err := base64.RawURLEncoding.Strict().DecodeString(a.PublicKey)
		if err != nil || len(public) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(public) != a.PublicKey || publicKeyID(public) != a.KeyID {
			return nil, ErrInvalid
		}
		if seen[a.KeyID] {
			return nil, ErrTrust
		}
		seen[a.KeyID] = true
		anchors = append(anchors, Anchor{Role: a.Role, IssuerID: a.IssuerID, Public: public})
	}
	if _, err := NewTrust(anchors); err != nil {
		return nil, err
	}
	return anchors, nil
}

func LoadTrustAnchors(path string) ([]Anchor, error) {
	raw, err := readKeyFile(path, false, api.RegionalRuntimeTrustBundleMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("runtime quota: read trust bundle: %w", err)
	}
	anchors, err := decodeTrustBundle(raw)
	if err != nil {
		return nil, fmt.Errorf("runtime quota: decode trust bundle: %w", err)
	}
	return anchors, nil
}

// LoadAuthorityKeys loads stable material once. There is no generation fallback
// and no TLS/workload signing key reuse. The current key must have an explicit
// public anchor for exactly the owner's role and immutable issuer identity.
func LoadAuthorityKeys(c KeyConfig, role, issuer string) (AuthorityKeys, error) {
	if c.PrivateKeyPath == "" || c.TrustBundlePath == "" || !validID(issuer) || (role != Directory && role != Cell) {
		return AuthorityKeys{}, ErrTrust
	}
	raw, err := readKeyFile(c.PrivateKeyPath, true, api.RegionalRuntimeSigningKeyMaxBytes)
	if err != nil {
		return AuthorityKeys{}, fmt.Errorf("runtime quota: read signing key: %w", err)
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.Equal(bytes.TrimSpace(raw), bytes.TrimSpace(pem.EncodeToMemory(block))) {
		return AuthorityKeys{}, ErrInvalid
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	private, ok := key.(ed25519.PrivateKey)
	if err != nil || !ok {
		return AuthorityKeys{}, ErrInvalid
	}
	signer, err := NewSigner(issuer, private)
	if err != nil {
		return AuthorityKeys{}, err
	}
	anchors, err := LoadTrustAnchors(c.TrustBundlePath)
	if err != nil {
		return AuthorityKeys{}, err
	}
	trust, err := NewTrust(anchors)
	if err != nil || trust.CheckSigner(signer, role) != nil {
		return AuthorityKeys{}, ErrTrust
	}
	return AuthorityKeys{Signer: signer, Trust: trust}, nil
}

// ProvisionAuthorityKeys creates a new, exclusively owned key directory. It
// never replaces an existing directory, including one from a partial install.
// Rotation creates a second directory and distributes overlapping public trust
// before the owner restarts with the second private key.
func ProvisionAuthorityKeys(path, role, issuer string) (Anchor, error) {
	if !filepath.IsAbs(path) || !validID(issuer) || (role != Directory && role != Cell) {
		return Anchor{}, ErrInvalid
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Anchor{}, err
	}
	signer, err := NewSigner(issuer, private)
	if err != nil {
		return Anchor{}, err
	}
	anchor, err := signer.PublicAnchor(role)
	if err != nil {
		return Anchor{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return Anchor{}, err
	}
	public, err := EncodeTrustBundle([]Anchor{anchor})
	if err != nil {
		return Anchor{}, err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return Anchor{}, fmt.Errorf("runtime quota: create fresh key directory: %w", err)
	}
	for _, f := range []struct {
		name string
		raw  []byte
		mode os.FileMode
	}{{PrivateKeyFilename, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o400}, {TrustFilename, public, 0o444}} {
		if err := writeKeyFile(filepath.Join(path, f.name), f.raw, f.mode); err != nil {
			return Anchor{}, err // Preserve partial installation; never overwrite it.
		}
	}
	if err := syncKeyDirectory(path); err != nil {
		return Anchor{}, err
	}
	if err := syncKeyDirectory(filepath.Dir(path)); err != nil {
		return Anchor{}, err
	}
	return anchor, nil
}

// WriteTrustBundle exclusively creates a public bundle; it never overwrites an
// active verifier file. Publish the new path and restart readers after staging.
func WriteTrustBundle(path string, anchors []Anchor) error {
	raw, err := EncodeTrustBundle(anchors)
	if err != nil {
		return err
	}
	if err := writeKeyFile(path, raw, 0o444); err != nil {
		return err
	}
	return syncKeyDirectory(filepath.Dir(path))
}
