// adr: 1027 — pgproxyd presents a renewed internal client leaf after
// SIGHUP, without a restart.
package main

import (
	"crypto/x509"
	"log/slog"
	"syscall"
	"testing"
	"time"
)

func TestRotatingClientTLSPresentsTheRenewedLeafAfterSIGHUP(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	caPath, _ := writeCert(t, t.TempDir(), 99, now.Add(365*24*time.Hour), now)
	certPath, keyPath := writeCert(t, dir, 1, now.Add(30*24*time.Hour), now.Add(-time.Hour))
	cfg, err := rotatingClientTLS(t.Context(), "schedd_client", certPath, keyPath, caPath, slog.New(slog.DiscardHandler))
	if err != nil || cfg == nil {
		t.Fatalf("client TLS = %v, %v", cfg, err)
	}
	presented := func() int64 {
		t.Helper()
		cert, err := cfg.GetClientCertificate(nil)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		return leaf.SerialNumber.Int64()
	}
	if got := presented(); got != 1 {
		t.Fatalf("serial %d, want 1", got)
	}
	writeCert(t, dir, 2, now.Add(90*24*time.Hour), now)
	if got := presented(); got != 1 {
		t.Fatalf("serial %d before SIGHUP, want 1", got)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for presented() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("SIGHUP did not rotate the client leaf")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// No paths, no TLS (a unix-socket schedd).
	if cfg, err := rotatingClientTLS(t.Context(), "schedd_client", "", "", "", slog.New(slog.DiscardHandler)); cfg != nil || err != nil {
		t.Fatalf("empty paths = %v, %v", cfg, err)
	}
}
