// adr: 417
package tcpd

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type heldCloseListener struct {
	net.Listener
	closing, release, closed chan struct{}
	once                     sync.Once
	err                      error
}

func (l *heldCloseListener) Close() error {
	l.once.Do(func() {
		close(l.closing)
		<-l.release
		l.err = l.Listener.Close()
		close(l.closed)
	})
	return l.err
}

func TestSupervisorDisableClosesSocketBeforeCancelingSessions(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const port = 40126
	listener := &heldCloseListener{Listener: &aliasedTCPListener{Listener: base, port: port}, closing: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(listener.release) }) }
	route := Route{PublicPort: port, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 8080, Protocol: "tcp"}
	routes := NewRouteTable()
	if err := routes.Upsert(route); err != nil {
		t.Fatal(err)
	}
	var enabled atomic.Bool
	enabled.Store(true)
	started := make(chan context.Context, 1)
	supervisor := &Supervisor{
		Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) {
			if !enabled.Load() {
				return nil, nil
			}
			return []state.TCPListener{{AppID: route.AppID, AccountID: route.AccountID, ListenerName: route.ListenerName, GuestPort: route.GuestPort, PublicPort: port, Protocol: "tcp", Enabled: true}}, nil
		}),
		Routes: routes,
		Targets: targetResolverFunc(func(context.Context, Route) (gateway.Target, error) {
			return gateway.Target{}, nil
		}),
		Forwarder: forwarderFunc(func(ctx context.Context, _ net.Conn, _ gateway.Target) error {
			started <- ctx
			<-ctx.Done()
			return ctx.Err()
		}),
		Listen:          func(string, string) (net.Listener, error) { return listener, nil },
		RefreshInterval: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	defer func() {
		release()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("supervisor shutdown hung")
		}
	}()
	conn, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var session context.Context
	select {
	case session = <-started:
	case <-time.After(time.Second):
		t.Fatal("session did not enter forwarding")
	}
	enabled.Store(false)
	select {
	case <-listener.closing:
	case <-time.After(time.Second):
		t.Fatal("disable did not close listener")
	}
	if session.Err() != nil {
		t.Fatal("disable canceled sessions while the public socket was still bound")
	}
	release()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("disable did not cancel session after closing socket")
	}
	select {
	case <-listener.closed:
	default:
		t.Fatal("session cancellation preceded completed socket close")
	}
	probe, err := net.Listen("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("disabled public port still bound: %v", err)
	}
	_ = probe.Close()
}
