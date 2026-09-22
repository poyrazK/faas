package tcpd

import (
	"context"
	"errors"
	"net"
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

	t.Run("bind failure does not report ready", func(t *testing.T) {
		bindErr := errors.New("port already bound")
		ready := false
		supervisor := newSupervisor(func(_, _ string) (net.Listener, error) {
			return nil, bindErr
		}, func() { ready = true })
		if err := supervisor.Serve(context.Background()); !errors.Is(err, bindErr) {
			t.Fatalf("Serve error = %v, want bind failure", err)
		}
		if ready {
			t.Fatal("supervisor reported ready despite a failed initial bind")
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
