package udpd

import (
	"bytes"
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type echoForwarder struct{}

func (echoForwarder) ServePeer(ctx context.Context, peer gateway.DatagramPeer, target gateway.Target) error {
	for {
		payload, err := peer.Receive(ctx)
		if err != nil {
			return err
		}
		if err := peer.Send(ctx, payload); err != nil {
			return err
		}
	}
}
func TestServerKeepsDatagramPeersIsolated(t *testing.T) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metrics := NewMetrics(nil, "test")
	server := &Server{Metrics: metrics, Socket: socket, Route: Route{ListenerID: "listener", PublicPort: 40100, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 5353}, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Forwarder: echoForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
		return gateway.Target{AppID: "app", InstanceID: "guest", NodeID: "node"}, nil
	}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	var clients []*net.UDPConn
	for i := 0; i < 2; i++ {
		client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, client)
		defer client.Close()
	}
	for i, payload := range [][]byte{{0, 255, 1}, []byte("other-peer")} {
		if _, _, err := clients[i].WriteMsgUDP(payload, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range [][]byte{{0, 255, 1}, []byte("other-peer")} {
		if err := clients[i].SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		var buffer [128]byte
		n, err := clients[i].Read(buffer[:])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buffer[:n], want) {
			t.Fatalf("peer %d received another peer's data: %q", i, buffer[:n])
		}
	}
	if _, _, err := clients[0].WriteMsgUDP(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	var empty [1]byte
	if n, err := clients[0].Read(empty[:]); err != nil || n != 0 {
		t.Fatalf("empty datagram lost: n=%d err=%v", n, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop its peers and socket")
	}
	assertMetric(t, metrics, "test_udp_active_peers", nil, 0)
	assertMetric(t, metrics, "test_udp_listeners", nil, 0)
	assertMetric(t, metrics, "test_udp_peers_started_total", nil, 2)
	assertMetric(t, metrics, "test_udp_peers_completed_total", map[string]string{"outcome": "canceled"}, 2)
	for _, direction := range []string{"client_to_guest", "guest_to_client"} {
		assertMetric(t, metrics, "test_udp_datagrams_total", map[string]string{"direction": direction}, 3)
		assertMetric(t, metrics, "test_udp_payload_bytes_total", map[string]string{"direction": direction}, 13)
	}
}
func TestServerEmptySourceAllowlistDoesNotAdmit(t *testing.T) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := make(chan struct{}, 1)
	server := &Server{Socket: socket, Route: Route{ListenerID: "listener", PublicPort: 40100, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 5353}, Forwarder: echoForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
		admitted <- struct{}{}
		return gateway.Target{}, nil
	}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("blocked")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-admitted:
		t.Fatal("empty source allowlist admitted a peer")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server failed to stop")
	}
}

type resourceLimitedForwarder struct{}

func (resourceLimitedForwarder) ServePeer(context.Context, gateway.DatagramPeer, gateway.Target) error {
	return status.Error(codes.ResourceExhausted, "peer datagram budget exhausted")
}
func TestServerSeparatesResourceExhaustionFromForwardFailures(t *testing.T) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metrics := NewMetrics(nil, "test")
	reported := make(chan error, 1)
	server := &Server{Socket: socket, Route: Route{ListenerID: "listener", PublicPort: 40100, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 5353}, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Metrics: metrics, Forwarder: resourceLimitedForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
		return gateway.Target{AppID: "app", InstanceID: "guest", NodeID: "node"}, nil
	}, OnError: func(err error) { reported <- err }}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reported:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("quota did not terminate peer")
	}
	deadline := time.Now().Add(time.Second)
	for {
		var metric dto.Metric
		if err := metrics.completed.WithLabelValues("resource_exhausted").Write(&metric); err != nil {
			t.Fatal(err)
		}
		if metric.GetCounter().GetValue() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resource completion was not recorded")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stalled")
	}
	assertMetric(t, metrics, "test_udp_peers_completed_total", map[string]string{"outcome": "resource_exhausted"}, 1)
	assertMetric(t, metrics, "test_udp_peers_completed_total", map[string]string{"outcome": "forward_error"}, 0)
	assertMetric(t, metrics, "test_udp_active_peers", nil, 0)
}

type countingUDPForwarder struct{ calls atomic.Int32 }

