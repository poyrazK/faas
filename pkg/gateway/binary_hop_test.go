// adr: 20261110081523
package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hopPair serves one handler on two unix sockets, over HTTP/1.1 and over the
// binary hop, so a test can send the same request both ways.
type hopPair struct {
	h1Path, binPath string
	binary          *binaryHopTransport
	h1              *http.Transport
	binAccepts      atomic.Int64
	srv             *BinaryHopServer
}

type countingListener struct {
	net.Listener
	n *atomic.Int64
}

func (l countingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.n.Add(1)
	}
	return c, err
}

func newHopPair(t *testing.T, h http.Handler) *hopPair {
	t.Helper()
	dir, err := os.MkdirTemp("", "hop")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	p := &hopPair{h1Path: filepath.Join(dir, "h1.sock"), binPath: filepath.Join(dir, "bin.sock")}
	h1ln, err := net.Listen("unix", p.h1Path)
	if err != nil {
		t.Fatal(err)
	}
	h1srv := &http.Server{Handler: h, MaxHeaderBytes: 4096, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = h1srv.Serve(h1ln) }()
	t.Cleanup(func() { _ = h1srv.Close() })
	binln, err := net.Listen("unix", p.binPath)
	if err != nil {
		t.Fatal(err)
	}
	p.srv = &BinaryHopServer{Handler: func() http.Handler { return h }, MaxHeaderBytes: 4096, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	go func() { _ = p.srv.Serve(countingListener{binln, &p.binAccepts}) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.srv.Shutdown(ctx)
	})
	p.binary = newBinaryHopTransport(time.Second)
	t.Cleanup(p.binary.closeIdle)
	p.h1 = newInternalProxyTransport(NewUnixSocketDialer(p.h1Path), time.Second)
	t.Cleanup(p.h1.CloseIdleConnections)
	return p
}

func (p *hopPair) dialBinary(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", p.binPath)
}

// result is what a client saw of one response.
type result struct {
	status        int
	header        http.Header
	body          string
	trailer       http.Header
	contentLength int64
}

func (p *hopPair) do(t *testing.T, binary bool, newReq func() *http.Request) result {
	t.Helper()
	req := newReq()
	var resp *http.Response
	var err error
	if binary {
		resp, err = p.binary.roundTrip(req, p.dialBinary)
	} else {
		resp, err = p.h1.RoundTrip(req)
	}
	if err != nil {
		t.Fatalf("binary=%v: %v", binary, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("binary=%v: read body: %v", binary, err)
	}
	h := resp.Header.Clone()
	h.Del("Date")
	h.Del("Trailer")
	return result{status: resp.StatusCode, header: h, body: string(body), trailer: resp.Trailer, contentLength: resp.ContentLength}
}

// TestBinaryHopMatchesHTTP1 sends each request over both hops and compares
// what the handler saw and what the client got.
func TestBinaryHopMatchesHTTP1(t *testing.T) {
	type seen struct {
		method, host, uri, xff, body string
		contentLength                int64
		noBody                       bool
		trailer                      string
	}
	var mu sync.Mutex
	var last seen
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = seen{method: r.Method, host: r.Host, uri: r.RequestURI, xff: r.Header.Get("X-Forwarded-For"), body: string(body),
			contentLength: r.ContentLength, noBody: r.Body == http.NoBody, trailer: r.Trailer.Get("Checksum")}
		mu.Unlock()
		switch r.URL.Path {
		case "/small":
			_, _ = w.Write([]byte("hello"))
		case "/large":
			_, _ = w.Write([]byte(strings.Repeat("y", 5000)))
		case "/typed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"a":1}`))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/trailers":
			w.Header().Set("Trailer", "Grpc-Status")
			_, _ = w.Write([]byte("data"))
			w.Header().Set("Grpc-Status", "0")
			w.Header().Set(http.TrailerPrefix+"Late", "yes")
		case "/echo":
			_, _ = w.Write(body)
		case "/missing":
			http.NotFound(w, r)
		}
	})
	p := newHopPair(t, h)
	for _, tc := range []struct {
		name string
		req  func() *http.Request
	}{
		{"small", func() *http.Request { return newHopRequest(http.MethodGet, "/small?q=1", nil) }},
		{"large", func() *http.Request { return newHopRequest(http.MethodGet, "/large", nil) }},
		{"typed", func() *http.Request { return newHopRequest(http.MethodGet, "/typed", nil) }},
		{"head", func() *http.Request { return newHopRequest(http.MethodHead, "/small", nil) }},
		{"no content", func() *http.Request { return newHopRequest(http.MethodGet, "/empty", nil) }},
		{"trailers", func() *http.Request { return newHopRequest(http.MethodGet, "/trailers", nil) }},
		{"not found", func() *http.Request { return newHopRequest(http.MethodGet, "/missing", nil) }},
		{"known-length body", func() *http.Request {
			return newHopRequest(http.MethodPost, "/echo", strings.NewReader("payload"))
		}},
		{"streamed body with a trailer", func() *http.Request {
			r := newHopRequest(http.MethodPost, "/echo", io.MultiReader(strings.NewReader("part1-"), strings.NewReader("part2")))
			r.ContentLength = -1
			r.Trailer = http.Header{"Checksum": {"abc"}}
			return r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h1 := p.do(t, false, tc.req)
			mu.Lock()
			h1Seen := last
			mu.Unlock()
			bin := p.do(t, true, tc.req)
			mu.Lock()
			binSeen := last
			mu.Unlock()
			if h1Seen != binSeen {
				t.Errorf("handler saw\n  http/1.1 %+v\n  binary   %+v", h1Seen, binSeen)
			}
			if h1.status != bin.status || h1.body != bin.body || h1.contentLength != bin.contentLength {
				t.Errorf("response\n  http/1.1 %d %q length %d\n  binary   %d %q length %d", h1.status, h1.body, h1.contentLength, bin.status, bin.body, bin.contentLength)
			}
			if fmt.Sprint(h1.header) != fmt.Sprint(bin.header) {
				t.Errorf("header\n  http/1.1 %v\n  binary   %v", h1.header, bin.header)
			}
			if fmt.Sprint(h1.trailer) != fmt.Sprint(bin.trailer) {
				t.Errorf("trailer\n  http/1.1 %v\n  binary   %v", h1.trailer, bin.trailer)
			}
		})
	}
	if n := p.binAccepts.Load(); n != 1 {
		t.Errorf("binary hop opened %d connections for sequential requests, want 1 reused", n)
	}
}

