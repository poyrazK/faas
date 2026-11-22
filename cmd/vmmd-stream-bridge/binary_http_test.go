package main

// adr: 20261122140000 — binary HTTP from vmmd must reach the guest exactly as
// h2c does, and share the bridge socket with h2c and sessions.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"

	"github.com/onebox-faas/faas/pkg/binhop"
	"github.com/onebox-faas/faas/pkg/instancebridge"
)

// guestView is what the guest app saw of one request.
type guestView struct {
	Method, URI, Host string
	Header            []string // "Name: value", sorted
	ContentLength     int64
	TransferEncoding  []string
	Body              string
	BodyErr           string
}

type recordingGuest struct {
	*httptest.Server
	mu   sync.Mutex
	seen []guestView
}

func newRecordingGuest(t *testing.T) *recordingGuest {
	t.Helper()
	g := &recordingGuest{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := guestView{Method: r.Method, URI: r.RequestURI, Host: r.Host, ContentLength: r.ContentLength, TransferEncoding: r.TransferEncoding}
		for name, values := range r.Header {
			for _, value := range values {
				v.Header = append(v.Header, name+": "+value)
			}
		}
		slices.Sort(v.Header)
		b, err := io.ReadAll(r.Body)
		v.Body = string(b)
		if err != nil {
			v.BodyErr = err.Error()
		}
		g.mu.Lock()
		g.seen = append(g.seen, v)
		g.mu.Unlock()
		w.Header().Set("X-Guest", "1")
		if r.URL.Path == "/gz" && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			_, _ = zw.Write([]byte("ok:compressed"))
			_ = zw.Close()
			return
		}
		_, _ = w.Write([]byte("ok:" + string(b)))
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *recordingGuest) take() []guestView {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := g.seen
	g.seen = nil
	return out
}

// startDualBridge runs the bridge's servers on one unix socket as main
// does with FAAS_BRIDGE_BINARY_HTTP=1: sessions, then binary HTTP and h2c.
func startDualBridge(t *testing.T, guest *recordingGuest) (sock, guestPort string) {
	t.Helper()
	guestHost, guestPort, _ := net.SplitHostPort(guest.Listener.Addr().String())
	port, _ := strconv.Atoi(guestPort)
	dir, err := os.MkdirTemp("", "bb") // unix socket paths are short on macOS
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock = filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv, handler, _ := buildServerAndHandler(guestHost, uint16(port), time.Now().Add(time.Hour))
	var d net.Dialer
	httpLn := instancebridge.Split(ctx, ln, &instancebridge.Sessions{GuestIP: guestHost, Dial: d.DialContext})
	h2Ln, hop := serveBinaryHTTP(httpLn, handler)
	go func() { _ = srv.Serve(h2Ln) }()
	t.Cleanup(func() {
		shut, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = hop.Shutdown(shut)
		_ = srv.Shutdown(shut)
	})
	return sock, guestPort
}

func h2cClient(sock string) *http.Client {
	return &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}

// binaryClient mirrors vmmd's streamBridgeBinaryTransport.
type binaryClient struct {
	t    *binhop.Transport
	sock string
}

func (b *binaryClient) RoundTrip(req *http.Request) (*http.Response, error) {
	return b.t.RoundTrip(req, func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", b.sock)
	})
}

type bridgeCase struct {
	name   string
	build  func(port string) *http.Request
	refuse bool // the request must not reach the guest with mismatched framing
}

func bridgeRequest(port, method, uri string, body io.Reader, length int64, header http.Header) *http.Request {
	req, _ := http.NewRequest(method, "http://unix"+uri, body)
	if body == nil {
		req.Body = http.NoBody
	}
	req.ContentLength = length
	req.Header.Set(bridgeRequestMarkerHeader, "1")
	req.Header.Set(bridgeRequestProtocolHeader, "h1")
	req.Header.Set(bridgeRequestPortHeader, port)
	req.Header.Set(bridgeRequestHostHeader, "app.example")
	for name, values := range header {
		req.Header[name] = values
	}
	return req
}

// unknownLength hides a reader's length, so the request is streamed.
type unknownLength struct{ io.Reader }

