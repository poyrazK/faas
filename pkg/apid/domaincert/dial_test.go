package domaincert_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/domaincert"
)

func TestDialFallsBackWithinBudgetWithoutResolvingAgain(t *testing.T) {
	addresses := []net.IPAddr{{IP: net.ParseIP("2001:4860:4860::8888")}, {IP: net.ParseIP("8.8.8.8")}}
	var attempts []string
	client, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	conn, err := domaincert.Dial(t.Context(), addresses, 100*time.Millisecond, func(ctx context.Context, network, address string) (net.Conn, error) {
		attempts = append(attempts, address)
		if len(attempts) == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if len(attempts) != 2 || attempts[0] != "[2001:4860:4860::8888]:443" || attempts[1] != "8.8.8.8:443" {
		t.Fatalf("numeric attempts=%v", attempts)
	}
}

func TestDialTLSFallsBackAfterHandshakeTimeout(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	addresses := []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}
	stalled, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	var attempts []string
	conn, err := domaincert.DialTLS(t.Context(), addresses, time.Second, func(ctx context.Context, _, address string) (net.Conn, error) {
		attempts = append(attempts, address)
		if len(attempts) == 1 {
			return stalled, nil
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}, &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if len(attempts) != 2 || attempts[0] != "8.8.8.8:443" || attempts[1] != "1.1.1.1:443" {
		t.Fatalf("TLS attempts = %v", attempts)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("stalled connection was not closed")
	}
}

func TestDialTLSRejectsUntrustedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	conn, err := domaincert.DialTLS(t.Context(), []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, time.Second,
		func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		}, &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12})
	if err == nil {
		_ = conn.Close()
		t.Fatal("accepted an untrusted certificate")
	}
}

func TestDialHonorsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := domaincert.Dial(ctx, []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, time.Second, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dial called after cancellation")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dial=%v", err)
	}
}
