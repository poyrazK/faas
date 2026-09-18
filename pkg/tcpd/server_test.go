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
