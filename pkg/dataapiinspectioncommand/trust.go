package dataapiinspectioncommand

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"sort"
	"strings"
)

// MaxTrustBundleBytes keeps the JSON-encoded task request below the protocol
// frame limit even when the command itself uses its full allowance.
const MaxTrustBundleBytes = 32 << 10

// ValidTrustBundleFingerprint checks the canonical, lowercase SHA-256 form.
func ValidTrustBundleFingerprint(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

// ParseTrustBundle accepts only an explicit, bounded set of CA certificates.
// The caller must establish where the bytes came from before trusting them.
func ParseTrustBundle(data []byte) (*x509.CertPool, error) {
	certs, err := parseTrustCertificates(data)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool, nil
}

// TrustBundleFingerprint identifies the set of CA certificates accepted for
// this inspection. PEM formatting, certificate order, and duplicates do not
// affect the fingerprint. This is not proof of where the bundle came from.
func TrustBundleFingerprint(data []byte) (string, error) {
	certs, err := parseTrustCertificates(data)
	if err != nil {
		return "", err
	}
	raw := make([][]byte, 0, len(certs))
	for _, cert := range certs {
		raw = append(raw, cert.Raw)
	}
	sort.Slice(raw, func(i, j int) bool { return bytes.Compare(raw[i], raw[j]) < 0 })
	h := sha256.New()
	_, _ = h.Write([]byte("gregale-data-api-inspection-trust-v1\x00"))
	for i, der := range raw {
		if i > 0 && bytes.Equal(der, raw[i-1]) {
			continue
		}
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(der)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(der)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func parseTrustCertificates(data []byte) ([]*x509.Certificate, error) {
	invalid := errors.New("invalid inspection trust bundle")
	if len(data) == 0 || len(data) > MaxTrustBundleBytes {
		return nil, invalid
	}
	var certs []*x509.Certificate
	rest := data
	for len(bytes.TrimSpace(rest)) != 0 {
		rest = bytes.TrimSpace(rest)
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, invalid
		}
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, invalid
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.BasicConstraintsValid || !cert.IsCA || cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, invalid
		}
		certs = append(certs, cert)
		rest = next
	}
	if len(certs) == 0 {
		return nil, invalid
	}
	return certs, nil
}