func newHopRequest(method, target string, body io.Reader) *http.Request {
	r, err := http.NewRequest(method, "http://gatewayd-internal"+target, body)
	if err != nil {
		panic(err)
	}
	r.Host = "shop.gregale.dev"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	r.Header.Set("User-Agent", "test")
	return r
}

// A streamed request and its response flow both ways at once: the handler
// answers each piece before the next is sent.
func TestBinaryHopFullDuplex(t *testing.T) {
	p := newHopPair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		br := bufio.NewReader(r.Body)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			_, _ = w.Write([]byte("echo " + line))
			w.(http.Flusher).Flush()
		}
	}))
	pr, pw := io.Pipe()
	req := newHopRequest(http.MethodPost, "/stream", pr)
	req.ContentLength = -1
	resp, err := p.binary.roundTrip(req, p.dialBinary)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	for _, piece := range []string{"one\n", "two\n"} {
		if _, err := pw.Write([]byte(piece)); err != nil {
			t.Fatal(err)
		}
		got, err := br.ReadString('\n')
		if err != nil || got != "echo "+piece {
			t.Fatalf("got %q, %v; want %q", got, err, "echo "+piece)
		}
	}
	_ = pw.Close()
	if _, err := io.ReadAll(br); err != nil {
		t.Fatal(err)
	}
}

// Closing the request's context closes the connection, which cancels the
// handler's context, as net/http's background read does.
func TestBinaryHopCancelReachesTheHandler(t *testing.T) {
	cancelled := make(chan struct{})
	p := newHopPair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	req := newHopRequest(http.MethodGet, "/wait", nil).WithContext(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := p.binary.roundTrip(req, p.dialBinary)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler's context was not cancelled")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("round trip: %v, want context.Canceled", err)
	}
}

func TestBinaryHopInformationalResponses(t *testing.T) {
	p := newHopPair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", "</a.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	var got []int
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
		got = append(got, code)
		return nil
	}}
	req := newHopRequest(http.MethodGet, "/", nil)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := p.binary.roundTrip(req, p.dialBinary)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if len(got) != 1 || got[0] != http.StatusEarlyHints || resp.StatusCode != http.StatusOK {
		t.Fatalf("1xx %v then %d, want [103] then 200", got, resp.StatusCode)
	}
}

