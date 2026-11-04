package gateway

// adr: 1343 — the public edge keeps connections to gatewayd-internal alive per
// compute node. The node is picked before the transport looks for an idle
// connection, so drains and wake failover exclusions (ADR-20261022184531)
// still decide where each request goes.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer is a gatewayd-internal stand-in that counts the connections
// and requests it accepts.
type countingServer struct {
	*httptest.Server
	conns    atomic.Int32
	requests atomic.Int32
	host     atomic.Value
}

func newCountingServer(t *testing.T, name string) *countingServer {
	t.Helper()
	s := &countingServer{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		s.host.Store(r.Host)
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Node", name)
		_, _ = w.Write(body)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			s.conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	s.Server = srv
	return s
}

func (s *countingServer) upstream(node string) Upstream {
	address := s.Listener.Addr().String()
	return Upstream{Key: "tcp://" + address, Node: node, Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", address)
	}}
}

// deadUpstream refuses every dial.
var deadUpstream = Upstream{Key: "tcp://dead", Node: "dead", Dial: func(context.Context) (net.Conn, error) {
	return nil, errors.New("connection refused")
}}

// scriptedPicker answers PickUpstream with pick and counts the calls.
type scriptedPicker struct {
	calls atomic.Int32
	pick  func(rec *DialRecord, failed []string) (Upstream, error)
}

func (p *scriptedPicker) DialContext(context.Context, string) (net.Conn, error) {
	return nil, errors.New("scriptedPicker connects through PickUpstream")
}

func (p *scriptedPicker) PickUpstream(_ context.Context, rec *DialRecord, failed []string) (Upstream, error) {
	p.calls.Add(1)
	return p.pick(rec, failed)
}

func newTestUpstreamTransport(picker UpstreamPicker) *upstreamTransport {
	return newUpstreamTransport(picker, newInternalProxyTransport(picker, time.Second), time.Second)
}

// trackedBody is a request body http.NewRequest cannot rewind (no GetBody),
// which counts its Close calls.
type trackedBody struct {
	io.Reader
	closes atomic.Int32
}

func (b *trackedBody) Close() error {
	b.closes.Add(1)
	return nil
}

func upstreamRequest(t *testing.T, ctx context.Context, method string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, "http://gatewayd-internal/path", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "app.example.test"
	return req
}

// roundTripBody sends req and returns the response's X-Node and body.
func roundTripBody(t *testing.T, rt http.RoundTripper, req *http.Request) (string, string) {
	t.Helper()
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.Header.Get("X-Node"), string(body)
}

func TestUpstreamKeepAliveReusesOneConnectionPerNode(t *testing.T) {
	a, b := newCountingServer(t, "a"), newCountingServer(t, "b")
	var next atomic.Int32
	picker := &scriptedPicker{pick: func(*DialRecord, []string) (Upstream, error) {
		if next.Add(1)%2 == 1 {
			return a.upstream("a"), nil
		}
		return b.upstream("b"), nil
	}}
	rt := newTestUpstreamTransport(picker)
	t.Cleanup(rt.transport.CloseIdleConnections)

	var nodes []string
	for range 10 {
		node, _ := roundTripBody(t, rt, upstreamRequest(t, context.Background(), http.MethodGet, nil))
		nodes = append(nodes, node)
	}
	if a.requests.Load() != 5 || b.requests.Load() != 5 {
		t.Fatalf("requests a=%d b=%d, want 5 each (round robin per request): %v", a.requests.Load(), b.requests.Load(), nodes)
	}
	if a.conns.Load() != 1 || b.conns.Load() != 1 {
		t.Fatalf("connections a=%d b=%d, want one kept-alive connection per node", a.conns.Load(), b.conns.Load())
	}
	if got := a.host.Load(); got != "app.example.test" {
		t.Fatalf("Host = %v, want the customer's host", got)
	}
}

