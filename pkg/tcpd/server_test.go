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

func TestServersShareGlobalConnectionLimitAcrossPorts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slots := make(chan struct{}, 1)
	entered := make(chan struct{}, 2)
	done := make(chan error, 2)
	var addresses []string
	for i := 0; i < 2; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		port := listener.Addr().(*net.TCPAddr).Port
		routes := NewRouteTable()
		if err := routes.Upsert(Route{PublicPort: port, AppID: "app", ListenerName: "echo", GuestPort: 8080, Protocol: "tcp"}); err != nil {
			t.Fatal(err)
		}
		server := &Server{Listener: listener, Routes: routes, connectionSlots: slots, MaxConnections: 1,
			Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
			Forwarder: forwarderFunc(func(_ context.Context, conn net.Conn, _ gateway.Target) error {
				entered <- struct{}{}
				if _, err := io.WriteString(conn, "accepted"); err != nil {
					return err
				}
				_, err := io.Copy(io.Discard, conn)
				return err
			})}
		addresses = append(addresses, listener.Addr().String())
		go func() { done <- server.Serve(ctx) }()
	}
	first, err := net.Dial("tcp", addresses[0])
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first session not admitted")
	}
	second, err := net.Dial("tcp", addresses[1])
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	var timeout net.Error
	if _, err := second.Read(one[:]); err == nil {
		t.Fatal("excess session returned data")
	} else if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatal("excess session remained open")
	}
	select {
	case <-entered:
		t.Fatal("second port bypassed global cap")
	default:
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Observe admission over the socket rather than inspecting semaphore state.
	admitted := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		retry, err := net.DialTimeout("tcp", addresses[1], time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := retry.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			_ = retry.Close()
			t.Fatal(err)
		}
		var response [8]byte
		_, err = io.ReadFull(retry, response[:])
		_ = retry.Close()
		if err == nil {
			if string(response[:]) != "accepted" {
				t.Fatalf("unexpected admission response: %q", response)
			}
			admitted = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !admitted {
		t.Fatal("completed session did not release capacity for another listener")
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("server failed to stop")
		}
	}
}
