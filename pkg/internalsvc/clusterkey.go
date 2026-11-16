package internalsvc

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// ErrClusterKeySealed means no identity opened the cluster_signing_keys
// row's sealed blob.
var ErrClusterKeySealed = errors.New("internalsvc: no identity opens the cluster signing key")

// OpenClusterKey opens a cluster_signing_keys row (ADR-125): it unseals the
// private key with identities, then refuses a row whose key_id or
// public_key_pem disagrees with that key, so a minter never signs with a
// key the gateways do not pin. It returns the key and its kid.
func OpenClusterKey(sealedBlob []byte, publicKeyPEM, keyID string, identities []*age.X25519Identity) (ed25519.PrivateKey, string, error) {
	plaintext, err := UnsealClusterKey(sealedBlob, identities)
	if err != nil {
		return nil, "", errors.Join(ErrClusterKeySealed, err)
	}
	priv, err := ParseClusterPrivateKeyPEM(plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("internalsvc: parse unsealed cluster key (kid=%s): %w", keyID, err)
	}
	kid := KidFromPub(priv.Public().(ed25519.PublicKey))
	if kid != keyID {
		return nil, "", fmt.Errorf("internalsvc: cluster key row kid=%s does not match derived kid=%s; refusing to mint", keyID, kid)
	}
	pub, err := ParseClusterPublicKeyPEM([]byte(publicKeyPEM))
	if err != nil {
		return nil, "", fmt.Errorf("internalsvc: parse cluster key public_key_pem (kid=%s): %w", keyID, err)
	}
	if !pub.Equal(priv.Public().(ed25519.PublicKey)) {
		return nil, "", fmt.Errorf("internalsvc: cluster key public_key_pem does not match the sealed key (kid=%s); refusing to mint", keyID)
	}
	return priv, kid, nil
}

// UnsealClusterKey opens the row's sealed blob with any of identities.
func UnsealClusterKey(sealed []byte, identities []*age.X25519Identity) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, errors.New("empty sealed blob")
	}
	if len(identities) == 0 {
		return nil, errors.New("no identities available")
	}
	_, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	return plaintext, nil
}

// ParseClusterPublicKeyPEM decodes the PKIX "PUBLIC KEY" PEM of
// cluster_signing_keys.public_key_pem.
func ParseClusterPublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("cluster key public_key_pem is not PEM-encoded")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, fmt.Errorf("cluster key public_key_pem has unexpected PEM block type %q", block.Type)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKIX public key: %w", err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("cluster key public_key_pem is not an Ed25519 public key")
	}
	return pub, nil
}

// ParseClusterPrivateKeyPEM decodes the unsealed PKCS#8 "PRIVATE KEY" PEM.
func ParseClusterPrivateKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("cluster key payload is not PEM-encoded")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("cluster key payload has unexpected PEM block type %q", block.Type)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("cluster key payload is not an Ed25519 key")
	}
	return priv, nil
}
