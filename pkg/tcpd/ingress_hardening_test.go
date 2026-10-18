package tcpd

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDescriptorExhaustionIsATemporaryAcceptError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept4", syscall.EMFILE)}, true},
		{&net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept4", syscall.ENFILE)}, true},
		{net.ErrClosed, false},
		{errors.New("listener broken"), false},
	} {
		if got := isTemporaryAcceptError(tc.err); got != tc.want {
			t.Fatalf("isTemporaryAcceptError(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
	wait := time.Duration(0)
	for i := 0; i < 20; i++ {
		wait = nextAcceptBackoff(wait)
	}
	if wait != acceptBackoffMax {
		t.Fatalf("backoff = %v, want it capped at %v", wait, acceptBackoffMax)
	}
}

// failingListener fails Accept the way an unrecoverable listener does.
type failingListener struct {
	net.Listener
	err error
}

func (l failingListener) Accept() (net.Conn, error) { return nil, l.err }

// A port whose server stopped on its own is rebound on the next refresh
// instead of staying closed until the listener changes.
func TestSupervisorRebindsAStoppedListener(t *testing.T) {
	var binds atomic.Int32
	rebound := make(chan struct{})
	supervisor := &Supervisor{
		BindHost: "127.0.0.1",
		Source: supervisorSourceFunc(func(context.Context) ([]state.TCPListener, error) {
			return []state.TCPListener{{
				AppID: "app-1", AccountID: "acct-1", ListenerName: "echo",
				GuestPort: 8080, PublicPort: 40125, Protocol: "tcp", Enabled: true,
			}}, nil
		}),
		Routes:          NewRouteTable(),
		Targets:         targetResolverFunc(func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }),
		Forwarder:       forwarderFunc(func(context.Context, net.Conn, gateway.Target) error { return nil }),
		RefreshInterval: 20 * time.Millisecond,
		OnError:         func(error) {},
		Listen: func(_, _ string) (net.Listener, error) {
			base, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			if binds.Add(1) == 1 {
				return failingListener{Listener: base, err: errors.New("listener broken")}, nil
			}
			select {
			case <-rebound:
			default:
				close(rebound)
			}
			return base, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	select {
	case <-rebound:
	case err := <-done:
		t.Fatalf("Serve returned: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("stopped listener was not rebound")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve after cancellation: %v", err)
	}
}

type countingRouteResolver struct {
	calls atomic.Int32
	err   error
}

func (r *countingRouteResolver) Resolve(context.Context, int) (Route, bool, error) {
	r.calls.Add(1)
	if r.err != nil {
		return Route{}, false, r.err
	}
	return Route{PublicPort: 40001, AppID: "app"}, true, nil
}

func TestCachedRouteResolverReusesLookupsWithinTTL(t *testing.T) {
	next := &countingRouteResolver{}
	resolver := NewCachedRouteResolver(next, time.Minute)
	now := time.Unix(1000, 0)
	resolver.cache.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		route, ok, err := resolver.Resolve(context.Background(), 40001)
		if err != nil || !ok || route.AppID != "app" {
			t.Fatalf("Resolve = %+v, %v, %v", route, ok, err)
		}
	}
	if next.calls.Load() != 1 {
		t.Fatalf("lookups = %d, want 1 within the TTL", next.calls.Load())
	}
	now = now.Add(time.Minute)
	if _, _, err := resolver.Resolve(context.Background(), 40001); err != nil {
		t.Fatal(err)
	}
	if next.calls.Load() != 2 {
		t.Fatalf("lookups = %d, want a fresh lookup after the TTL", next.calls.Load())
	}
}

func TestCachedRouteResolverDoesNotCacheErrors(t *testing.T) {
	next := &countingRouteResolver{err: errors.New("database unavailable")}
	resolver := NewCachedRouteResolver(next, time.Minute)
	for i := 0; i < 2; i++ {
		if _, _, err := resolver.Resolve(context.Background(), 40001); err == nil {
			t.Fatal("Resolve hid the lookup error")
		}
	}
	if next.calls.Load() != 2 {
		t.Fatalf("lookups = %d, want every failed lookup retried", next.calls.Load())
	}
}

type countingInstanceSource struct {
	apps, listeners, domains, deployments, instances atomic.Int32
}

func (s *countingInstanceSource) AppByID(context.Context, string) (state.App, error) {
	s.apps.Add(1)
	return state.App{ID: "app"}, nil
}

func (s *countingInstanceSource) TCPListenerByAppAndName(context.Context, string, string) (state.TCPListener, error) {
	s.listeners.Add(1)
	return state.TCPListener{ID: "listener"}, nil
}

func (s *countingInstanceSource) DomainByName(context.Context, string) (state.CustomDomain, error) {
	s.domains.Add(1)
	return state.CustomDomain{}, nil
}

func (s *countingInstanceSource) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	s.deployments.Add(1)
	return nil, nil
}

func (s *countingInstanceSource) ListInstancesForApp(context.Context, string) ([]state.Instance, error) {
	s.instances.Add(1)
	return nil, nil
}

// Domain and deployment reads are cached. The app and listener-intent reads,
// which reject stale routes, and instance selection always read fresh state.
func TestCachedInstanceSourceKeepsAdmissionChecksFresh(t *testing.T) {
	next := &countingInstanceSource{}
	source := NewCachedInstanceSource(next, time.Minute)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = source.AppByID(ctx, "app")
		_, _ = source.TCPListenerByAppAndName(ctx, "app", "echo")
		_, _ = source.DomainByName(ctx, "example.com")
		_, _ = source.LiveDeployments(ctx, "app")
		_, _ = source.ListInstancesForApp(ctx, "app")
	}
	for name, tc := range map[string]struct{ got, want int32 }{
		"domain":      {next.domains.Load(), 1},
		"deployments": {next.deployments.Load(), 1},
		"app":         {next.apps.Load(), 3},
		"listener":    {next.listeners.Load(), 3},
		"instances":   {next.instances.Load(), 3},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s lookups = %d, want %d", name, tc.got, tc.want)
		}
	}
}
