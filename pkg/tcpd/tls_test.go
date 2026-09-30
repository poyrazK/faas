package tcpd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type testCertificateProvider struct {
	certificate *tls.Certificate
	calls       atomic.Int32
}

func (p *testCertificateProvider) Certificate(context.Context, string) (*tls.Certificate, error) {
	p.calls.Add(1)
	return p.certificate, nil
}
func testListenerCertificate(t *testing.T, name string, expiry time.Time) *tls.Certificate {
	t.Helper()
	return testListenerCertificateWithUsage(t, name, expiry, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
}

func testListenerCertificateWithUsage(t *testing.T, name string, expiry time.Time, usage []x509.ExtKeyUsage) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage}
	der, err := x509.CreateCertificate(rand.Reader, identity, identity, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestListenerTLSHandshake(t *testing.T) {
	for _, scenario := range []struct {
		name, sni, certificateName string
		expired, succeeds          bool
	}{
		{"valid", "echo.example", "echo.example", false, true},
		{"wrong-sni", "other.example", "echo.example", false, false},
		{"missing-sni", "", "echo.example", false, false},
		{"wrong-certificate", "echo.example", "other.example", false, false},
		{"expired", "echo.example", "echo.example", true, false},
		{"client-only", "echo.example", "echo.example", false, false},
		{"wrong-key", "echo.example", "echo.example", false, false},
		{"no-signature-algorithms", "echo.example", "echo.example", false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			expiry := time.Now().Add(time.Hour)
			if scenario.expired {
				expiry = time.Now().Add(-time.Minute)
			}
			provider := &testCertificateProvider{certificate: testListenerCertificate(t, scenario.certificateName, expiry)}
			if scenario.name == "client-only" {
				provider.certificate = testListenerCertificateWithUsage(t, scenario.certificateName, expiry, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
			}
			if scenario.name == "wrong-key" {
				provider.certificate.PrivateKey = testListenerCertificate(t, scenario.certificateName, expiry).PrivateKey
			}
			if scenario.name == "no-signature-algorithms" {
				provider.certificate.SupportedSignatureAlgorithms = []tls.SignatureScheme{}
			}
			server, client := net.Pipe()
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				connection, err := TerminateTLS(ctx, server, "echo.example", provider)
				if connection != nil {
					_ = connection.NetConn().Close()
				}
				done <- err
			}()
			// Server policy is the subject here; client trust is tested separately
			// when certificate provisioning is wired into the public edge.
			secure := tls.Client(client, &tls.Config{ServerName: scenario.sni, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
			clientErr := secure.HandshakeContext(ctx)
			select {
			case err := <-done:
				if (err == nil) != scenario.succeeds || (clientErr == nil) != scenario.succeeds {
					t.Fatalf("server=%v client=%v expected success=%v", err, clientErr, scenario.succeeds)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("TLS handshake retained connection")
			}
			if scenario.sni != "echo.example" && provider.calls.Load() != 0 {
				t.Fatal("invalid SNI accessed certificate provider")
			}
		})
	}
}

func TestListenerTLSCanceledStalledHandshake(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := TerminateTLS(ctx, server, "echo.example", &testCertificateProvider{}); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("stalled handshake ignored cancellation")
	}
	_ = client.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := client.Write([]byte{1}); err == nil {
		t.Fatal("failed handshake left raw connection open")
	}
}

func TestInspectTLSCertificateUsability(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		usage    []x509.ExtKeyUsage
		unknown  []asn1.ObjectIdentifier
		wrongKey bool
		succeeds bool
	}{
		{name: "server", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, succeeds: true},
		{name: "unrestricted", succeeds: true},
		{name: "any", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}, succeeds: true},
		{name: "client-only", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{name: "unknown-only", unknown: []asn1.ObjectIdentifier{{1, 2, 3, 4}}},
		{name: "wrong-key", usage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, wrongKey: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
			leaf, err := x509.ParseCertificate(certificate.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			leaf.ExtKeyUsage, leaf.UnknownExtKeyUsage = scenario.usage, scenario.unknown
			key := certificate.PrivateKey.(*ecdsa.PrivateKey)
			certificate.Certificate[0], err = x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.wrongKey {
				certificate.PrivateKey = testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour)).PrivateKey
			}
			expiry, err := inspectTLSCertificate(certificate, "echo.example", time.Now())
			if (err == nil) != scenario.succeeds || (scenario.succeeds && !expiry.Equal(leaf.NotAfter)) {
				t.Fatalf("expiry=%v err=%v expected success=%v", expiry, err, scenario.succeeds)
			}
		})
	}
}
