// Command vmmd-stream-bridge (issue #686) is the inner-leg H2C
// streaming bridge for the gatewayd → vmmd → guest path.
//
// The legacy streaming bridge (buildStreamingBridgeScript in
// pkg/vmmdgrpc/forward.go:989) shell-scripts a `/dev/tcp` dial and
// hard-codes HTTP/1.1 on the wire (forward.go:998). With the
// gatewayd-public → gatewayd-internal hop now running H2C
// (ADR-070 + PR #713/#719), the inner leg is the only plaintext
// H1 hop in the chain. vmmd-stream-bridge replaces the shell
// bridge with a small Go binary that:
//
//  1. Listens on a unix socket inside the host netns (the binary
//     is spawned via `ip netns exec <netns> …` so it inherits the
//     per-instance netns; ADR-009's strict-netns invariant is
//     preserved by the spawn shape, not the binary itself).
//  2. Speaks H2C on that socket (cleartext HTTP/2, no TLS).
//  3. For each inbound H2C request, opens an HTTP/1.1 connection to
//     the guest at 10.0.0.2:<port>, writes the request line +
//     headers + chunked transfer encoding from env-supplied
//     `FAAS_BRIDGE_*` vars, bridges the body bidirectionally, and
//     reads back the chunked-encoded response.
//
// v2 vs v1: v1 (the shell bridge) is the production default until
// `make metal-lima` confirms H2C end-to-end via the e2e test
// (cmd/e2e/streaming_metal_test.go). The flip is a one-line change
// to `streamBridgeVersion` in pkg/vmmdgrpc/forward.go. Setting
// FAAS_STREAM_BRIDGE_VERSION=v1 on vmmd is the live rollback.
//
// Args (positional, no flags):
//
//	argv[1] = bind unix socket path
//	          (e.g. /var/run/faas/stream/<instance>.sock)
//	argv[2] = guest IP (e.g. 10.0.0.2)
//	argv[3] = guest TCP port
//	argv[4] = session deadline (RFC3339 or duration like "24h")
//
// Env vars (HONORED BY THE HANDLER, set by vmmd from the
// ForwardHTTPRequestInit frame):
//
//	FAAS_BRIDGE_METHOD  = HTTP method (e.g. GET, POST)
//	FAAS_BRIDGE_URL     = request URI (e.g. /foo?bar=1)
//	FAAS_BRIDGE_HOST    = Host header value (e.g. example.com)
//	FAAS_BRIDGE_HEADERS = comma-separated k=v pairs, split on the
//	                      first '=' so values may contain '='. Content-
//	                      Length is dropped (chunked is hard-coded).
//
// Wire protocol:
//
//   - server side: H2C (cleartext HTTP/2) per RFC 7540 / 9113
//   - client side: HTTP/1.1 + Transfer-Encoding: chunked to the
//     guest (the legacy v1 contract; matches the shell bridge so
//     guest-side net/http sees the same envelope)
//   - bidi byte-copy with chunked framing on the request side and
//     httputil.NewChunkedReader on the response side
//
// Failure modes (exit code != 0):
//
//	2 = usage error (bad argv, bad deadline)
//	3 = bind failure on the unix socket
//	4 = accept loop fatal
//
// See docs/adr/028-gatewayd-remote-routing.md (amended) for the
// architectural context; the bridge is the artifact that closes
// issue #686.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dialTimeout caps the inner-leg TCP dial against the guest.
// Past 30s the gateway has already-abandoned the wake.
const dialTimeout = 30 * time.Second

// readHeaderTimeout caps how long we wait for the guest's first
// response byte (the HTTP/1.1 status line + headers) after the
// H2C request lands. Applied only to the head read; the body
// io.Copy that follows runs with no conn deadline so SSE / WS /
// long-poll responses can stream past 30 s.
const readHeaderTimeout = 30 * time.Second

