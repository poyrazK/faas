//go:build linux || darwin

package tcpd

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFileCertificatesRejectFIFOWithoutBlocking(t *testing.T) {
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
	go func() { _, err := provider.Certificate(context.Background(), "echo.example"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO certificate bundle accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked certificate lookup")
	}
}
