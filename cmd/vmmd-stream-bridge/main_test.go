package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseDeadline_DurationString(t *testing.T) {
	before := time.Now()
	got, err := parseDeadline("24h")
	if err != nil {
		t.Fatalf("parseDeadline(24h): %v", err)
	}
	delta := time.Until(got)
	// 24h minus the time spent in this test = roughly 24h. Allow
	// 1s slack on either side for clock granularity.
	if delta < 24*time.Hour-time.Second || delta > 24*time.Hour+time.Second {
		t.Errorf("parseDeadline(24h) = %v from now, want ~24h (got delta %v since %v)",
			got, delta, before)
	}
}

func TestParseDeadline_RFC3339(t *testing.T) {
	now := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	got, err := parseDeadline(now)
	if err != nil {
		t.Fatalf("parseDeadline(RFC3339): %v", err)
	}
	if d := time.Until(got); d < time.Hour || d > 3*time.Hour {
		t.Errorf("parseDeadline(RFC3339) delta = %v, want ~2h", d)
	}
}

func TestParseDeadline_EmptyFallsBackToDefault(t *testing.T) {
	before := time.Now()
	got, err := parseDeadline("")
	if err != nil {
		t.Fatalf("parseDeadline(\"\"): %v", err)
	}
	delta := time.Until(got)
	if delta < defaultSessionDeadline-time.Second || delta > defaultSessionDeadline+time.Second {
		t.Errorf("parseDeadline(\"\") = %v, want ~%v from now (got delta %v since %v)",
			got, defaultSessionDeadline, delta, before)
	}
}

func TestParseDeadline_Garbage(t *testing.T) {
	if _, err := parseDeadline("not-a-time"); err == nil {
		t.Errorf("parseDeadline(\"not-a-time\") should fail, got nil")
	}
}

func TestParseDeadline_NegativeDuration(t *testing.T) {
	if _, err := parseDeadline("-1h"); err == nil {
		t.Errorf("parseDeadline(\"-1h\") must reject negative durations (would produce a past-time deadline and 502 every request)")
	}
}

func TestParseDeadline_PastTimestamp(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := parseDeadline(past); err == nil {
		t.Errorf("parseDeadline(<past RFC3339>) must reject timestamps in the past")
	}
}