// defaultSessionDeadline is the wall-clock ceiling for a streaming
// session; matches rawStreamSessionDeadline in pkg/gateway/forwardproxy.go.
const defaultSessionDeadline = 24 * time.Hour

// requestChunkSize is the chunked-encoding chunk size emitted to
// the guest. 8 KiB matches the shell bridge's
// `dd bs=8192 …` granularity (forward.go:1016-1024) and the vmmd
// reader goroutine's 8 KiB read buffer (forward.go:367).
const requestChunkSize = 8 * 1024

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintf(os.Stderr, "usage: vmmd-stream-bridge <bind-unix-sock> <guest-ip> <guest-port> <deadline>\n")
		os.Exit(2)
	}
	bind := os.Args[1]
	guestIP := os.Args[2]
	port, err := strconv.ParseUint(os.Args[3], 10, 16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid port %q: %v\n", os.Args[3], err)
		os.Exit(2)
	}
	deadline, err := parseDeadline(os.Args[4])
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid deadline %q: %v\n", os.Args[4], err)
		os.Exit(2)
	}

	// Remove any stale socket from a previous crashed run.
	if err := os.Remove(bind); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "remove stale socket %s: %v\n", bind, err)
		os.Exit(2)
	}
	ln, err := net.Listen("unix", bind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen %s: %v\n", bind, err)
		os.Exit(3)
	}
	defer func() { _ = ln.Close() }()

	// chmod 0600 — only vmmd (and the jailer user) can dial this
	// socket. Per spec §11 the host /var/run/faas/stream/ is mode 0700
	// so this is belt-and-braces, but the explicit chmod is the source
	// of truth per the manpage convention.
	if err := os.Chmod(bind, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "chmod %s: %v\n", bind, err)
		os.Exit(3)
	}

	srv := &http.Server{
		Handler: newHandler(guestIP, uint16(port), deadline),
		// ReadHeaderTimeout is the H2C connection preface budget;
		// 10s is generous on a same-host unix socket.
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Enable cleartext HTTP/2 (H2C) on this listener via the stdlib
	// Protocols API (Go 1.24+). Replaces the deprecated
	// golang.org/x/net/http2/h2c.NewHandler wrapper; the vmmd client
	// now sets its transport to AllowHTTP=true and dials the unix
	// socket directly. The Protocols struct must be non-nil before
	// calling SetUnencryptedHTTP2 (Go 1.26 panics on nil).
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetUnencryptedHTTP2(true)

	// SIGTERM/SIGINT → graceful shutdown. vmmd sends SIGTERM after
	// the gRPC ForwardHTTPStream returns; the bridge exits cleanly
	// without truncating an in-flight stream.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	errc := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case <-ctx.Done():
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutCancel()
		_ = srv.Shutdown(shutCtx)
		<-errc
	case err := <-errc:
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: %v\n", err)
			os.Exit(4)
		}
	}
}