var bridgeCases = []bridgeCase{
	{name: "get without user agent", build: func(p string) *http.Request {
		return bridgeRequest(p, "GET", "/a?b=1", nil, 0, nil)
	}},
	{name: "get with repeated and comma headers", build: func(p string) *http.Request {
		return bridgeRequest(p, "GET", "/x", nil, 0, http.Header{"User-Agent": {"k6/1.0"}, "Accept": {"a/b", "c/d"}, "X-List": {"1, 2"}, "Cookie": {"a=1; b=2"}})
	}},
	{name: "gzip the client did not ask for", build: func(p string) *http.Request {
		return bridgeRequest(p, "GET", "/gz", nil, 0, nil)
	}},
	{name: "gzip the client asked for", build: func(p string) *http.Request {
		return bridgeRequest(p, "GET", "/gz", nil, 0, http.Header{"Accept-Encoding": {"gzip"}})
	}},
	{name: "post with known length", build: func(p string) *http.Request {
		return bridgeRequest(p, "POST", "/u", strings.NewReader("hello"), 5, http.Header{"Content-Type": {"text/plain"}})
	}},
	{name: "post with streamed body", build: func(p string) *http.Request {
		return bridgeRequest(p, "POST", "/s", unknownLength{strings.NewReader(strings.Repeat("z", 100000))}, -1, nil)
	}},
	{name: "transfer-encoding field with known length", build: func(p string) *http.Request {
		return bridgeRequest(p, "POST", "/te", strings.NewReader("hello"), 5, http.Header{"Transfer-Encoding": {"chunked"}})
	}},
	{name: "private bridge header from the customer", build: func(p string) *http.Request {
		return bridgeRequest(p, "GET", "/p", nil, 0, http.Header{"X-Faas-Bridge-Evil": {"1"}})
	}},
	// x/net/http2's client refuses this request outright; over binary HTTP
	// it reaches the bridge, which must strip the field Connection lists.
	{name: "connection-listed field", refuse: true, build: func(p string) *http.Request {
		return bridgeRequest(p, "GET", "/c", nil, 0, http.Header{"Connection": {"close, X-Drop"}, "X-Drop": {"1"}})
	}},
	{name: "conflicting content-length fields", refuse: true, build: func(p string) *http.Request {
		return bridgeRequest(p, "POST", "/cl", strings.NewReader("hello"), 5, http.Header{"Content-Length": {"3", "9"}})
	}},
}

// TestBinaryHTTPReachesGuestAsH2C sends each request through the bridge over
// h2c and over binary HTTP: the guest must see the same request, and a
// request with framing the two could read differently never reaches it
// mismatched.
func TestBinaryHTTPReachesGuestAsH2C(t *testing.T) {
	guest := newRecordingGuest(t)
	sock, port := startDualBridge(t, guest)
	clients := map[string]*http.Client{
		"h2c":    h2cClient(sock),
		"binary": {Transport: &binaryClient{t: binhop.NewTransport(time.Minute, 0, 0).WithHTTP2ClientDefaults(), sock: sock}},
	}
	for _, tc := range bridgeCases {
		t.Run(tc.name, func(t *testing.T) {
			views := map[string][]guestView{}
			bodies := map[string]string{}
			for name, c := range clients {
				resp, err := c.Do(tc.build(port))
				if err == nil {
					b, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					bodies[name] = fmt.Sprintf("%d %s ce=%q %q", resp.StatusCode, resp.Header.Get("X-Guest"), resp.Header.Get("Content-Encoding"), b)
				} else {
					bodies[name] = "error: " + err.Error()
				}
				views[name] = guest.take()
			}
			for name, seen := range views {
				for _, v := range seen {
					if v.BodyErr == "" && v.ContentLength >= 0 && int64(len(v.Body)) != v.ContentLength {
						t.Fatalf("%s: guest got %d body bytes for Content-Length %d", name, len(v.Body), v.ContentLength)
					}
					for _, h := range v.Header {
						if strings.HasPrefix(strings.ToLower(h), "x-faas-bridge-") || strings.HasPrefix(h, "X-Drop:") {
							t.Fatalf("%s: private or connection-listed header reached the guest: %q", name, h)
						}
					}
				}
			}
			if tc.refuse {
				if n := len(views["binary"]); n != 0 && views["binary"][0].ContentLength != int64(len(views["binary"][0].Body)) {
					t.Fatalf("binary: mismatched request reached the guest: %+v", views["binary"])
				}
				return
			}
			if !equalViews(views["h2c"], views["binary"]) {
				t.Fatalf("guest saw different requests:\n h2c    %+v\n binary %+v\n responses %q", views["h2c"], views["binary"], bodies)
			}
			if bodies["h2c"] != bodies["binary"] {
				t.Fatalf("responses differ: h2c %q binary %q", bodies["h2c"], bodies["binary"])
			}
		})
	}
}