func TestParseHeaders_NewlineSeparated(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []headerEntry
	}{
		{"empty", "", nil},
		{"single", "a=1", []headerEntry{{Name: "a", Value: "1"}}},
		{"two", "a=1\nb=2", []headerEntry{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}},
		{"value-contains-comma", "Accept=text/html, application/json\nX-Custom=val",
			[]headerEntry{{Name: "Accept", Value: "text/html, application/json"}, {Name: "X-Custom", Value: "val"}}},
		{"value-contains-equals", "a=1\nb=2=\nc==3",
			[]headerEntry{{Name: "a", Value: "1"}, {Name: "b", Value: "2="}, {Name: "c", Value: "=3"}}},
		{"empty-name-dropped", "=skip\na=1", []headerEntry{{Name: "a", Value: "1"}}},
		{"no-equals-dropped", "noeq\na=1", []headerEntry{{Name: "a", Value: "1"}}},
		{"trailing-newline-tolerated", "a=1\n", []headerEntry{{Name: "a", Value: "1"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHeaders(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("parseHeaders(%q) = %d entries, want %d (%v)", tc.input, len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parseHeaders(%q)[%d] = %+v, want %+v", tc.input, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// fakeGuestHandler captures the bytes the bridge writes to the
// guest and replies with a fixed response. Used by the framing
// and chunked tests below.
type fakeGuestHandler struct {
	mu       sync.Mutex
	request  []byte
	body     []byte
	response string
}

func (f *fakeGuestHandler) ServeTCP(c net.Conn) {
	defer func() { _ = c.Close() }()
	buf := make([]byte, 64*1024)
	for {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if n > 0 {
			f.mu.Lock()
			f.request = append(f.request, buf[:n]...)
			// Capture body bytes after the blank-line separator.
			if idx := bytes.Index(f.request, []byte("\r\n\r\n")); idx >= 0 {
				f.body = append([]byte(nil), f.request[idx+4:]...)
			}
			f.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	// Reply with a fixed chunked response.
	_, _ = c.Write([]byte(f.response))
}

// startFakeGuest starts a TCP listener on 127.0.0.1:<random> and
// returns its address plus the captured-bytes handler.
func startFakeGuest(t *testing.T) (net.Listener, *fakeGuestHandler) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake guest listen: %v", err)
	}
	f := &fakeGuestHandler{
		response: "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n",
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.ServeTCP(conn)
		}
	}()
	return ln, f
}

func TestNewHandler_WritesHTTP11RequestLine(t *testing.T) {
	ln, fake := startFakeGuest(t)
	defer func() { _ = ln.Close() }()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	t.Setenv("FAAS_BRIDGE_METHOD", "POST")
	t.Setenv("FAAS_BRIDGE_URL", "/foo?bar=1")
	t.Setenv("FAAS_BRIDGE_HOST", "example.com")
	t.Setenv("FAAS_BRIDGE_HEADERS", "Content-Type=application/json\nX-Custom=val")

	// Start the bridge binary on a unix socket.
	bindSock := tempUnixSock(t)
	lnBridge, err := net.Listen("unix", bindSock)
	if err != nil {
		t.Fatalf("bridge listen: %v", err)
	}
	defer func() { _ = lnBridge.Close() }()
	srv := &http.Server{Handler: newHandler("127.0.0.1", mustPort(t, portStr), time.Now().Add(time.Minute))}
	// Test seam: H1-only over the unix socket. The H2C wire is
	// verified separately in pkg/vmmdgrpc/forward_test.go.
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	go func() { _ = srv.Serve(lnBridge) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Open a plain HTTP/1.1 client over the unix socket.
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", bindSock)
		},
	}}
	resp, err := c.Post("http://unix/foo?bar=1", "application/json", strings.NewReader("body"))
	if err != nil {
		t.Fatalf("client POST: %v", err)
	}
	_ = resp.Body.Close()

	// Allow the guest capture to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		got := string(fake.request)
		fake.mu.Unlock()
		if strings.Contains(got, "Transfer-Encoding: chunked") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fake.mu.Lock()
	got := string(fake.request)
	fake.mu.Unlock()

	if !strings.HasPrefix(got, "POST /foo?bar=1 HTTP/1.1\r\n") {
		t.Errorf("request line = %q, want %q", firstLine(got), "POST /foo?bar=1 HTTP/1.1")
	}
	if !strings.Contains(got, "Host: example.com\r\n") {
		t.Errorf("missing Host header: %q", got)
	}
	if !strings.Contains(got, "Transfer-Encoding: chunked\r\n") {
		t.Errorf("missing Transfer-Encoding: chunked: %q", got)
	}
	if strings.Contains(got, "Content-Length:") {
		t.Errorf("Content-Length must be dropped when chunked is hard-coded: %q", got)
	}
	if !strings.Contains(got, "Content-Type: application/json\r\n") {
		t.Errorf("missing Content-Type: %q", got)
	}
	if !strings.Contains(got, "X-Custom: val\r\n") {
		t.Errorf("missing X-Custom: %q", got)
	}
}

func TestNewHandler_ChunkedEncodesBody(t *testing.T) {
	ln, fake := startFakeGuest(t)
	defer func() { _ = ln.Close() }()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	bindSock := tempUnixSock(t)
	lnBridge, err := net.Listen("unix", bindSock)
	if err != nil {
		t.Fatalf("bridge listen: %v", err)
	}
	defer func() { _ = lnBridge.Close() }()
	srv := &http.Server{Handler: newHandler("127.0.0.1", mustPort(t, portStr), time.Now().Add(time.Minute))}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	go func() { _ = srv.Serve(lnBridge) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// 20 KiB body — three 8 KiB chunks plus the trailing 4 KiB.
	body := bytes.Repeat([]byte("x"), 20*1024)
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", bindSock)
		},
	}}
	resp, err := c.Post("http://unix/", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("client POST: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		got := string(fake.request)
		fake.mu.Unlock()
		if strings.Contains(got, "0\r\n\r\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fake.mu.Lock()
	got := string(fake.request)
	fake.mu.Unlock()

	// Decode the chunked framing from the captured request.
	idx := strings.Index(got, "\r\n\r\n")
	if idx < 0 {
		t.Fatalf("no head/body separator in captured request: %q", got)
	}
	decoded := decodeChunked(t, got[idx+4:])
	if !bytes.Equal(decoded, body) {
		t.Errorf("decoded body length = %d, want %d (first diff at %d)",
			len(decoded), len(body), firstDiff(decoded, body))
	}
}

func TestNewHandler_PropagatesContextCancellation(t *testing.T) {
	// Start a TCP guest that reads but never writes (so the
	// handler is stuck in the body io.Copy to the bridge side).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("guest listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Drain forever until peer closes.
			go func(c net.Conn) {
				_, _ = io.Copy(io.Discard, c)
				_ = c.Close()
			}(c)
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	bindSock := tempUnixSock(t)
	lnBridge, err := net.Listen("unix", bindSock)
	if err != nil {
		t.Fatalf("bridge listen: %v", err)
	}
	defer func() { _ = lnBridge.Close() }()
	srv := &http.Server{Handler: newHandler("127.0.0.1", mustPort(t, portStr), time.Now().Add(time.Minute))}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	go func() { _ = srv.Serve(lnBridge) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Open a client request that we will cancel mid-stream.
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", bindSock)
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	// Pipe that never closes on the client side — body stays open
	// until ctx cancel.
	bodyR, bodyW := io.Pipe()
	go func() {
		// Never write; just hold open until cancel.
		<-ctx.Done()
		_ = bodyW.Close()
	}()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://unix/", bodyR)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, _ := c.Do(req)
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	// Cancel after the handshake has settled.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good: the handler returned because the ctx watcher
		// closed the guest conn.
	case <-time.After(2 * time.Second):
		t.Errorf("handler did not return within 2s after ctx cancel; ctx-cancellation wiring is broken")
	}
}

// TestSanitizeCRLF is the unit-level pin for finding #6 from
// PR #754's medium code review. The function is the bridge-side
// defense-in-depth: vmmd already strips CR/LF in streamBridgeEnv
// (pkg/vmmdgrpc/forward.go), but the bridge is a stand-alone
// binary that may be invoked from other surfaces (tests, future
// operator override, misconfigured host env). The handler calls
// sanitizeCRLF on every env-derived value before writing it to the
// guest TCP socket; without this test a future refactor that drops
// the call would re-open the CRLF-injection hole.
func TestSanitizeCRLF(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "no-control", in: "hello world", want: "hello world"},
		{name: "bare-LF", in: "evil\ninjected", want: "evilinjected"},
		{name: "bare-CR", in: "evil\rinjected", want: "evilinjected"},
		{name: "CRLF", in: "evil\r\ninjected", want: "evilinjected"},
		{name: "NUL-truncation", in: "real\x00fake", want: "realfake"},
		{name: "multiple-LFs", in: "a\nb\nc", want: "abc"},
		{name: "leading-CRLF", in: "\r\nX", want: "X"},
		{name: "trailing-CRLF", in: "X\r\n", want: "X"},
		{name: "only-CRLF", in: "\r\n", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeCRLF(c.in); got != c.want {
				t.Errorf("sanitizeCRLF(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestNewHandler_SanitizesCRLFInEnvVars is the wire-level pin for
// the CRLF sanitization: a FAAS_BRIDGE_HOST value containing CRLF
// must NOT inject an extra header line into the H1 request to the
// guest. Runs the handler with a malicious FAAS_BRIDGE_HOST and
// asserts the captured guest bytes contain no extra header line.
func TestNewHandler_SanitizesCRLFInEnvVars(t *testing.T) {
	ln, fake := startFakeGuest(t)
	defer func() { _ = ln.Close() }()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())

	// Host header with embedded CRLF + injected header. The bridge
	// MUST strip the CRLF before writing — otherwise the guest sees
	// `Host: evil.com\r\nX-Injected: bad\r\n` which the guest's
	// net/http would parse as two headers.
	t.Setenv("FAAS_BRIDGE_METHOD", "GET")
	t.Setenv("FAAS_BRIDGE_URL", "/")
	t.Setenv("FAAS_BRIDGE_HOST", "evil.com\r\nX-Injected: bad")
	t.Setenv("FAAS_BRIDGE_HEADERS", "X-Custom=clean")

	bindSock := tempUnixSock(t)
	lnBridge, err := net.Listen("unix", bindSock)
	if err != nil {
		t.Fatalf("bridge listen: %v", err)
	}
	defer func() { _ = lnBridge.Close() }()
	srv := &http.Server{Handler: newHandler("127.0.0.1", mustPort(t, portStr), time.Now().Add(time.Minute))}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	go func() { _ = srv.Serve(lnBridge) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", bindSock)
		},
	}}
	resp, err := c.Get("http://unix/")
	if err != nil {
		t.Fatalf("client do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Allow the fake guest to capture the bytes (same shape as
	// TestNewHandler_WritesHTTP11RequestLine).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		got := string(fake.request)
		fake.mu.Unlock()
		if strings.Contains(got, "\r\n\r\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fake.mu.Lock()
	got := string(fake.request)
	fake.mu.Unlock()

	// The CRLF is stripped (no header line break), but the
	// concatenation "evil.com" + "X-Injected: bad" survives as
	// ONE Host header value. The injection prevention is the
	// line break — without it, the guest's net/http sees a single
	// header line and ignores the embedded colon. Assert that:
	//   1. There's exactly one Host header line (no second header
	//      produced from the injected text).
	//   2. The Host value is the sanitized concatenation.
	//   3. X-Custom passes through unchanged.
	hostLines := 0
	for _, line := range strings.Split(got, "\r\n") {
		if strings.HasPrefix(line, "Host:") {
			hostLines++
		}
	}
	if hostLines != 1 {
		t.Errorf("CRLF injection succeeded (got %d Host header lines, want 1): %q", hostLines, got)
	}
	if !strings.Contains(got, "Host: evil.comX-Injected: bad") {
		t.Errorf("expected sanitized Host header value to be the CRLF-stripped concatenation, got: %q", got)
	}
	if !strings.Contains(got, "X-Custom: clean") {
		t.Errorf("expected X-Custom header in request, got: %q", got)
	}
}

// --- helpers ---

func tempUnixSock(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "stream-bridge-*.sock")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return f.Name()
}

func mustPort(t *testing.T, s string) uint16 {
	t.Helper()
	var port int
	if _, err := fmt.Sscanf(s, "%d", &port); err != nil {
		t.Fatalf("parse port %q: %v", s, err)
	}
	return uint16(port)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func decodeChunked(t *testing.T, s string) []byte {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(s))
	var out []byte
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("decode chunked: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if line == "0" {
			break
		}
		var n int
		if _, err := fmt.Sscanf(line, "%x", &n); err != nil {
			t.Fatalf("chunk size %q: %v", line, err)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(br, buf); err != nil {
			t.Fatalf("chunk body: %v", err)
		}
		out = append(out, buf...)
		// Read trailing CRLF.
		cr := make([]byte, 2)
		if _, err := io.ReadFull(br, cr); err != nil {
			t.Fatalf("chunk CRLF: %v", err)
		}
	}
	return out
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// silence unused import warnings if helpers get pruned.
var _ = httptest.NewServer