// newHandler builds the H2C handler that proxies a single H2C
// stream to the guest at <guestIP>:<port>. Each inbound request
// opens a fresh dial against the guest, writes the H1 request
// line + headers + chunked body, and reads back the chunked
// response.
//
// The request parts are taken from env vars set by vmmd from the
// ForwardHTTPRequestInit frame:
//
//	FAAS_BRIDGE_METHOD  + FAAS_BRIDGE_URL form the request line
//	FAAS_BRIDGE_HOST    is the Host header
//	FAAS_BRIDGE_HEADERS contributes the rest (Content-Length dropped)
//
// We do NOT keep a long-lived guest conn per H2C stream. The
// guest at 10.0.0.2:<port> is HTTP/1.1 and a long-lived conn
// would have to serialize requests through it; the simpler shape
// is "one H2C request = one guest dial." A future optimisation
// (HTTP/1.1 keep-alive multiplexing on the guest side) is out of
// scope for the cutover.
func newHandler(guestIP string, guestPort uint16, deadline time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()

		method := os.Getenv("FAAS_BRIDGE_METHOD")
		if method == "" {
			method = "GET"
		}
		url := os.Getenv("FAAS_BRIDGE_URL")
		if url == "" {
			url = "/"
		}
		host := os.Getenv("FAAS_BRIDGE_HOST")
		extraHeaders := parseHeaders(os.Getenv("FAAS_BRIDGE_HEADERS"))

		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(guestIP, strconv.FormatUint(uint64(guestPort), 10)))
		if err != nil {
			http.Error(w, fmt.Sprintf("dial guest: %v", err), http.StatusBadGateway)
			return
		}
		defer func() { _ = conn.Close() }()

		// ctx.Done() watcher: close the guest conn when the H2C
		// request is cancelled so the in-flight io.Copy(s) unblock
		// with `use of closed network connection` rather than
		// hanging on a dead guest. The deferred conn.Close() above
		// is the safety net; this goroutine is the eager path.
		stopWatch := make(chan struct{})
		defer func() { close(stopWatch) }()
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-stopWatch:
			}
		}()

		// Write the H1 request line + headers + chunked framing
		// before the body. Mirrors the v1 shell script output
		// (forward.go:998-1024) byte-for-byte modulo path-style
		// differences; the guest's net/http must see the same
		// envelope to handle the request correctly.
		if err := writeH1RequestHead(conn, method, url, host, extraHeaders); err != nil {
			http.Error(w, fmt.Sprintf("write request head: %v", err), http.StatusBadGateway)
			return
		}

		// Bridge request body → guest, in chunked-encoded chunks.
		// The bridge writes the chunk-size line, then the bytes,
		// then a CRLF, repeating until r.Body returns EOF, then a
		// final "0\r\n\r\n" terminator. The guest's net/http
		// stack decodes the encoding transparently.
		bodyErr := make(chan error, 1)
		go func() {
			bodyErr <- writeChunkedBody(conn, r.Body)
		}()

		// Bound ONLY the response-head read at readHeaderTimeout so
		// a wedged guest doesn't hang the H2C stream waiting for the
		// first byte. The streaming body io.Copy below MUST run with
		// no conn deadline so SSE / WS / long-poll responses can
		// stream past 30 s; that bound is the ctx deadline (24 h by
		// default) which the watcher goroutine respects via Close.
		_ = conn.SetReadDeadline(time.Now().Add(readHeaderTimeout))
		br := newBufioReader(conn)
		resp, err := http.ReadResponse(br, r)
		_ = conn.SetReadDeadline(time.Time{})
		if err != nil {
			http.Error(w, fmt.Sprintf("read guest response: %v", err), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		// Mirror guest response headers back to the H2C caller.
		// Chunked-decoded on the way out so the H2C client sees
		// the same body semantics as the v1 forward.go path.
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)

		// Drain the body goroutine so any chunked-encoding error
		// surfaces as a 502 to the H2C client. The ctx watcher
		// will close the conn when the handler returns (via the
		// deferred close(stopWatch) sequence plus the deferred
		// conn.Close on conn).
		if err := <-bodyErr; err != nil && !errors.Is(err, io.EOF) {
			// Best-effort: the response is already written,
			// so we can't change the status. Log to stderr.
			fmt.Fprintf(os.Stderr, "writeChunkedBody: %v\n", err)
		}
	})
}

