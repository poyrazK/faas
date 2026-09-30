package tcpd

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tcpmetrics"
)

type tlsIngressAdmitter struct {
	store *state.MemStore
	calls atomic.Int32
}

func (a *tlsIngressAdmitter) AdmitInstance(ctx context.Context, app, _, _, _ string) (string, string, string, string, int32, bool, int, error) {
	a.calls.Add(1)
	instance, err := a.store.CreateInstance(ctx, app, "deployment", string(state.StateRunning), 256, "node", "wake")
	return instance.ID, "node", "deployment", "wake", 0, false, 9000, err
}

// Real TLS/public sockets, durable in-memory intent and domain state, supervisor,
// resolver and file provisioning are composed here. Scheduler/guest execution
// are substituted; this is not native VM acceptance.
func TestTLSIngressRotationAndDisable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "tls-ingress@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "tls-ingress", Status: state.AppActive, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "echo.example", app.ID, "challenge"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "echo.example"); err != nil {
		t.Fatal(err)
	}
	var base net.Listener
	for port := state.TCPListenerPublicPortMin; port <= state.TCPListenerPublicPortMax; port++ {
		base, err = net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal("no free public TCP port")
	}
	defer base.Close()
	address := base.Addr().String()
	intent, err := store.CreateTCPListener(ctx, state.TCPListener{AppID: app.ID, AccountID: account.ID, ListenerName: "echo", GuestPort: 9000, PublicPort: base.Addr().(*net.TCPAddr).Port, Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	provider, err := NewFileCertificateProvider(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	roots := x509.NewCertPool()
	provision := func() []byte {
		t.Helper()
		certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
		leaf, err := x509.ParseCertificate(certificate.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		roots.AddCert(leaf)
		key, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		bundle := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})...)
		staged := filepath.Join(directory, "staged.pem")
		if err := os.WriteFile(staged, bundle, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, filepath.Join(directory, "echo.example.pem")); err != nil {
			t.Fatal(err)
		}
		return leaf.Raw
	}
	metrics := tcpmetrics.New(nil, "tls_test")
	assertGauge := func(name string, expected float64) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			families, err := metrics.Registry().Gather()
			if err != nil {
				t.Fatal(err)
			}
			for _, family := range families {
				if family.GetName() == name && len(family.Metric) == 1 && family.Metric[0].GetGauge().GetValue() == expected {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("metric %s did not reach %v", name, expected)
			}
			time.Sleep(time.Millisecond)
		}
	}
	admit := &tlsIngressAdmitter{store: store}
	ready := make(chan struct{})
	done := make(chan error, 1)
	supervisor := &Supervisor{BindHost: "127.0.0.1", Source: store, Routes: ListenerStoreResolver{Store: store}, Targets: &StoreTargetResolver{Instances: store, Admitter: admit}, Certificates: provider, RefreshInterval: 5 * time.Millisecond, Metrics: metrics,
		Observations: store, EdgeID: "edge-test",
		Listen: func(string, string) (net.Listener, error) { return base, nil }, OnReady: func() { close(ready) },
		Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, _ gateway.Target) error {
			_, err := io.Copy(conn, conn)
			return err
		}),
	}
	go func() { done <- supervisor.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("TLS ingress shutdown hung")
		}
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("TLS listener not ready")
	}
	assertGauge("tls_test_tcp_tls_listeners_not_ready", 1)
	assertGauge("tls_test_tcp_tls_listeners_ready", 0)
	assertTLSObservation := func(expected string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			listener, err := store.TCPListenerByID(ctx, intent.ID)
			if err != nil {
				t.Fatal(err)
			}
			observations, err := store.ListTCPListenerTLSObservations(ctx, intent.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(observations) == 1 && observations[0].EdgeID == "edge-test" && observations[0].Status(listener, time.Now()) == expected {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("certificate evidence did not become %s: %+v", expected, observations)
			}
			time.Sleep(time.Millisecond)
		}
	}
	assertTLSObservation("not_ready")
	firstCertificate := provision()
	assertGauge("tls_test_tcp_tls_listeners_ready", 1)
	assertGauge("tls_test_tcp_tls_listeners_not_ready", 0)
	assertTLSObservation("ready")
	dial := func() *tls.Conn {
		t.Helper()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", address, &tls.Config{ServerName: "echo.example", RootCAs: roots.Clone(), MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	echo := func(conn *tls.Conn, payload string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := io.WriteString(conn, payload); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, len(payload))
		if _, err := io.ReadFull(conn, buffer); err != nil || string(buffer) != payload {
			t.Fatalf("echo=%q err=%v", buffer, err)
		}
	}
	first := dial()
	defer first.Close()
	echo(first, "before-rotation")
	if !bytes.Equal(first.ConnectionState().PeerCertificates[0].Raw, firstCertificate) {
		t.Fatal("wrong initial certificate")
	}
	secondCertificate := provision()
	second := dial()
	defer second.Close()
	echo(second, "after-rotation")
	echo(first, "existing-session-survives")
	if !bytes.Equal(second.ConnectionState().PeerCertificates[0].Raw, secondCertificate) {
		t.Fatal("new handshake retained old certificate")
	}
	if admit.calls.Load() != 1 {
		t.Fatalf("admissions=%d, want running reuse", admit.calls.Load())
	}
	if _, err := store.SetTCPListenerEnabled(ctx, intent.ID, false); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*tls.Conn{first, second} {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var timeout net.Error
		if _, err := conn.Read(make([]byte, 1)); err == nil {
			t.Fatal("disabled listener retained session")
		} else if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatal("disable did not close TLS session")
		}
	}
	probe, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("disabled public port still bound: %v", err)
	}
	_ = probe.Close()
	assertGauge("tls_test_tcp_tls_listeners_ready", 0)
	assertGauge("tls_test_tcp_tls_certificate_expiry_seconds", 0)
	assertTLSObservation("unknown")
}
