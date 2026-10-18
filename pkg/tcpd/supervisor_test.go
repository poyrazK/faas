package tcpd

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSupervisorReadyOnlyAfterInitialListenersBind(t *testing.T) {
	const publicPort = 40124
	newSupervisor := func(listen func(string, string) (net.Listener, error), onReady func()) *Supervisor {
		return &Supervisor{
			BindHost: "127.0.0.1",
			Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) {
				return []state.TCPListener{{
					AppID: "app-1", AccountID: "acct-1", ListenerName: "echo",
					GuestPort: 8080, PublicPort: publicPort, Protocol: "tcp", Enabled: true,
				}}, nil
			}),
			Routes: NewRouteTable(),
			Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
				return gateway.Target{}, nil
			}),
			Forwarder:       forwarderFunc(func(context.Context, net.Conn, gateway.Target) error { return nil }),
			RefreshInterval: time.Hour,
			Listen:          listen,
			OnReady:         onReady,
		}
	}

	t.Run("ready after bind", func(t *testing.T) {
		ready := make(chan int, 1)
		binds := 0
		supervisor := newSupervisor(func(_, _ string) (net.Listener, error) {
			binds++
			return net.Listen("tcp", "127.0.0.1:0")
		}, func() { ready <- binds })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- supervisor.Serve(ctx) }()
		select {
		case got := <-ready:
			if got != 1 {
				t.Fatalf("ready after %d binds, want 1", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("supervisor never reported initial readiness")
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Serve after cancellation: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("supervisor did not stop after cancellation")
		}
	})

	// One port that cannot be bound (another process holds it, as during a
	// rolling restart) must not fail gatewayd-public's startup or keep the
	// other listeners down. It is reported and retried on the next refresh.
	t.Run("bind failure is reported and retried", func(t *testing.T) {
		bindErr := errors.New("port already bound")
		var attempts atomic.Int32
		bound := make(chan struct{})
		supervisor := newSupervisor(func(_, _ string) (net.Listener, error) {
			if attempts.Add(1) == 1 {
				return nil, bindErr
			}
			defer close(bound)
			return net.Listen("tcp", "127.0.0.1:0")
		}, nil)
		ready := make(chan struct{})
		supervisor.OnReady = func() { close(ready) }
		reported := make(chan error, 4)
		supervisor.OnError = func(err error) { reported <- err }
		supervisor.RefreshInterval = 20 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- supervisor.Serve(ctx) }()
		for _, step := range []struct {
			name string
			ch   <-chan struct{}
		}{{"ready", ready}, {"rebind", bound}} {
			select {
			case <-step.ch:
			case err := <-done:
				t.Fatalf("Serve returned before %s: %v", step.name, err)
			case <-time.After(2 * time.Second):
				t.Fatalf("supervisor did not reach %s", step.name)
			}
		}
		if err := <-reported; !errors.Is(err, bindErr) {
			t.Fatalf("reported error = %v, want the bind failure", err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Serve after cancellation: %v", err)
		}
	})
}

func TestSupervisorDrainStopsAcceptsAndWaitsForActiveSessions(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer func() { _ = base.Close() }()
	publicPort := 40123
	listener := &aliasedTCPListener{Listener: base, port: publicPort}

	route := Route{
		PublicPort:   publicPort,
		AppID:        "app-1",
		AccountID:    "acct-1",
		ListenerName: "echo",
		GuestPort:    8080,
		Protocol:     "tcp",
	}
	routes := NewRouteTable()
	if err := routes.Upsert(route); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	forwardCanceled := make(chan struct{})
	serveErrors := make(chan error, 1)
	supervisor := &Supervisor{
		BindHost: "127.0.0.1",
		Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) {
			return []state.TCPListener{{
				AppID:        route.AppID,
				AccountID:    route.AccountID,
				ListenerName: route.ListenerName,
				GuestPort:    route.GuestPort,
				PublicPort:   route.PublicPort,
				Protocol:     route.Protocol,
				Enabled:      true,
			}}, nil
		}),
		Routes:  routes,
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
		Forwarder: forwarderFunc(func(ctx context.Context, conn net.Conn, _ gateway.Target) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				close(forwardCanceled)
				return ctx.Err()
			}
		}),
		RefreshInterval: time.Hour,
		OnError:         func(err error) { serveErrors <- err },
		Listen: func(string, string) (net.Listener, error) {
			return listener, nil
		},
	}

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- supervisor.Serve(serveCtx) }()

	conn, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("connection did not reach the forwarder")
	}

	drainDone := make(chan error, 1)
	go func() { drainDone <- supervisor.Drain(context.Background()) }()
	select {
	case err := <-drainDone:
		t.Fatalf("Drain returned before active session finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-forwardCanceled:
		t.Fatal("Drain canceled an active session before its grace ended")
	default:
	}

	close(release)
	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("Drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Drain did not finish after active session completed")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve after drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop after drain")
	}
	select {
	case err := <-serveErrors:
		t.Fatalf("graceful drain reported an error: %v", err)
	default:
	}
}

