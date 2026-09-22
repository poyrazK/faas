package tcpd

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestServerRoutesConnectionToGuestPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	publicPort := listener.Addr().(*net.TCPAddr).Port

	route := Route{
		PublicPort:   publicPort,
		AppID:        "app-1",
		AccountID:    "acct-1",
		ListenerName: "postgres",
		GuestPort:    5432,
		Protocol:     "tcp",
	}
	routes := NewRouteTable()
	if err := routes.Upsert(route); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var got gateway.Target
	var targetMu sync.Mutex
	targets := targetResolverFunc(func(_ context.Context, gotRoute Route) (gateway.Target, error) {
		if gotRoute != route {
			return gateway.Target{}, errors.New("unexpected route")
		}
		return gateway.Target{NodeID: "node-1", InstanceID: "instance-1"}, nil
	})
	forwarder := forwarderFunc(func(_ context.Context, conn net.Conn, target gateway.Target) error {
		targetMu.Lock()
		got = target
		targetMu.Unlock()
		_, err := io.WriteString(conn, "ok")
		return err
	})
	server := &Server{Listener: listener, Routes: routes, Targets: targets, Forwarder: forwarder}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	response := make([]byte, 2)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(response) != "ok" {
		t.Fatalf("response = %q, want ok", response)
	}
	targetMu.Lock()
	defer targetMu.Unlock()
	if got.AppID != route.AppID || got.Port != route.GuestPort || got.NodeID != "node-1" || got.InstanceID != "instance-1" {
		t.Fatalf("target = %#v, want app/guest port/node/instance from route", got)
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop after cancellation")
	}
}

func TestServerRejectsConnectionWhenAccountQuotaIsFull(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	publicPort := listener.Addr().(*net.TCPAddr).Port
	route := Route{PublicPort: publicPort, AppID: "app-1", AccountID: "acct-1", ListenerName: "echo", GuestPort: 5432, Protocol: "tcp"}
	routes := NewRouteTable()
	if err := routes.Upsert(route); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	gate := make(chan struct{})
	started := make(chan struct{})
	server := &Server{
		Listener: listener,
		Routes:   routes,
		Targets:  targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
		Forwarder: forwarderFunc(func(context.Context, net.Conn, gateway.Target) error {
			close(started)
			<-gate
			return nil
		}),
		Limiter: NewConnectionLimiter(1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()

	first, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("first Dial: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first connection did not reach the forwarder")
	}
	second, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("second Dial: %v", err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	var one [1]byte
	if _, err := second.Read(one[:]); err == nil {
		t.Fatal("quota-rejected connection unexpectedly received data")
	}
	close(gate)
	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve after cancellation: %v", err)
	}
}

func TestServerReportsUnknownRouteAndContinues(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverErrors := make(chan error, 1)
	server := &Server{
		Listener:  listener,
		Routes:    NewRouteTable(),
		Targets:   targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
		Forwarder: forwarderFunc(func(context.Context, net.Conn, gateway.Target) error { return nil }),
		OnError:   func(err error) { serverErrors <- err },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var one [1]byte
	_, readErr := conn.Read(one[:])
	if readErr == nil {
		t.Fatal("Read unexpectedly succeeded for unknown route")
	}
	select {
	case err := <-serverErrors:
		if !errors.Is(err, ErrNoRoute) {
			t.Fatalf("reported error = %v, want ErrNoRoute", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not report unknown route")
	}
	cancel()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve after cancellation: %v", err)
	}
}

type targetResolverFunc func(context.Context, Route) (gateway.Target, error)

func (f targetResolverFunc) ResolveTarget(ctx context.Context, route Route) (gateway.Target, error) {
	return f(ctx, route)
}

type forwarderFunc func(context.Context, net.Conn, gateway.Target) error

func (f forwarderFunc) ServeConn(ctx context.Context, conn net.Conn, target gateway.Target) error {
	return f(ctx, conn, target)
}
