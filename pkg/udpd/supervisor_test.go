package udpd

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type udpIntentSource struct {
	mu   sync.Mutex
	rows []state.UDPListener
}

func (s *udpIntentSource) ListEnabledUDPListeners(context.Context) ([]state.UDPListener, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.UDPListener(nil), s.rows...), nil
}
func (s *udpIntentSource) set(rows ...state.UDPListener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = rows
}

type peerObservation struct {
	target gateway.Target
	ctx    context.Context
}
type observingUDPForwarder struct{ started chan peerObservation }

func (f observingUDPForwarder) ServePeer(ctx context.Context, peer gateway.DatagramPeer, target gateway.Target) error {
	f.started <- peerObservation{target, ctx}
	return (echoForwarder{}).ServePeer(ctx, peer, target)
}
func udpIntent(id, app, account string, port int) state.UDPListener {
	return state.UDPListener{ID: id, AppID: app, AccountID: account, ListenerName: "echo", GuestPort: 5353, PublicPort: port, Protocol: "udp", Enabled: true}
}

func TestSupervisorReassignmentStopsPreviousOwner(t *testing.T) {
	source := &udpIntentSource{}
	source.set(udpIntent("old", "old-app", "old-account", api.UDPListenerPublicPortMin))
	bound := make(chan *net.UDPConn, 4)
	observations := make(chan peerObservation, 4)
	physicalAddress := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	supervisor := &Supervisor{Source: source, RefreshInterval: 5 * time.Millisecond, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Forwarder: observingUDPForwarder{observations}, ResolveTarget: func(_ context.Context, r Route) (gateway.Target, error) {
		return gateway.Target{AppID: r.AppID, InstanceID: r.AppID, NodeID: "node"}, nil
	}, Listen: func(_ string, _ *net.UDPAddr) (*net.UDPConn, error) {
		socket, err := net.ListenUDP("udp4", physicalAddress)
		if err == nil {
			physicalAddress = socket.LocalAddr().(*net.UDPAddr)
			bound <- socket
		}
		return socket, err
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	nextSocket := func() *net.UDPConn {
		t.Helper()
		select {
		case socket := <-bound:
			return socket
		case <-time.After(time.Second):
			t.Fatal("listener not bound")
			return nil
		}
	}
	send := func(socket *net.UDPConn) *net.UDPConn {
		t.Helper()
		client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		if _, err := client.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		return client
	}
	observe := func() peerObservation {
		t.Helper()
		select {
		case event := <-observations:
			return event
		case <-time.After(time.Second):
			t.Fatal("peer not admitted")
			return peerObservation{}
		}
	}
	oldSocket := nextSocket()
	send(oldSocket)
	old := observe()
	if old.target.AppID != "old-app" {
		t.Fatal(old.target)
	}
	source.set(udpIntent("new", "new-app", "new-account", api.UDPListenerPublicPortMin))
	newSocket := nextSocket()
	// Binding replacement must wait until old peer cancellation has completed.
	if old.ctx.Err() == nil {
		t.Fatal("old owner still active after replacement bind")
	}
	send(newSocket)
	fresh := observe()
	if fresh.target.AppID != "new-app" {
		t.Fatal(fresh.target)
	}
	source.set()
	select {
	case <-fresh.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("deleted endpoint retained peer")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor shutdown hung")
	}
}

func TestSupervisorPeerBudgetSharedAcrossPorts(t *testing.T) {
	source := &udpIntentSource{}
	source.set(udpIntent("one", "app", "account", api.UDPListenerPublicPortMin), udpIntent("two", "app", "account", api.UDPListenerPublicPortMin+1))
	bound := make(chan *net.UDPConn, 2)
	observations := make(chan peerObservation, 2)
	supervisor := &Supervisor{Source: source, Pool: NewPeerPool(1, 1), AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Forwarder: observingUDPForwarder{observations}, ResolveTarget: func(_ context.Context, r Route) (gateway.Target, error) {
		return gateway.Target{AppID: r.AppID, InstanceID: "guest", NodeID: "node"}, nil
	}, Listen: func(_ string, _ *net.UDPAddr) (*net.UDPConn, error) {
		socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err == nil {
			bound <- socket
		}
		return socket, err
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	var clients []*net.UDPConn
	for i := 0; i < 2; i++ {
		select {
		case socket := <-bound:
			client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			clients = append(clients, client)
			defer client.Close()
		case <-time.After(time.Second):
			t.Fatal("bind timed out")
		}
	}
	if _, err := clients[0].Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observations:
	case <-time.After(time.Second):
		t.Fatal("first peer not admitted")
	}
	if _, err := clients[1].Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observations:
		t.Fatal("second port multiplied peer budget")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timed out")
	}
}

// A canceled in-flight durable-state read is shutdown, not a reconciliation
// failure. This avoids emitting operational errors for a healthy teardown.
type cancelingUDPSource struct {
	calls   atomic.Int32
	entered chan struct{}
}

func (s *cancelingUDPSource) ListEnabledUDPListeners(ctx context.Context) ([]state.UDPListener, error) {
	if s.calls.Add(1) == 1 {
		return nil, nil
	}
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestSupervisorShutdownDoesNotReportReconciliationFailure(t *testing.T) {
	source := &cancelingUDPSource{entered: make(chan struct{})}
	metrics := NewMetrics(nil, "test")
	errorsReported := make(chan error, 1)
	supervisor := &Supervisor{Source: source, RefreshInterval: time.Millisecond, Metrics: metrics, Forwarder: echoForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }, OnError: func(err error) { errorsReported <- err }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("reconciliation did not enter source")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not complete")
	}
	select {
	case err := <-errorsReported:
		t.Fatalf("shutdown reported runtime error: %v", err)
	default:
	}
	assertMetric(t, metrics, "test_udp_reconciliation_errors_total", nil, 0)
}

// Initial shutdown must not bind sockets or count a canceled read as failure.
type initialCancelUDPSource struct{ entered chan struct{} }

func (s initialCancelUDPSource) ListEnabledUDPListeners(ctx context.Context) ([]state.UDPListener, error) {
	close(s.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestSupervisorInitialReadCancellationIsClean(t *testing.T) {
	entered := make(chan struct{})
	metrics := NewMetrics(nil, "test")
	supervisor := &Supervisor{Source: initialCancelUDPSource{entered}, Metrics: metrics,
		Forwarder: echoForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil },
		Listen: func(string, *net.UDPAddr) (*net.UDPConn, error) {
			t.Error("shutdown bound socket")
			return nil, context.Canceled
		},
		OnReady: func() { t.Error("canceled startup reported readiness") }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial read not entered")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung")
	}
	assertMetric(t, metrics, "test_udp_reconciliation_errors_total", nil, 0)
}

type deadlineUDPSource struct{ bounded bool }

func (s *deadlineUDPSource) ListEnabledUDPListeners(ctx context.Context) ([]state.UDPListener, error) {
	deadline, ok := ctx.Deadline()
	s.bounded = ok && time.Until(deadline) <= api.UDPListenerReadTimeout
	return nil, context.DeadlineExceeded
}
func TestSupervisorInitialReadHasDeadline(t *testing.T) {
	source := &deadlineUDPSource{}
	metrics := NewMetrics(nil, "test")
	supervisor := &Supervisor{Source: source, Metrics: metrics, Forwarder: echoForwarder{},
		ResolveTarget: func(context.Context, Route) (gateway.Target, error) { return gateway.Target{}, nil }}
	if err := supervisor.Serve(context.Background()); err == nil {
		t.Fatal("read failure accepted")
	}
	if !source.bounded {
		t.Fatal("intent read had no bounded deadline")
	}
	assertMetric(t, metrics, "test_udp_reconciliation_errors_total", nil, 1)
}

func TestSupervisorInvalidRefreshPreservesSocketThenRecovers(t *testing.T) {
	source := &udpIntentSource{}
	row := udpIntent("listener", "app", "account", api.UDPListenerPublicPortMin)
	source.set(row)
	bound := make(chan *net.UDPConn, 2)
	reported := make(chan error, 20)
	supervisor := &Supervisor{Source: source, RefreshInterval: 5 * time.Millisecond,
		Forwarder: echoForwarder{}, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
			return gateway.Target{AppID: "app", InstanceID: "guest", NodeID: "node"}, nil
		},
		OnError: func(err error) {
			select {
			case reported <- err:
			default:
			}
		},
		Listen: func(string, *net.UDPAddr) (*net.UDPConn, error) {
			socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err == nil {
				bound <- socket
			}
			return socket, err
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	var socket *net.UDPConn
	select {
	case socket = <-bound:
	case <-time.After(time.Second):
		t.Fatal("no initial bind")
	}
	client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	invalid := row
	invalid.GuestPort = 0
	source.set(invalid)
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("invalid refresh not reported")
	}
	if _, err := client.Write([]byte("retained")); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 20)
	n, err := client.Read(buf)
	if err != nil || string(buf[:n]) != "retained" {
		t.Fatalf("old socket lost: %q, %v", buf[:n], err)
	}
	source.set()
	deadline := time.Now().Add(time.Second)
	for {
		_, err := socket.WriteToUDP(nil, client.LocalAddr().(*net.UDPAddr))
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recovered refresh did not retire socket")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-bound:
		t.Fatal("invalid refresh rebound socket")
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung")
	}
}

// A failed socket must be joined and rebound without restarting the daemon.
func TestSupervisorRecoversClosedSocket(t *testing.T) {
	source := &udpIntentSource{}
	source.set(udpIntent("listener", "app", "account", api.UDPListenerPublicPortMin))
	bound := make(chan *net.UDPConn, 4)
	observations := make(chan peerObservation, 4)
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	supervisor := &Supervisor{Source: source, RefreshInterval: 5 * time.Millisecond,
		AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		Forwarder:      observingUDPForwarder{observations},
		ResolveTarget: func(_ context.Context, r Route) (gateway.Target, error) {
			return gateway.Target{AppID: r.AppID, InstanceID: "guest", NodeID: "node"}, nil
		}, Listen: func(_ string, _ *net.UDPAddr) (*net.UDPConn, error) {
			socket, err := net.ListenUDP("udp4", address)
			if err == nil {
				address = socket.LocalAddr().(*net.UDPAddr)
				bound <- socket
			}
			return socket, err
		}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("shutdown hung")
		}
	})
	next := func() *net.UDPConn {
		t.Helper()
		select {
		case socket := <-bound:
			return socket
		case <-time.After(time.Second):
			t.Fatal("socket not rebound")
			return nil
		}
	}
	first := next()
	client, err := net.DialUDP("udp4", nil, first.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	exchange := func(payload string) peerObservation {
		t.Helper()
		if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 64)
		n, err := client.Read(buffer)
		if err != nil || string(buffer[:n]) != payload {
			t.Fatalf("echo=%q err=%v", buffer[:n], err)
		}
		select {
		case observation := <-observations:
			return observation
		case <-time.After(time.Second):
			t.Fatal("peer not observed")
			return peerObservation{}
		}
	}
	old := exchange("before failure")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := next()
	if replacement.LocalAddr().String() != first.LocalAddr().String() {
		t.Fatal("public address changed")
	}
	if old.ctx.Err() == nil {
		t.Fatal("failed socket retained old peer")
	}
	fresh := exchange("after recovery")
	if fresh.ctx.Err() != nil || fresh.target.AppID != "app" {
		t.Fatal("recovered peer is invalid")
	}
}