func (f *countingUDPForwarder) ServePeer(context.Context, gateway.DatagramPeer, gateway.Target) error {
	f.calls.Add(1)
	return nil
}
func TestServerRejectsIncompleteAdmissionAndReleasesQuota(t *testing.T) {
	for _, target := range []gateway.Target{
		{InstanceID: "guest", NodeID: "node"},
		{AppID: "other", InstanceID: "guest", NodeID: "node"},
		{AppID: "app", NodeID: "node"},
		{AppID: "app", InstanceID: "guest"},
	} {
		t.Run(target.AppID+"/"+target.InstanceID+"/"+target.NodeID, func(t *testing.T) {
			socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pool := NewPeerPool(1, 1)
			metrics := NewMetrics(nil, "test")
			reported := make(chan error, 1)
			deadlineObserved := make(chan bool, 1)
			forwarder := &countingUDPForwarder{}
			server := &Server{Socket: socket, Route: Route{ListenerID: "listener", PublicPort: 40100, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 5353}, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Pool: pool, Metrics: metrics, Forwarder: forwarder, ResolveTarget: func(admitCtx context.Context, _ Route) (gateway.Target, error) {
				deadline, ok := admitCtx.Deadline()
				remaining := time.Until(deadline)
				deadlineObserved <- ok && remaining > 0 && remaining <= api.UDPAdmissionTimeout
				return target, nil
			}, OnError: func(err error) { reported <- err }}
			done := make(chan error, 1)
			go func() { done <- server.Serve(ctx) }()
			client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Write([]byte("request")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-reported:
			case <-time.After(time.Second):
				t.Fatal("invalid admission was not rejected")
			}
			if !<-deadlineObserved {
				t.Fatal("admission deadline missing or exceeds platform bound")
			}
			deadline := time.Now().Add(time.Second)
			for {
				release, ok := pool.Acquire("account")
				if ok {
					release()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("failed admission retained peer quota")
				}
				time.Sleep(time.Millisecond)
			}
			if forwarder.calls.Load() != 0 {
				t.Fatal("incomplete target reached forwarding")
			}
			assertMetric(t, metrics, "test_udp_peers_completed_total", map[string]string{"outcome": "admission_error"}, 1)
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("server shutdown stalled")
			}
		})
	}
}
func TestServerDoesNotForwardCanceledAdmissionResult(t *testing.T) {
	socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder := &countingUDPForwarder{}
	entered := make(chan struct{})
	server := &Server{Socket: socket, Route: Route{ListenerID: "listener", PublicPort: 40100, AppID: "app", AccountID: "account", ListenerName: "echo", GuestPort: 5353}, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Forwarder: forwarder, ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
		close(entered)
		cancel()
		return gateway.Target{AppID: "app", InstanceID: "guest", NodeID: "node"}, nil
	}}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("request")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("admission did not run")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled admission blocked shutdown")
	}
	if forwarder.calls.Load() != 0 {
		t.Fatal("canceled admission started forwarding")
	}
}

func TestServerSharesAccountAdmissionAcrossSockets(t *testing.T) {
	pool := NewPeerPool(2, 1)
	rates := NewRateLimits()
	var reported atomic.Int32
	start := func(id string, port int) (*net.UDPConn, context.CancelFunc, chan error, *Metrics) {
		t.Helper()
		socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		metrics := NewMetrics(nil, id)
		server := &Server{Socket: socket, Route: Route{AppID: "app", AccountID: "account", ListenerID: id, ListenerName: id, PublicPort: port, GuestPort: 5353}, Pool: pool, Rates: rates, Metrics: metrics, AllowedSources: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, Forwarder: echoForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
			return gateway.Target{AppID: "app", InstanceID: "instance", NodeID: "node"}, nil
		}, OnError: func(error) { reported.Add(1) }}
		done := make(chan error, 1)
		go func() { done <- server.Serve(ctx) }()
		t.Cleanup(func() { cancel(); _ = socket.Close() })
		client, err := net.DialUDP("udp4", nil, socket.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client, cancel, done, metrics
	}
	first, cancelFirst, firstDone, firstMetrics := start("first", 40100)
	second, cancelSecond, secondDone, secondMetrics := start("second", 40101)
	echo := func(client *net.UDPConn, payload string) {
		t.Helper()
		if _, err := client.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		var buffer [64]byte
		n, err := client.Read(buffer[:])
		if err != nil || string(buffer[:n]) != payload {
			t.Fatalf("echo=%q/%v", buffer[:n], err)
		}
	}
	echo(first, "first peer")
	if _, err := second.Write([]byte("blocked peer")); err != nil {
		t.Fatal(err)
	}
	if err := second.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var buffer [64]byte
	if _, err := second.Read(buffer[:]); err == nil {
		t.Fatal("another socket bypassed the shared account peer cap")
	} else {
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() {
			t.Fatal(err)
		}
	}
	assertMetric(t, secondMetrics, "second_udp_datagrams_dropped_total", map[string]string{"reason": "peer_limit"}, 1)
	stop := func(cancel context.CancelFunc, done chan error) {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("listener did not release its peers")
		}
	}
	stop(cancelFirst, firstDone)
	echo(second, "reused slot")
	stop(cancelSecond, secondDone)
	assertMetric(t, firstMetrics, "first_udp_active_peers", nil, 0)
	assertMetric(t, secondMetrics, "second_udp_active_peers", nil, 0)
	if reported.Load() != 0 {
		t.Fatalf("clean shutdown reported %d errors", reported.Load())
	}
	for _, account := range []string{"one", "two"} {
		release, ok := pool.Acquire(account)
		if !ok {
			t.Fatal("shutdown retained a global slot")
		}
		defer release()
	}
}

func TestServerInvalidConfigClosesOwnedSocket(t *testing.T) {
	for _, name := range []string{"route", "forwarder", "source", "parent"} {
		t.Run(name, func(t *testing.T) {
			socket, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = socket.Close() })
			server := &Server{Socket: socket, Route: Route{AppID: "app", AccountID: "account", ListenerID: "listener", ListenerName: "echo", PublicPort: 40100, GuestPort: 5353}, Forwarder: echoForwarder{}, ResolveTarget: func(context.Context, Route) (gateway.Target, error) {
				t.Fatal("invalid configuration attempted admission")
				return gateway.Target{}, nil
			}}
			ctx := context.Background()
			switch name {
			case "route":
				server.Route.ListenerID = ""
			case "forwarder":
				server.Forwarder = nil
			case "source":
				server.AllowedSources = []netip.Prefix{{}}
			case "parent":
				ctx = nil
			}
			if err := socket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := server.Serve(ctx); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			var buffer [1]byte
			if _, err := socket.Read(buffer[:]); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("owned socket survived rejection: %v", err)
			}
		})
	}
}
