package dataapiinspectioncommand

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func trustCertificate(t *testing.T, isCA bool) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: isCA, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestParseTrustBundleAcceptsOnlyBoundedCACertificates(t *testing.T) {
	ca := trustCertificate(t, true)
	if _, err := ParseTrustBundle(append(append([]byte(nil), ca...), ca...)); err != nil {
		t.Fatal(err)
	}
	for _, value := range [][]byte{nil, trustCertificate(t, false), []byte(strings.Repeat("x", MaxTrustBundleBytes+1)), append([]byte("prefix\n"), ca...), append(append([]byte(nil), ca...), []byte("trailing")...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("secret")})} {
		if _, err := ParseTrustBundle(value); err == nil {
			t.Fatal("accepted invalid trust bundle")
		}
	}
}

func TestTrustBundleFingerprintUsesCertificateSet(t *testing.T) {
	a, b, other := trustCertificate(t, true), trustCertificate(t, true), trustCertificate(t, true)
	ab := append(append([]byte(nil), a...), b...)
	ba := append(append(append([]byte("\n"), b...), a...), a...)
	fingerprint, err := TrustBundleFingerprint(ab)
	if err != nil || !ValidTrustBundleFingerprint(fingerprint) {
		t.Fatalf("invalid fingerprint: %q %v", fingerprint, err)
	}
	if got, err := TrustBundleFingerprint(ba); err != nil || got != fingerprint {
		t.Fatalf("reordered or repeated CA changed fingerprint: %q %v", got, err)
	}
	if got, err := TrustBundleFingerprint(append(append([]byte(nil), a...), other...)); err != nil || got == fingerprint {
		t.Fatalf("different CA did not change fingerprint: %q %v", got, err)
	}
	for _, invalid := range [][]byte{nil, []byte("invalid"), trustCertificate(t, false)} {
		if _, err := TrustBundleFingerprint(invalid); err == nil {
			t.Fatal("accepted invalid CA bundle")
		}
	}
	for _, invalid := range []string{"", strings.Repeat("A", 64), strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if ValidTrustBundleFingerprint(invalid) {
			t.Fatalf("accepted invalid fingerprint %q", invalid)
		}
	}
}
