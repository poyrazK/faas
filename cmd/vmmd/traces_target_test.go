package main

// adr: 957 — split-box guest spans use vmmd's node-identity mTLS leaf.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTraceTestPKI(t *testing.T) (cert, key, ca string) {
	t.Helper()
	dir := t.TempDir()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "compute-1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, ca = filepath.Join(dir, "apid-client.crt"), filepath.Join(dir, "apid-client.key"), filepath.Join(dir, "ca.crt")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	for path, data := range map[string][]byte{
		cert: certPEM, ca: certPEM,
		key: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cert, key, ca
}

func TestTraceSpansWriterTarget(t *testing.T) {
	cert, key, ca := writeTraceTestPKI(t)
	for _, tc := range []struct {
		name    string
		env     map[string]string
		target  string
		mtls    bool
		wantErr string
	}{
		{name: "single-box default", target: "/run/faas/otel_spans_writer.sock"},
		{name: "socket override wins", env: map[string]string{"FAAS_APID_OTEL_SPANS_WRITER_SOCKET": "/tmp/sw.sock", "FAAS_VMMD_SPANS_WRITER_TARGET": "tcp://apid.faas:9093"}, target: "/tmp/sw.sock"},
		{name: "split-box mTLS", env: map[string]string{
			"FAAS_VMMD_SPANS_WRITER_TARGET":       "tcp://apid.faas:9093",
			"FAAS_VMMD_APID_CLIENT_TLS_CERT_PATH": cert, "FAAS_VMMD_APID_CLIENT_TLS_KEY_PATH": key, "FAAS_VMMD_APID_CLIENT_TLS_CA_PATH": ca,
		}, target: "tcp://apid.faas:9093", mtls: true},
		{name: "remote without TLS refused", env: map[string]string{"FAAS_VMMD_SPANS_WRITER_TARGET": "tcp://apid.faas:9093"}, wantErr: "requires vmmd apid client mTLS"},
		{name: "local target without TLS allowed", env: map[string]string{"FAAS_VMMD_SPANS_WRITER_TARGET": "unix:///run/faas/otel_spans_writer.sock"}, target: "unix:///run/faas/otel_spans_writer.sock"},
		{name: "incomplete TLS", env: map[string]string{"FAAS_VMMD_SPANS_WRITER_TARGET": "tcp://apid.faas:9093", "FAAS_VMMD_APID_CLIENT_TLS_CERT_PATH": cert}, wantErr: "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, tlsCfg, err := traceSpansWriterTarget(func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || target != tc.target || (tlsCfg != nil) != tc.mtls {
				t.Fatalf("target=%q mtls=%v err=%v, want %q mtls=%v", target, tlsCfg != nil, err, tc.target, tc.mtls)
			}
		})
	}
}
