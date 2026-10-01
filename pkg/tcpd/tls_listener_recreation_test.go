package tcpd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type gatedListenerCertificate struct {
	certificate *tls.Certificate
	entered     chan struct{}
	release     chan struct{}
	calls       atomic.Int32
}

func (p *gatedListenerCertificate) Certificate(ctx context.Context, _ string) (*tls.Certificate, error) {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.certificate, nil
}

// Pause a real TLS handshake after durable route lookup. Recreating an
// otherwise identical listener must invalidate that route before admission.
func TestTLSListenerRecreationDuringHandshakeDoesNotWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tls-recreation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tls-recreation", Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "echo.example", app.ID, "challenge"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "echo.example"); err != nil {
		t.Fatal(err)
	}
	const port = 40125
	makeIntent := func() state.TCPListener {
		t.Helper()
		intent, err := store.CreateTCPListener(ctx, state.TCPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "echo", GuestPort: 9000, PublicPort: port, Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"})
		if err != nil {
			t.Fatal(err)
		}
		return intent
	}
	old := makeIntent()
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	provider := &gatedListenerCertificate{certificate: certificate, entered: make(chan struct{}), release: make(chan struct{})}
	admit := &tlsIngressAdmitter{store: store}
	reported := make(chan error, 2)
	server := &Server{Listener: &aliasedTCPListener{Listener: base, port: port}, Routes: ListenerStoreResolver{Store: store}, Targets: &StoreTargetResolver{Instances: store, Admitter: admit}, Certificates: provider, MaxConnections: 1, connectionSlots: make(chan struct{}, 1), Limiter: NewConnectionLimiter(1), OnError: func(err error) { reported <- err }, Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, _ gateway.Target) error {
		_, err := io.WriteString(conn, "replacement-ready")
		return err
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("server shutdown hung")
		}
	}()
	config := &tls.Config{RootCAs: roots, ServerName: "echo.example", MinVersion: tls.VersionTLS12}
	first, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	stale := tls.Client(first, config)
	handshake := make(chan error, 1)
	go func() { handshake <- stale.HandshakeContext(ctx) }()
	select {
	case <-provider.entered:
	case <-time.After(time.Second):
		t.Fatal("handshake did not reach provider")
	}
	if err := store.DeleteTCPListener(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	replacement := makeIntent()
	if replacement.ID == old.ID {
		t.Fatal("replacement reused listener identity")
	}
	close(provider.release)
	select {
	case <-handshake:
	case <-time.After(time.Second):
		t.Fatal("handshake hung")
	}
	select {
	case err := <-reported:
		if !strings.Contains(err.Error(), "disabled or changed") {
			t.Fatalf("unexpected failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stale listener was not rejected")
	}
	if admit.calls.Load() != 0 {
		t.Fatal("stale handshake woke application")
	}
	if err := stale.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := stale.Read(b[:]); err == nil {
		t.Fatal("stale connection stayed open")
	} else {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatal("stale connection was not closed")
		}
	}
	deadline := time.Now().Add(time.Second)
	for len(server.connectionSlots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("stale handshake retained connection credit")
		}
		time.Sleep(time.Millisecond)
	}
	current, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", base.Addr().String(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err := current.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(current)
	if err != nil || string(reply) != "replacement-ready" || admit.calls.Load() != 1 {
		t.Fatalf("replacement reply=%q err=%v wakes=%d", reply, err, admit.calls.Load())
	}
}
