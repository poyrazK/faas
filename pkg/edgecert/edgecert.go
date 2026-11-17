// Package edgecert obtains, seals and stores the apps domains' publicly
// trusted certificate (ADR-20261103163954 §3).
//
// edgednsd runs it on the control plane, the only place that holds the
// zone's Cloudflare token. Each certificate gets a new ECDSA P-256 key,
// generated in memory: its handshake signatures cost an edge far less CPU
// than RSA under a handshake flood. The key leaves the process only sealed
// to the edge-tls seal domain, which the compute edges alone open, so
// edgednsd cannot read back what it stored. One issuer per cell keeps the
// cell inside Let's Encrypt's limit of 5 certificates for the same names in
// 7 days.
package edgecert

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// The CAs a certificate comes from, as the table names them.
const (
	CALetsEncrypt        = "letsencrypt"
	CALetsEncryptStaging = "letsencrypt-staging"
)

// KeyNamespace tags a sealed certificate key, so a blob sealed for another
// column never opens as one.
const KeyNamespace = "edge_tls_key"

// Certificate is one issued certificate as the table stores it.
type Certificate struct {
	Names      []string
	CA         string
	AccountURI string
	// Serial and SHA256 identify the leaf in lowercase hex: the serial for
	// revocation, the fingerprint for the Certificate Transparency watch.
	Serial    string
	SHA256    string
	ChainPEM  string
	KeySealed []byte
	// KeyKID is the recipient the key is sealed to. A new recipient means a
	// new certificate, since edgednsd cannot open the old key to re-seal it.
	KeyKID    string
	NotBefore time.Time
	NotAfter  time.Time
}

// Terms is what a renewal decision needs from the newest stored certificate.
type Terms struct {
	AccountURI string
	KeyKID     string
	SHA256     string
	NotAfter   time.Time
}

// Orderer orders a certificate for names from one CA, for the key that
// signs the request. It returns the chain, leaf first, and the URI of the
// ACME account that ordered it.
type Orderer interface {
	CA() string
	Order(ctx context.Context, key crypto.Signer, names []string) (chainPEM []byte, accountURI string, err error)
}

// Issue orders a certificate for names with a new key and seals the key to
// recipient. It checks that the CA signed this key for exactly these names
// before anything is stored.
func Issue(ctx context.Context, o Orderer, names []string, recipient *age.X25519Recipient) (Certificate, error) {
	if recipient == nil {
		return Certificate{}, errors.New("edgecert: no recipient to seal the key to")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, fmt.Errorf("edgecert: generate key: %w", err)
	}
	chainPEM, accountURI, err := o.Order(ctx, key, names)
	if err != nil {
		return Certificate{}, fmt.Errorf("edgecert: order %s from %s: %w", strings.Join(names, ","), o.CA(), err)
	}
	leaf, err := checkLeaf(chainPEM, key, names)
	if err != nil {
		return Certificate{}, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Certificate{}, fmt.Errorf("edgecert: encode key: %w", err)
	}
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{recipient}, KeyNamespace, der, 0)
	clear(der)
	if err != nil {
		return Certificate{}, fmt.Errorf("edgecert: seal key: %w", err)
	}
	sum := sha256.Sum256(leaf.Raw)
	return Certificate{
		Names:      names,
		CA:         o.CA(),
		AccountURI: accountURI,
		Serial:     hex.EncodeToString(leaf.SerialNumber.Bytes()),
		SHA256:     hex.EncodeToString(sum[:]),
		ChainPEM:   string(chainPEM),
		KeySealed:  sealed,
		KeyKID:     recipient.String(),
		NotBefore:  leaf.NotBefore.UTC(),
		NotAfter:   leaf.NotAfter.UTC(),
	}, nil
}

// checkLeaf parses the chain's first certificate and checks it carries key
// and exactly names.
func checkLeaf(chainPEM []byte, key *ecdsa.PrivateKey, names []string) (*x509.Certificate, error) {
	block, _ := pem.Decode(chainPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("edgecert: the CA returned no PEM certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("edgecert: parse the leaf: %w", err)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("edgecert: the leaf does not carry the key that signed the request")
	}
	got := make([]string, len(leaf.DNSNames))
	for i, n := range leaf.DNSNames {
		got[i] = strings.ToLower(n)
	}
	slices.Sort(got)
	if !slices.Equal(got, names) {
		return nil, fmt.Errorf("edgecert: the leaf names %v, want %v", got, names)
	}
	if !leaf.NotAfter.After(leaf.NotBefore) {
		return nil, errors.New("edgecert: the leaf's validity is empty")
	}
	// Every compute edge holds this key, so its certificate must expire
	// soon after a compromised node copies it (ADR-20261108100000). A CA
	// that ignored the profile gets the order counted as failed rather than
	// a long-lived key handed to the fleet.
	if lifetime := leaf.NotAfter.Sub(leaf.NotBefore); lifetime > api.EdgeCertificateLifetimeMax {
		return nil, fmt.Errorf("edgecert: the leaf lives %s, longer than %s; the CA did not apply the %q profile", lifetime, api.EdgeCertificateLifetimeMax, api.EdgeCertificateProfile)
	}
	return leaf, nil
}

// NormalizeNames lowercases, sorts and checks the names of one certificate:
// host names or wildcards one label deep (*.gregale.dev), at most
// api.EdgeCertificateNamesMax, no duplicates.
func NormalizeNames(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if !validName(n) {
			return nil, fmt.Errorf("edgecert: %q is not a host name or a *. wildcard", n)
		}
		out = append(out, n)
	}
	if len(out) == 0 || len(out) > api.EdgeCertificateNamesMax {
		return nil, fmt.Errorf("edgecert: a certificate names 1 to %d hosts, not %d", api.EdgeCertificateNamesMax, len(out))
	}
	slices.Sort(out)
	if len(slices.Compact(slices.Clone(out))) != len(out) {
		return nil, fmt.Errorf("edgecert: %v names a host twice", out)
	}
	return out, nil
}

func validName(n string) bool {
	n = strings.TrimPrefix(n, "*.")
	if len(n) == 0 || len(n) > 253 || !strings.Contains(n, ".") {
		return false
	}
	for _, label := range strings.Split(n, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// Due reports whether the newest certificate (nil when there is none) needs
// replacing at now, and why: none yet, sealed to a recipient the edges no
// longer hold, or inside api.EdgeCertificateRenewBefore of its expiry.
func Due(newest *Terms, kid string, now time.Time) (bool, string) {
	switch {
	case newest == nil:
		return true, "no certificate"
	case newest.KeyKID != kid:
		return true, "the edge-tls recipient changed"
	case newest.NotAfter.After(now.Add(api.EdgeCertificateLifetimeMax)):
		// A certificate from before short-lived ones (ADR-20261108100000):
		// its key is on every edge for months, so replace it now.
		return true, "longer-lived than the short-lived limit"
	case !now.Before(newest.NotAfter.Add(-api.EdgeCertificateRenewBefore)):
		return true, "expiring"
	}
	return false, ""
}

// pemCertificates counts the certificates in a chain, for the log.
func pemCertificates(chain string) int {
	return bytes.Count([]byte(chain), []byte("-----BEGIN CERTIFICATE-----"))
}
