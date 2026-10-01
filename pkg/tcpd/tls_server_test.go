package tcpd

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestServerTLSBeforeAdmission(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	routes := NewRouteTable()
	if err := routes.Upsert(Route{PublicPort: listener.Addr().(*net.TCPAddr).Port, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000, Protocol: "tcp", TLSHostname: "echo.example"}); err != nil {
		t.Fatal(err)
	}
	var admissions atomic.Int32
	errorsReported := make(chan error, 2)
	server := &Server{Listener: listener, Routes: routes, MaxConnections: 1, Limiter: NewConnectionLimiter(1), Certificates: &testCertificateProvider{certificate: testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))},
		connectionSlots: make(chan struct{}, 1),
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
			admissions.Add(1)
			return gateway.Target{AppID: "app", InstanceID: "instance", NodeID: "node"}, nil
		}),
		Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, _ gateway.Target) error {
			_, err := io.WriteString(conn, "plaintext-guest-reply")
			return err
		}),
		OnError: func(err error) { errorsReported <- err },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("TLS server shutdown hung")
		}
	}()
	dial := func(name string) (*tls.Conn, error) {
		raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err != nil {
			return nil, err
		}
		secure := tls.Client(raw, &tls.Config{ServerName: name, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
		_ = secure.SetDeadline(time.Now().Add(time.Second))
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, err
		}
		return secure, nil
	}
	if conn, err := dial("other.example"); err == nil {
		_ = conn.Close()
		t.Fatal("wrong SNI succeeded")
	}
	select {
	case <-errorsReported:
	case <-time.After(time.Second):
		t.Fatal("failed TLS session did not finish")
	}
	if admissions.Load() != 0 {
		t.Fatal("invalid TLS woke app")
	}
	deadline := time.Now().Add(time.Second)
	for len(server.connectionSlots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("failed TLS handshake retained global credit")
		}
		time.Sleep(time.Millisecond)
	}
	// A valid connection after rejection also proves both quota credits released.
	conn, err := dial("echo.example")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := make([]byte, len("plaintext-guest-reply"))
	if _, err := io.ReadFull(conn, payload); err != nil || string(payload) != "plaintext-guest-reply" {
		t.Fatalf("reply=%q err=%v", payload, err)
	}
	if admissions.Load() != 1 {
		t.Fatalf("admissions=%d", admissions.Load())
	}
}

func TestServerTLSStalledHandshakeShutdownReleasesCredits(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	routes := NewRouteTable()
	if err := routes.Upsert(Route{PublicPort: listener.Addr().(*net.TCPAddr).Port,
		AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 9000,
		Protocol: "tcp", TLSHostname: "echo.example"}); err != nil {
		t.Fatal(err)
	}
	var admissions atomic.Int32
	limiter := NewConnectionLimiter(1)
	server := &Server{Listener: listener, Routes: routes, MaxConnections: 1,
		Limiter: limiter, connectionSlots: make(chan struct{}, 1),
		Certificates: &testCertificateProvider{certificate: testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))},
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
			admissions.Add(1)
			return gateway.Target{}, nil
		}),
		Forwarder: forwarderFunc(func(context.Context, net.Conn, gateway.Target) error {
			return nil
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	raw, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Send no ClientHello. Wait until the server owns the account credit,
	// proving shutdown occurs during the handshake rather than before accept.
	deadline := time.Now().Add(time.Second)
	for limiter.Current("account") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("stalled connection never entered TLS handshake")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for the TLS handshake timeout")
	}
	if admissions.Load() != 0 || len(server.connectionSlots) != 0 {
		t.Fatalf("stalled handshake woke app or leaked global credit: admissions=%d slots=%d", admissions.Load(), len(server.connectionSlots))
	}
	release, available := limiter.Acquire("account")
	if !available {
		t.Fatal("shutdown leaked account credit")
	}
	release()
	if err := raw.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	_, err = raw.Read(data[:])
	var timeout net.Error
	if err == nil {
		t.Fatal("shutdown retained stalled socket")
	} else if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("stalled socket only stopped at client deadline")
	}
}