// adr: 20261022184531 — a failover retry never rides an idle connection to the
// node it excluded.
func TestUpstreamKeepAliveNeverReusesAnExcludedNode(t *testing.T) {
	a, b := newCountingServer(t, "a"), newCountingServer(t, "b")
	picker := &scriptedPicker{pick: func(rec *DialRecord, _ []string) (Upstream, error) {
		if !rec.Excluded("a") {
			return a.upstream("a"), nil
		}
		return b.upstream("b"), nil
	}}
	rt := newTestUpstreamTransport(picker)
	t.Cleanup(rt.transport.CloseIdleConnections)

	first := NewDialRecord()
	if node, _ := roundTripBody(t, rt, upstreamRequest(t, WithDialRecord(context.Background(), first), http.MethodGet, nil)); node != "a" || first.Dialed() != "a" {
		t.Fatalf("first request reached %q, dialed %q; want a", node, first.Dialed())
	}
	retry := NewDialRecord("a")
	if node, _ := roundTripBody(t, rt, upstreamRequest(t, WithDialRecord(context.Background(), retry), http.MethodGet, nil)); node != "b" || retry.Dialed() != "b" {
		t.Fatalf("retry reached %q, dialed %q; want b while a has an idle connection", node, retry.Dialed())
	}
	if a.requests.Load() != 1 || b.requests.Load() != 1 {
		t.Fatalf("requests a=%d b=%d, want 1 each", a.requests.Load(), b.requests.Load())
	}
}

func TestUpstreamKeepAliveMovesToTheNextNodeWhenADialFails(t *testing.T) {
	live := newCountingServer(t, "live")
	var failedSeen [][]string
	var mu sync.Mutex
	picker := &scriptedPicker{pick: func(_ *DialRecord, failed []string) (Upstream, error) {
		mu.Lock()
		failedSeen = append(failedSeen, slices.Clone(failed))
		mu.Unlock()
		if !slices.Contains(failed, deadUpstream.Key) {
			return deadUpstream, nil
		}
		return live.upstream("live"), nil
	}}
	rt := newTestUpstreamTransport(picker)
	t.Cleanup(rt.transport.CloseIdleConnections)

	body := &trackedBody{Reader: strings.NewReader("payload")}
	rec := NewDialRecord()
	node, got := roundTripBody(t, rt, upstreamRequest(t, WithDialRecord(context.Background(), rec), http.MethodPost, body))
	if node != "live" || got != "payload" {
		t.Fatalf("response from %q with body %q, want the whole body at live", node, got)
	}
	if rec.Dialed() != "live" {
		t.Fatalf("dialed %q, want live", rec.Dialed())
	}
	if len(failedSeen) != 2 || len(failedSeen[1]) != 1 || failedSeen[1][0] != deadUpstream.Key {
		t.Fatalf("picks saw failed=%v, want the dead node's key on the second pick", failedSeen)
	}
	if closes := body.closes.Load(); closes != 1 {
		t.Fatalf("request body closed %d times, want once", closes)
	}
}

// A request that reached a node is never sent to another: it may have run.
func TestUpstreamKeepAliveDoesNotRetryARequestThatReachedANode(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Read the request, then drop the connection without answering.
			_, _ = http.ReadRequest(bufio.NewReader(conn))
			_ = conn.Close()
		}
	}()
	address := listener.Addr().String()
	hangup := Upstream{Key: "tcp://" + address, Node: "hangup", Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", address)
	}}
	live := newCountingServer(t, "live")
	picker := &scriptedPicker{pick: func(_ *DialRecord, failed []string) (Upstream, error) {
		if len(failed) == 0 {
			return hangup, nil
		}
		return live.upstream("live"), nil
	}}
	rt := newTestUpstreamTransport(picker)
	t.Cleanup(rt.transport.CloseIdleConnections)

	body := &trackedBody{Reader: strings.NewReader("payload")}
	resp, err := rt.RoundTrip(upstreamRequest(t, context.Background(), http.MethodPost, body))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a dropped connection after the request was sent returned a response")
	}
	if picker.calls.Load() != 1 || live.requests.Load() != 0 {
		t.Fatalf("picks=%d live requests=%d, want the error returned without another node", picker.calls.Load(), live.requests.Load())
	}
	if closes := body.closes.Load(); closes < 1 {
		t.Fatal("request body was not closed")
	}
}

