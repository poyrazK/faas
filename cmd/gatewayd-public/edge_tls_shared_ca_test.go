// adr: 20261107180000

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func caPEM(t *testing.T, commonName string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: commonName, Organization: []string{"CloudFlare, Inc."}},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// R8-7: a compute edge refuses Cloudflare's shared origin-pull CA, alone or
// in a bundle, and takes the zone's own.
func TestRefuseSharedOriginPullCA(t *testing.T) {
	zone := caPEM(t, "Gregale origin pull CA")
	shared := caPEM(t, "origin-pull.cloudflare.net")
	if err := refuseSharedOriginPullCA(zone); err != nil {
		t.Fatalf("the zone's own CA refused: %v", err)
	}
	for name, bundle := range map[string][]byte{"shared": shared, "bundle": append(append([]byte{}, zone...), shared...)} {
		if err := refuseSharedOriginPullCA(bundle); err == nil {
			t.Errorf("%s: Cloudflare's shared origin-pull CA accepted", name)
		}
	}
}
