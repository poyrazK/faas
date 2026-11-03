// adr: 1027 — pgproxyd picks up a renewed public certificate without a
// restart, keeps the one in use when the new files do not load, and
// exports how long the one in use has left.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// writeCert writes a self-signed *.db.gregale.test certificate valid until
// notAfter, and its key, stamping both files with mtime.
func writeCert(t *testing.T, dir string, serial int64, notAfter, mtime time.Time) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "*.db.gregale.test"},
		DNSNames: []string{"*.db.gregale.test"}, NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	writeStamped(t, certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), mtime)
	writeStamped(t, keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), mtime)
	return certPath, keyPath
}

func writeStamped(t *testing.T, path string, body []byte, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func servedSerial(t *testing.T, c *publicCert) int64 {
	t.Helper()
	cert, err := c.GetCertificate(nil)
	if err != nil || cert == nil || cert.Leaf == nil {
		t.Fatalf("certificate in use = %v, %v", cert, err)
	}
	return cert.Leaf.SerialNumber.Int64()
}

func TestPublicCertReloadsARenewalAndKeepsTheOneInUseOtherwise(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	certPath, keyPath := writeCert(t, dir, 1, now.Add(10*24*time.Hour), now.Add(-time.Hour))
	reg := prometheus.NewRegistry()
	c, err := loadPublicCert(certPath, keyPath, reg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if got := servedSerial(t, c); got != 1 {
		t.Fatalf("serial %d, want 1", got)
	}
	if left := testutil.ToFloat64(prometheusGauge(t, reg, "pgproxyd_tls_cert_expiry_seconds")); left < 9*86400 || left > 10*86400 {
		t.Fatalf("expiry seconds = %v, want about 10 days", left)
	}

	// Unchanged files are not reloaded.
	if replaced, err := c.reload(); err != nil || replaced {
		t.Fatalf("reload of unchanged files = %v, %v", replaced, err)
	}

	// A renewal is served on the next check.
	writeCert(t, dir, 2, now.Add(90*24*time.Hour), now)
	if replaced, err := c.reload(); err != nil || !replaced || servedSerial(t, c) != 2 {
		t.Fatalf("reload of a renewal = %v, %v (serial %d)", replaced, err, servedSerial(t, c))
	}

	// A new chain whose key has not arrived yet keeps the renewal in use.
	other := t.TempDir()
	newCert, _ := writeCert(t, other, 3, now.Add(90*24*time.Hour), now.Add(time.Minute))
	body, err := os.ReadFile(newCert)
	if err != nil {
		t.Fatal(err)
	}
	writeStamped(t, certPath, body, now.Add(time.Minute))
	if _, err := c.reload(); err == nil || servedSerial(t, c) != 2 {
		t.Fatalf("reload with a mismatched key = %v (serial %d); want an error and serial 2", err, servedSerial(t, c))
	}

	// An expired certificate is refused too.
	writeCert(t, dir, 4, now.Add(-time.Hour), now.Add(2*time.Minute))
	if _, err := c.reload(); err == nil || servedSerial(t, c) != 2 {
		t.Fatalf("reload of an expired certificate = %v (serial %d)", err, servedSerial(t, c))
	}
}

func TestPublicCertWatchReloadsOnHangup(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	certPath, keyPath := writeCert(t, dir, 1, now.Add(30*24*time.Hour), now.Add(-time.Hour))
	reg := prometheus.NewRegistry()
	c, err := loadPublicCert(certPath, keyPath, reg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	hup := make(chan os.Signal, 1)
	go c.watch(ctx, time.Hour, hup)
	writeCert(t, dir, 2, now.Add(90*24*time.Hour), now)
	hup <- os.Interrupt
	deadline := time.Now().Add(5 * time.Second)
	for servedSerial(t, c) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("a hangup did not reload the renewal")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := testutil.ToFloat64(c.reloads.WithLabelValues("ok")); got != 1 {
		t.Fatalf("ok reloads = %v, want 1", got)
	}
}

func TestPublicCertRefusesToStartWithoutAUsableCertificate(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	certPath, keyPath := writeCert(t, dir, 1, now.Add(-time.Minute), now)
	if _, err := loadPublicCert(certPath, keyPath, prometheus.NewRegistry(), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("started with an expired certificate")
	}
	if _, err := loadPublicCert(filepath.Join(dir, "missing.crt"), keyPath, prometheus.NewRegistry(), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("started without a certificate")
	}
}

// prometheusGauge finds a registered gauge by name.
func prometheusGauge(t *testing.T, reg *prometheus.Registry, name string) prometheus.Collector {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return constGauge(f.GetMetric()[0].GetGauge().GetValue())
		}
	}
	t.Fatalf("no %s", name)
	return nil
}

func constGauge(v float64) prometheus.Collector {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "copy"})
	g.Set(v)
	return g
}