func TestBinaryHopLimitsAndFailures(t *testing.T) {
	var panics atomic.Int64
	p := newHopPair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/panic":
			panics.Add(1)
			panic(http.ErrAbortHandler)
		case "/close":
			w.Header().Set("Connection", "close")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Run("headers past the limit", func(t *testing.T) {
		req := newHopRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Big", strings.Repeat("v", 8192))
		resp, err := p.binary.roundTrip(req, p.dialBinary)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Fatalf("status %d, want 431", resp.StatusCode)
		}
	})
	t.Run("a panic ends the connection and the next request gets a new one", func(t *testing.T) {
		if _, err := p.binary.roundTrip(newHopRequest(http.MethodGet, "/panic", nil), p.dialBinary); err == nil {
			t.Fatal("a request whose handler panicked succeeded")
		}
		resp, err := p.binary.roundTrip(newHopRequest(http.MethodGet, "/", nil), p.dialBinary)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	})
	t.Run("Connection: close is not reused", func(t *testing.T) {
		before := p.binAccepts.Load()
		for _, path := range []string{"/close", "/"} {
			resp, err := p.binary.roundTrip(newHopRequest(http.MethodGet, path, nil), p.dialBinary)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if opened := p.binAccepts.Load() - before; opened < 1 {
			t.Fatalf("opened %d connections after Connection: close, want a new one", opened)
		}
	})
	t.Run("no socket is a dial error the edge falls back from", func(t *testing.T) {
		missing := func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", p.binPath+".missing")
		}
		_, err := newBinaryHopTransport(time.Second).roundTrip(newHopRequest(http.MethodGet, "/", nil), missing)
		var dialErr *hopDialError
		if !errors.As(err, &dialErr) || dialErr.reason() != HopFallbackNoSocket {
			t.Fatalf("err %v, want a %s dial error", err, HopFallbackNoSocket)
		}
	})
}

// The edge's upstream transport sends a request to an upstream with a hop
// in binary HTTP, and over HTTP/1.1 when the hop's dial fails.
func TestUpstreamTransportUsesTheBinaryHop(t *testing.T) {
	p := newHopPair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	var fallbacks []string
	for _, tc := range []struct {
		name         string
		hop          string
		wantBinary   bool
		wantFallback []string
	}{
		{name: "the hop serves", hop: p.binPath, wantBinary: true},
		{name: "no hop socket", hop: p.binPath + ".missing", wantFallback: []string{HopFallbackNoSocket}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fallbacks = nil
			picker := &hopTestPicker{upstream: Upstream{
				Key:  "local",
				Dial: func(ctx context.Context) (net.Conn, error) { return NewUnixSocketDialer(p.h1Path).DialContext(ctx, "") },
				Hop: func(ctx context.Context) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", tc.hop)
				},
			}}
			proxy := NewInternalReverseProxy(picker, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), false)
			if !proxy.WithUpstreamKeepAlive() || !proxy.WithBinaryHop(func(reason string) { fallbacks = append(fallbacks, reason) }) {
				t.Fatal("the binary hop did not take effect")
			}
			before := p.binAccepts.Load()
			resp, err := proxy.Transport.RoundTrip(newHopRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(body) != "ok" {
				t.Fatalf("body %q", body)
			}
			if gotBinary := p.binAccepts.Load() > before; gotBinary != tc.wantBinary {
				t.Fatalf("served over the binary hop = %v, want %v", gotBinary, tc.wantBinary)
			}
			if fmt.Sprint(fallbacks) != fmt.Sprint(tc.wantFallback) {
				t.Fatalf("fallbacks %v, want %v", fallbacks, tc.wantFallback)
			}
		})
	}
}

type hopTestPicker struct{ upstream Upstream }

func (p *hopTestPicker) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	return p.upstream.Dial(ctx)
}

func (p *hopTestPicker) PickUpstream(context.Context, *DialRecord, []string) (Upstream, error) {
	return p.upstream, nil
}

// Shutdown waits for a request in progress and refuses new connections.
func TestBinaryHopShutdownWaitsForRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	p := newHopPair(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
	}))
	got := make(chan string, 1)
	go func() {
		resp, err := p.binary.roundTrip(newHopRequest(http.MethodGet, "/", nil), p.dialBinary)
		if err != nil {
			got <- err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		got <- string(b)
	}()
	<-started
	shut := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shut <- p.srv.Shutdown(ctx)
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if b := <-got; b != "done" {
		t.Fatalf("in-flight request got %q, want done", b)
	}
	if err := <-shut; err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
