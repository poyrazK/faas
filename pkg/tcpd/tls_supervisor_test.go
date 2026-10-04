package tcpd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorTLSNegotiatesBeforeTargetSelection(t *testing.T) {
	var listener net.Listener
	var err error
	for port := state.TCPListenerPublicPortMin; port <= state.TCPListenerPublicPortMax; port++ {
		listener, err = net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	row := state.TCPListener{ID: "listener", AppID: "app", AccountID: "account", ListenerName: "echo", PublicPort: port, GuestPort: 9000, Protocol: "tcp", Enabled: true, TLSMode: api.TCPListenerTLSTerminate, TLSHostname: "echo.example"}
	route, err := routeFromListener(row)
	if err != nil {
		t.Fatal(err)
	}
	routes := NewRouteTable()
	if err := routes.Upsert(route); err != nil {
		t.Fatal(err)
	}
	certificate := testListenerCertificate(t, "echo.example", time.Now().Add(time.Hour))
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	var selections atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	supervisor := Supervisor{Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) { return []state.TCPListener{row}, nil }), Routes: routes, Certificates: &testCertificateProvider{certificate: certificate}, Listen: func(string, string) (net.Listener, error) { return listener, nil },
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
			selections.Add(1)
			return gateway.Target{AppID: "app", InstanceID: "instance", NodeID: "node"}, nil
		}),
		Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, _ gateway.Target) error {
			_, err := io.WriteString(conn, "OK")
			return err
		}),
	}
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("supervisor shutdown hung")
		}
	}()
	dial := func(name string) (*tls.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", listener.Addr().String(), &tls.Config{ServerName: name, RootCAs: roots, MinVersion: tls.VersionTLS12})
	}
	if conn, err := dial("wrong.example"); err == nil {
		_ = conn.Close()
		t.Fatal("wrong SNI accepted")
	}
	if selections.Load() != 0 {
		t.Fatal("invalid TLS reached target selection")
	}
	conn, err := dial("echo.example")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || string(reply) != "OK" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if selections.Load() != 1 {
		t.Fatalf("target selections=%d", selections.Load())
	}
}