func TestUpstreamKeepAliveReportsNoCapacity(t *testing.T) {
	tests := []struct {
		name      string
		pick      func(failed []string) (Upstream, error)
		wantPicks int32
		wantDial  bool
	}{
		{
			name:      "no upstream",
			pick:      func([]string) (Upstream, error) { return Upstream{}, ErrNoComputeCapacity },
			wantPicks: 1,
		},
		{
			name: "every dial failed",
			pick: func(failed []string) (Upstream, error) {
				if len(failed) == 0 {
					return deadUpstream, nil
				}
				return Upstream{}, ErrNoComputeCapacity
			},
			wantPicks: 2,
			wantDial:  true,
		},
		{
			// A picker that offers a failed upstream again must not loop.
			name:      "failed upstream offered again",
			pick:      func([]string) (Upstream, error) { return deadUpstream, nil },
			wantPicks: 2,
			wantDial:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			picker := &scriptedPicker{pick: func(_ *DialRecord, failed []string) (Upstream, error) { return tc.pick(failed) }}
			rt := newTestUpstreamTransport(picker)
			body := &trackedBody{Reader: strings.NewReader("payload")}
			resp, err := rt.RoundTrip(upstreamRequest(t, context.Background(), http.MethodPost, body))
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("RoundTrip succeeded without an upstream")
			}
			if !errors.Is(err, ErrNoComputeCapacity) {
				t.Fatalf("err = %v, want ErrNoComputeCapacity", err)
			}
			if tc.wantDial && !strings.Contains(err.Error(), "connection refused") {
				t.Fatalf("err = %v, want the failed dial named", err)
			}
			if picker.calls.Load() != tc.wantPicks {
				t.Fatalf("picks = %d, want %d", picker.calls.Load(), tc.wantPicks)
			}
			if body.closes.Load() != 1 {
				t.Fatalf("request body closed %d times, want once", body.closes.Load())
			}
		})
	}
}

func TestWithUpstreamKeepAliveNeedsAPickerAndTheHTTP1Transport(t *testing.T) {
	target := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	picker := &scriptedPicker{}
	tests := []struct {
		name   string
		dialer InternalDialer
		h2c    bool
		want   bool
	}{
		{name: "picker over HTTP/1.1", dialer: picker, want: true},
		{name: "fixed dialer", dialer: NewUnixSocketDialer("/run/faas/gatewayd-internal.sock"), want: false},
		{name: "picker over H2C", dialer: picker, h2c: true, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proxy := NewInternalReverseProxy(tc.dialer, target, nil, tc.h2c)
			before := proxy.Transport
			if got := proxy.WithUpstreamKeepAlive(); got != tc.want {
				t.Fatalf("WithUpstreamKeepAlive() = %v, want %v", got, tc.want)
			}
			_, keptAlive := proxy.Transport.(*upstreamTransport)
			if keptAlive != tc.want || (!tc.want && proxy.Transport != before) {
				t.Fatalf("transport %T after WithUpstreamKeepAlive() = %v", proxy.Transport, tc.want)
			}
		})
	}
}

func TestUpstreamKeepAliveThroughTheProxy(t *testing.T) {
	a := newCountingServer(t, "a")
	picker := &scriptedPicker{pick: func(*DialRecord, []string) (Upstream, error) { return a.upstream("a"), nil }}
	proxy := NewInternalReverseProxy(picker, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, nil, false)
	if !proxy.WithUpstreamKeepAlive() {
		t.Fatal("WithUpstreamKeepAlive() = false for a picker")
	}
	t.Cleanup(proxy.Transport.(*upstreamTransport).transport.CloseIdleConnections)
	for range 3 {
		rr := httptest.NewRecorder()
		proxy.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://app.example.test/path", nil))
		if rr.Code != http.StatusOK || rr.Header().Get("X-Node") != "a" {
			t.Fatalf("status %d from %q, want 200 from a", rr.Code, rr.Header().Get("X-Node"))
		}
	}
	if a.requests.Load() != 3 || a.conns.Load() != 1 {
		t.Fatalf("requests=%d connections=%d, want 3 over one connection", a.requests.Load(), a.conns.Load())
	}
}