func equalViews(a, b []guestView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Method != y.Method || x.URI != y.URI || x.Host != y.Host || x.ContentLength != y.ContentLength ||
			x.Body != y.Body || x.BodyErr != y.BodyErr || !slices.Equal(x.Header, y.Header) || !slices.Equal(x.TransferEncoding, y.TransferEncoding) {
			return false
		}
	}
	return true
}

// TestBridgeSocketSharesSessionsH2CAndBinary checks one socket still routes
// a TCP session (its hello is answered by the session server), an h2c
// request and a binary HTTP request, each by its first bytes.
func TestBridgeSocketSharesSessionsH2CAndBinary(t *testing.T) {
	guest := newRecordingGuest(t)
	sock, port := startDualBridge(t, guest)
	p, _ := strconv.Atoi(port)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := instancebridge.WriteHello(conn, instancebridge.KindTCP, uint16(p)); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(conn)
	if err := instancebridge.ReadReady(r); err != nil {
		t.Fatalf("session hello: %v", err)
	}
	for name, c := range map[string]*http.Client{
		"h2c":    h2cClient(sock),
		"binary": {Transport: &binaryClient{t: binhop.NewTransport(time.Minute, 0, 0).WithHTTP2ClientDefaults(), sock: sock}},
	} {
		resp, err := c.Do(bridgeRequest(port, "GET", "/"+name, nil, 0, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.HasPrefix(b, []byte("ok:")) {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, b)
		}
	}
	got := map[string]bool{}
	for _, v := range guest.take() {
		got[v.URI] = true
	}
	for _, uri := range []string{"/h2c", "/binary"} {
		if !got[uri] {
			t.Fatalf("guest never saw %s (saw %v)", uri, got)
		}
	}
}

// BenchmarkBridgeFraming drives one hello-world exchange per op from vmmd's
// side through the real bridge (both servers on one socket, as with
// FAAS_BRIDGE_BINARY_HTTP=1) to an in-process HTTP/1.1 guest, over h2c and
// over binary HTTP. cpu-µs/op is the whole process: client, bridge, guest.
//
//	go test -run '^$' -bench BridgeFraming -benchmem -cpu 1 -count 6 ./cmd/vmmd-stream-bridge/
func BenchmarkBridgeFraming(b *testing.B) {
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	defer guest.Close()
	guestHost, guestPort, _ := net.SplitHostPort(guest.Listener.Addr().String())
	port, _ := strconv.Atoi(guestPort)
	dir, err := os.MkdirTemp("", "bf")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, handler, _ := buildServerAndHandler(guestHost, uint16(port), time.Now().Add(time.Hour))
	var d net.Dialer
	httpLn := instancebridge.Split(ctx, ln, &instancebridge.Sessions{GuestIP: guestHost, Dial: d.DialContext})
	h2Ln, hop := serveBinaryHTTP(httpLn, handler)
	go func() { _ = srv.Serve(h2Ln) }()
	defer func() { _ = srv.Close(); _ = hop.Shutdown(context.Background()) }()

	for _, mode := range []struct {
		name   string
		client *http.Client
	}{
		{"h2c", h2cClient(sock)},
		{"binary", &http.Client{Transport: &binaryClient{t: binhop.NewTransport(time.Minute, 0, 0).WithHTTP2ClientDefaults(), sock: sock}}},
	} {
		b.Run(mode.name, func(b *testing.B) {
			exchange := func() {
				req := bridgeRequest(guestPort, http.MethodGet, "/", nil, 0, http.Header{"User-Agent": {"k6/1.0"}, "Accept": {"*/*"}})
				resp, err := mode.client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
			for range 50 {
				exchange()
			}
			b.ReportAllocs()
			b.ResetTimer()
			start := processCPU()
			for b.Loop() {
				exchange()
			}
			b.ReportMetric(float64(processCPU()-start)/float64(b.N)/1e3, "cpu-µs/op")
		})
	}
}
