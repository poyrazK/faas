package tcpd

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFileCertificatesRotateAndFailClosed(t *testing.T) {
	directory := t.TempDir()
	provider, err := NewFileCertificateProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	path := filepath.Join(directory, "echo.example.pem")
	writeBundle := func(destination string) []byte {
		t.Helper()
		certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
		key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})...)
		if err := os.WriteFile(destination, bundle, 0600); err != nil {
			t.Fatal(err)
		}
		return certificate.Certificate[0]
	}
	first := writeBundle(path)
	if err := os.Chmod(directory, 0770); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Certificate(context.Background(), "echo.example"); err == nil {
		t.Fatal("lookup accepted writable certificate directory")
	}
	if err := os.Chmod(directory, 0750); err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.Certificate(context.Background(), "echo.example")
	if err != nil || !bytes.Equal(snapshot.Certificate[0], first) {
		t.Fatalf("initial snapshot: %v", err)
	}
	nextPath := filepath.Join(directory, "replacement.pem")
	second := writeBundle(nextPath)
	if err := os.Rename(nextPath, path); err != nil {
		t.Fatal(err)
	}
	replacement, err := provider.Certificate(context.Background(), "echo.example")
	if err != nil || !bytes.Equal(replacement.Certificate[0], second) || !bytes.Equal(snapshot.Certificate[0], first) {
		t.Fatalf("rotation changed old snapshot or missed new bundle: %v", err)
	}
	for _, hostname := range []string{"../echo.example", "missing.example"} {
		if _, err := provider.Certificate(context.Background(), hostname); err == nil {
			t.Fatalf("accepted %q", hostname)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Certificate(context.Background(), "echo.example"); err == nil {
		t.Fatal("publicly readable key accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, api.TCPListenerTLSBundleMaxBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Certificate(context.Background(), "echo.example"); err == nil {
		t.Fatal("oversized bundle accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Certificate(ctx, "echo.example"); err == nil {
		t.Fatal("canceled lookup succeeded")
	}
	outside := filepath.Join(t.TempDir(), "outside.pem")
	writeBundle(outside)
	if err := os.Symlink(outside, filepath.Join(directory, "escape.example.pem")); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Certificate(context.Background(), "escape.example"); err == nil {
		t.Fatal("root-escaping certificate symlink accepted")
	}
}

func TestFileCertificateDirectoryPermissions(t *testing.T) {
	for _, mode := range []os.FileMode{0700, 0750, 0770, 0777} {
		t.Run(mode.String(), func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Chmod(directory, mode); err != nil {
				t.Fatal(err)
			}
			provider, err := NewFileCertificateProvider(directory)
			if mode&0022 != 0 {
				if provider != nil {
					_ = provider.Close()
				}
				if err == nil {
					t.Fatal("accepted writable certificate directory")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := provider.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFileCertificateDirectoryReplacementKeepsAnchoredRoot(t *testing.T) {
	parent := t.TempDir()
	directory := filepath.Join(parent, "certificates")
	if err := os.Mkdir(directory, 0750); err != nil {
		t.Fatal(err)
	}
	writeBundle := func(root string) []byte {
		t.Helper()
		certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
		key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})...)
		if err := os.WriteFile(filepath.Join(root, "echo.example.pem"), bundle, 0600); err != nil {
			t.Fatal(err)
		}
		return certificate.Certificate[0]
	}
	original := writeBundle(directory)
	provider, err := NewFileCertificateProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	moved := filepath.Join(parent, "original")
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0750); err != nil {
		t.Fatal(err)
	}
	replacement := writeBundle(directory)
	certificate, err := provider.Certificate(t.Context(), "echo.example")
	if err != nil || !bytes.Equal(certificate.Certificate[0], original) || bytes.Equal(certificate.Certificate[0], replacement) {
		t.Fatalf("directory replacement redirected certificate lookup: %v", err)
	}
	if err := os.Chmod(moved, 0770); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Certificate(t.Context(), "echo.example"); err == nil {
		t.Fatal("lookup checked replacement directory permissions instead of anchored root")
	}
}

func TestFileCertificateFIFOIsRejectedWithoutBlocking(t *testing.T) {
	directory := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(directory, "echo.example.pem"), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewFileCertificateProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	done := make(chan error, 1)
	go func() { _, err := provider.Certificate(t.Context(), "echo.example"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as certificate bundle")
		}
	case <-time.After(time.Second):
		t.Fatal("certificate lookup blocked opening FIFO")
	}
}