type supervisorSourceFunc func(context.Context) ([]state.TCPListener, error)

func (f supervisorSourceFunc) ListEnabledTCPListeners(ctx context.Context) ([]state.TCPListener, error) {
	return f(ctx)
}

type aliasedTCPListener struct {
	net.Listener
	port int
}

func (l *aliasedTCPListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &aliasedTCPConn{Conn: conn, local: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: l.port}}, nil
}

type aliasedTCPConn struct {
	net.Conn
	local net.Addr
}

func (c *aliasedTCPConn) LocalAddr() net.Addr { return c.local }

func TestSupervisorReassignmentCancelsPreviousOwnerSessions(t *testing.T) {
	const port = 40125
	var mu sync.Mutex
	owner := "app-old"
	entered, canceled := make(chan struct{}), make(chan struct{})
	ready := make(chan string, 4)
	var address string
	routes := NewRouteTable()
	if err := routes.Upsert(Route{PublicPort: port, AppID: owner, AccountID: "acct", ListenerName: "echo", GuestPort: 8080, Protocol: "tcp"}); err != nil {
		t.Fatal(err)
	}
	supervisor := &Supervisor{
		Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) {
			mu.Lock()
			defer mu.Unlock()
			return []state.TCPListener{{AppID: owner, AccountID: "acct", ListenerName: "echo", GuestPort: 8080, PublicPort: port, Protocol: "tcp", Enabled: true}}, nil
		}), Routes: routes,
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
		Forwarder: forwarderFunc(func(ctx context.Context, _ net.Conn, _ gateway.Target) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return ctx.Err()
		}),
		RefreshInterval: 5 * time.Millisecond,
		Listen: func(_, _ string) (net.Listener, error) {
			base, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			ready <- base.Addr().String()
			return &aliasedTCPListener{Listener: base, port: port}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	select {
	case address = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("initial bind missing")
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not enter forwarding")
	}
	mu.Lock()
	owner = "app-new"
	mu.Unlock()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("previous owner session survived port reassignment")
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("new owner listener was not rebound")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorRecreationCancelsPreviousIdentitySessions(t *testing.T) {
	const port = 40125
	var mu sync.Mutex
	owner := "app-old"
	entered, canceled := make(chan struct{}), make(chan struct{})
	ready := make(chan string, 4)
	var address string
	routes := NewRouteTable()
	if err := routes.Upsert(Route{ListenerID: owner, AccountID: "acct", PublicPort: port, AppID: "app", ListenerName: "echo", GuestPort: 8080, Protocol: "tcp"}); err != nil {
		t.Fatal(err)
	}
	supervisor := &Supervisor{
		Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) {
			mu.Lock()
			defer mu.Unlock()
			return []state.TCPListener{{ID: owner, AppID: "app", AccountID: "acct", ListenerName: "echo", GuestPort: 8080, PublicPort: port, Protocol: "tcp", Enabled: true}}, nil
		}), Routes: routes,
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
		Forwarder: forwarderFunc(func(ctx context.Context, _ net.Conn, _ gateway.Target) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return ctx.Err()
		}),
		RefreshInterval: 5 * time.Millisecond,
		Listen: func(_, _ string) (net.Listener, error) {
			base, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			ready <- base.Addr().String()
			return &aliasedTCPListener{Listener: base, port: port}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	select {
	case address = <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("initial bind missing")
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("session did not enter forwarding")
	}
	mu.Lock()
	owner = "app-new"
	mu.Unlock()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("previous identity session survived listener recreation")
	}
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement listener was not rebound")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}