// writeH1RequestHead writes the HTTP/1.1 request line + headers
// + `Transfer-Encoding: chunked` to the guest conn. The header
// list is the FAAS_BRIDGE_HEADERS entries with Content-Length
// dropped (chunked is hard-coded). Headers go through the
// `textproto.MIMEHeader.Set` canonical form (Title-Case header
// names, no trailing whitespace on values).
func writeH1RequestHead(w io.Writer, method, url, host string, headers []headerEntry) error {
	if _, err := fmt.Fprintf(w, "%s %s HTTP/1.1\r\n", method, url); err != nil {
		return fmt.Errorf("write request line: %w", err)
	}
	seenHost := false
	if host != "" {
		if _, err := fmt.Fprintf(w, "Host: %s\r\n", host); err != nil {
			return fmt.Errorf("write Host: %w", err)
		}
		seenHost = true
	}
	hasTE := false
	for _, h := range headers {
		if strings.EqualFold(h.Name, "Content-Length") {
			continue
		}
		if strings.EqualFold(h.Name, "Host") {
			seenHost = true
			if _, err := fmt.Fprintf(w, "Host: %s\r\n", h.Value); err != nil {
				return fmt.Errorf("write Host: %w", err)
			}
			continue
		}
		if strings.EqualFold(h.Name, "Transfer-Encoding") {
			hasTE = true
		}
		if _, err := fmt.Fprintf(w, "%s: %s\r\n", h.Name, h.Value); err != nil {
			return fmt.Errorf("write header %s: %w", h.Name, err)
		}
	}
	if !seenHost {
		// Always emit a Host — the guest's net/http may sniff for
		// it; the v1 shell script always wrote one.
		if _, err := fmt.Fprintf(w, "Host: 10.0.0.2\r\n"); err != nil {
			return fmt.Errorf("write default Host: %w", err)
		}
	}
	if !hasTE {
		if _, err := fmt.Fprintf(w, "Transfer-Encoding: chunked\r\n"); err != nil {
			return fmt.Errorf("write Transfer-Encoding: %w", err)
		}
	}
	if _, err := fmt.Fprintf(w, "\r\n"); err != nil {
		return fmt.Errorf("write header terminator: %w", err)
	}
	return nil
}

// writeChunkedBody copies src to the chunked-encoded dst, emitting
// 8 KiB chunks with hex-size lines and CRLF terminators. The
// trailing "0\r\n\r\n" is emitted on EOF. Returns the first I/O
// error encountered (typically the ctx-watcher closing the conn).
func writeChunkedBody(dst io.Writer, src io.Reader) error {
	buf := make([]byte, requestChunkSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := fmt.Fprintf(dst, "%x\r\n", n); werr != nil {
				return werr
			}
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			if _, werr := fmt.Fprintf(dst, "\r\n"); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			if _, werr := fmt.Fprintf(dst, "0\r\n\r\n"); werr != nil {
				return werr
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// headerEntry is one (name, value) pair from FAAS_BRIDGE_HEADERS.
type headerEntry struct {
	Name  string
	Value string
}

// parseHeaders splits a comma-separated `k=v,k=v` string into
// header entries. Split on the FIRST `=` so values may contain `=`.
// Empty names are dropped. Names are returned verbatim (the
// vmmd caller already lower-cased or canon-cased them via the
// original `textproto.MIMEHeader`); we pass through unchanged
// since the H1 wire is case-insensitive on header names.
func parseHeaders(s string) []headerEntry {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]headerEntry, 0, len(parts))
	for _, p := range parts {
		eq := strings.IndexByte(p, '=')
		if eq <= 0 {
			continue
		}
		out = append(out, headerEntry{
			Name:  p[:eq],
			Value: p[eq+1:],
		})
	}
	return out
}

// newBufioReader is a tiny indirection so future tuning of the
// buffer size (currently stdlib default 4096) is one edit. The
// guest at 10.0.0.2:<port> typically returns <4 KiB headers and
// small bodies per request; stdlib default is fine.
func newBufioReader(r io.Reader) *bufio.Reader {
	return bufio.NewReader(r)
}

// parseDeadline accepts either an RFC3339 timestamp or a Go
// duration string ("24h", "30m"). An empty string falls back to
// defaultSessionDeadline-from-now.
func parseDeadline(s string) (time.Time, error) {
	if s == "" {
		return time.Now().Add(defaultSessionDeadline), nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("duration %q must be positive (got %v)", s, d)
		}
		return time.Now().Add(d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		if t.Before(time.Now()) {
			return time.Time{}, fmt.Errorf("RFC3339 timestamp %q is in the past", s)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("not a duration or RFC3339 timestamp")
}
