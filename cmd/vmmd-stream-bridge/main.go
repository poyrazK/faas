// Command vmmd-stream-bridge (issue #686) is the inner-leg H2C
// streaming bridge for the gatewayd-internal → vmmd → guest path.
//
// The legacy streaming bridge (buildStreamingBridgeScript in
// pkg/vmmdgrpc/forward.go:989) shell-scripts a `/dev/tcp` dial and
// hard-codes HTTP/1.1 on the wire (forward.go:998). With the
// gatewayd-public → gatewayd-internal hop now running H2C
// (ADR-070 + PR #713/#719), the inner leg is the last plaintext
// hop in the chain. vmmd-stream-bridge replaces the shell
// bridge with a small Go binary that:
//
//  1. Listens on a unix socket inside the host netns (the binary
//     is spawned via `ip netns exec <netns> …` so it inherits the
//     per-instance netns; ADR-009's strict-netns invariant is
//     preserved by the spawn shape, not the binary itself).
//
//  2. Speaks H2C on that socket (cleartext HTTP/2, no TLS).
//
//  3. For each inbound H2C request, opens a fresh TCP connection
//     to the guest at 10.0.0.2:<port> and bridges the envelope:
//
//     - app_protocol=http1 (default) →
//
//     HTTP/1.1 + chunked transfer encoding (legacy v1 contract,
//     matches the shell bridge so guest-side net/http sees the
//     same envelope). See handleH1Stream in main.go.
//
//     - app_protocol ∈ {http2, grpc} (ADR-126, G19) →
//
//     HTTP/2 prior-knowledge frames end-to-end. The bridge opens
//     a guest-side H2 connection, sends HEADERS + DATA frames
//     mirroring the inbound request, and forwards response H2
//     frames (including trailers for gRPC) verbatim. See
//     h2c_terminator.go::handleH2CStream.
//
//     The framing selection is per-stream via the
//     FAAS_BRIDGE_PROTOCOL env var (read in framing.go).
//     Operators can force H1 for any app with
//     FAAS_BRIDGE_PROTOCOL=h1 (the surgical rollback per
//     ADR-126 §Decision 7).
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
//	FAAS_BRIDGE_HEADERS = newline-separated k=v pairs, split on the
//	                      first '=' so values may contain '='. Newline
//	                      is the separator because real headers like
//	                      `Accept: text/html, application/json` carry
//	                      commas in their values; CR/LF are illegal in
//	                      HTTP/1.1 field-values. Content-Length is
//	                      dropped (chunked is hard-coded).
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
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	//nolint:staticcheck // golang.org/x/net/http2/h2c is deprecated by the
	// stdlib's http.Protocols.SetUnencryptedHTTP2 (Go 1.24+), but the
	// bitfield-only Protocols API does not expose a way to pin
	// per-protocol http2.Server knobs (MaxConcurrentStreams,
	// MaxReadFrameSize, IdleTimeout, ReadIdleTimeout, PingTimeout).
	// ADR-127 §D2 (Layer 9) is load-bearing on those knobs matching the
	// client-side transport pins; dropping the wrapper would silently
	// regress Layer 9. See the buildServer() docstring for the
	// canonical rationale. Once stdlib exposes a Protocols.Get-style
	// indirection, this annotation can be removed.
	"golang.org/x/net/http2/h2c"

	"github.com/onebox-faas/faas/pkg/api"
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
	// Remove the socket when the bridge exits so a vmmd crash or
	// parent-death cleanup does not leave a path behind until the next
	// request. vmmd also performs host-side cleanup; this local defer
	// covers exits that bypass the vmmd handler.
	defer func() { _ = os.Remove(bind) }()
	defer func() { _ = ln.Close() }()

	// chmod 0600 — only vmmd (and the jailer user) can dial this
	// socket. Per spec §11 the host /var/run/faas/stream/ is mode 0700
	// so this is belt-and-braces, but the explicit chmod is the source
	// of truth per the manpage convention.
	if err := os.Chmod(bind, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "chmod %s: %v\n", bind, err)
		os.Exit(3)
	}

	srv := buildServer(guestIP, uint16(port), deadline)
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

// buildServer constructs the stdlib http.Server the bridge
// listens on for FAAS_BRIDGE_PROTOCOL=h2c traffic.
//
// ADR-127 §D2 (Layer 9) — the inner Handler is wrapped with
// h2c.NewHandler so the listener negotiates H2C prior-knowledge
// AND we get to pin per-protocol http2.Server knobs that
// stdlib's Protocols.SetUnencryptedHTTP2 API (Go 1.24+) does
// not expose. The handler argument is the newHandler closure;
// the http2.Server is config-only — the runtime server is the
// one stdlib builds inside h2c.NewHandler and attaches to the
// wrapped handler.
//
// This function is the test seam for the listener-level
// hardeners (TestMain_ServerHardeners). A refactor of the
// h2c.MaxConcurrentStreams / h2cMaxReadFrameSize / IdleTimeout /
// ReadIdleTimeout / PingTimeout block must update both sites
// (this function and any sibling test) in lockstep; the comment
// block at h2c_terminator.go::h2cMax* holds the constant
// rationale.
//
// Pin rationale (the canonical location is here, not in tests):
//   - MaxConcurrentStreams = 100 — matches gatewayd-public cap
//     (pkg/gateway/internal_proxy.go).
//   - MaxReadFrameSize     = 1 MiB — matches h2cMaxReadFrameSize
//     on the client transport (symmetry contract).
//   - IdleTimeout          = 60s — connection idle cap.
//   - ReadIdleTimeout      = 30s — half of IdleTimeout; stdlib
//     will Ping at this point.
//   - PingTimeout          = 15s — response budget for the
//     PING/PINGACK round-trip.
//   - WriteByteTimeout     = 0 (UNSET) — see inline rationale.
//     Capping DATA-frame pacing would break SSE / long-poll /
//     gRPC server-streaming whose DATA frames span >30s. The
//     guest's per-request context bounds end-of-stream, not us.
//
// Outer http.Server pins:
//   - ReadHeaderTimeout  = 10s — H2C connection preface budget,
//     generous on a same-host unix socket.
//   - MaxHeaderBytes     = api.DefaultMaxHeaderBytes (1 MiB) —
//     prevents HPACK-decode allocation attacks on a 1 MiB
//     header list.
//   - IdleTimeout        = 120s — connection idle lifetime cap.
//   - ReadTimeout        = 0 (UNSET) — stdlib's ReadTimeout
//     caps the ENTIRE request lifetime (headers + body), not
//     just headers; a 30s cap would regress H1 streaming
//     uploads (Hobby+ allows 100 MB) and any H2C request whose
//     body takes >30s. ReadHeaderTimeout above is the
//     Slowloris defence.
//   - WriteTimeout       = 0 (UNSET) — streaming (SSE /
//     long-poll / H2 DATA frames past the deadline) is the
//     supported shape for both H1 and H2C paths.
func buildServer(guestIP string, guestPort uint16, deadline time.Time) *http.Server {
	return &http.Server{
		//nolint:staticcheck // ADR-127 §D2 (Layer 9) — the inner Handler is wrapped with
		// h2c.NewHandler so the listener negotiates H2C prior-knowledge
		// AND we get to pin per-protocol http2.Server knobs that
		// stdlib's Protocols.SetUnencryptedHTTP2 API (Go 1.24+) does
		// not expose. The handler argument is the newHandler closure;
		// the http2.Server is config-only — the runtime server is the
		// one stdlib builds inside h2c.NewHandler and attaches to the
		// wrapped handler. See the import-block comment for the same
		// rationale; once stdlib exposes a Protocols.Get indirection,
		// both annotations can be removed in lockstep.
		Handler: h2c.NewHandler(newHandler(guestIP, guestPort, deadline), &http2.Server{
			MaxConcurrentStreams: h2cMaxConcurrentStreams, // 100
			MaxReadFrameSize:     h2cMaxReadFrameSize,     // 1 MiB
			// MaxHeaderListSize is a client-side SETTINGS capability
			// (SETTINGS_MAX_HEADER_LIST_SIZE) and isn't an
			// x/net/http2.Server field. The stdlib http.Server's
			// MaxHeaderBytes above is the server-side equivalent
			// — caps the per-request header list at 1 MiB and is
			// applied before HPACK decode allocates per-name
			// memory.
			IdleTimeout:     60 * time.Second,
			ReadIdleTimeout: 30 * time.Second,
			PingTimeout:     15 * time.Second,
			// WriteByteTimeout is intentionally UNSET (default 0 = unbounded). A 30s cap would terminate SSE responses, long-poll responses, and gRPC server-streaming responses whose DATA frames are spaced >30s apart — the canonical H2C use case. The bridge is the host-side proxy; the guest's per-request context bounds end-of-stream, not us.
		}),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    api.DefaultMaxHeaderBytes, // 1 MiB
		IdleTimeout:       120 * time.Second,
		// ReadTimeout is intentionally UNSET. stdlib's ReadTimeout caps the ENTIRE request lifetime (headers + body), not just the headers; a 30s cap would regress slow H1 uploads (Hobby+ plans allow up to 100 MB streaming body) and any H2C request whose body takes >30s. The ReadHeaderTimeout above is the Slowloris defence; the per-request ctx deadline bounds the in-flight request lifetime. WriteTimeout is also UNSET — streaming (SSE / long-poll / H2 DATA frames past the deadline) is the supported shape for both H1 and H2C paths.
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
//	FAAS_BRIDGE_PROTOCOL selects the per-stream framing (ADR-126)
//	                    → "h1" (legacy, default) or "h2c" (new)
//
// Per-stream framing dispatch (ADR-126 §Decision 1):
//
//   - FAAS_BRIDGE_PROTOCOL=h1 (default; app_protocol=http1) →
//     handleH1Stream — unchanged from the v2 cutover (PR #750).
//   - FAAS_BRIDGE_PROTOCOL=h2c (app_protocol in {http2, grpc}) →
//     handleH2CStream — new H2C terminator (h2c_terminator.go)
//     that originates HTTP/2 prior-knowledge frames to the guest.
//
// We do NOT keep a long-lived guest conn per H2C stream. The
// guest at 10.0.0.2:<port> is either HTTP/1.1 (legacy) or
// HTTP/2 prior-knowledge (new H2C path) and a long-lived conn
// would have to serialize requests through it; the simpler shape
// is "one H2C request = one guest dial." A future optimisation
// (HTTP/2 stream multiplexing across N guest streams on one conn)
// is out of scope for the cutover.
func newHandler(guestIP string, guestPort uint16, deadline time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read FAAS_BRIDGE_PROTOCOL once per request: currentBridgeFraming uses the same value for dispatch, and the slog line below logs the raw string verbatim (operator correlation with FAAS_BRIDGE_PROTOCOL env flips). One syscall beats two.
		bridgeProtoEnv := os.Getenv("FAAS_BRIDGE_PROTOCOL")
		framing := currentBridgeFramingFrom(bridgeProtoEnv)
		// ADR-127 §D3 (Layer 7) — framing-selection slog line. Captured at DEBUG, not Info: at Scale plan (5000 rps/account × N accounts × many bridge processes), per-request Info emission generates tens of thousands of JSON lines/sec/box and buries operator queries under journald's rate limit. Debug is the right level — the operator opts in via FAAS_LOG_LEVEL=debug (the canonical env flag, parsed at pkg/wire/ParseLevel) when investigating a framing question. The counter vmmd_bridge_framing_total carries the same information as a queryable metric; the dashboard panel 1 (bridge-protection deploy/grafana/bridge-protection.json) is the operator's primary view.
		slog.Debug("vmmd-stream-bridge: framing selected",
			"framing", framing.String(),
			"app_protocol_env", bridgeProtoEnv,
			"guest", net.JoinHostPort(guestIP, strconv.FormatUint(uint64(guestPort), 10)),
			"method", r.Method,
			"path", r.URL.Path,
		)
		// ADR-127 §D3 (Layer 7) — bridge frames its own framing
		// decision into the response head so vmmd can increment
		// vmmd_bridge_framing_total on the read side. The header
		// is set BEFORE the dispatch to handleH1Stream /
		// handleH2CStream so both paths inherit it (stdlib honours
		// pre-write w.Header().Set calls). vmmd's
		// ForwardHTTPStream v2 path extracts this header on the
		// other end and computes match/mismatch against
		// reqInit.AppProtocol via appProtocolToBridgeProtocol. The
		// header is dropped from the initHeaders sent down the
		// gRPC stream (it is a control-plane signal, not a guest
		// response header); see pkg/vmmdgrpc/forward.go loop at
		// the response-header mirror site.
		w.Header().Set("X-Faas-Bridge-Framing", framing.String())
		switch framing {
		case framingH2C:
			handleH2CStream(w, r, guestIP, guestPort, deadline)
		default:
			handleH1Stream(w, r, guestIP, guestPort, deadline)
		}
	})
}

// handleH1Stream is the legacy H1+chunked framing path (today's
// contract verbatim, refactored from newHandler per ADR-126 §Decision 1).
// app_protocol=http1 (default) rides this path; setting
// FAAS_BRIDGE_PROTOCOL=h1 on the bridge process forces it for
// any app_protocol (the surgical rollback switch).
func handleH1Stream(w http.ResponseWriter, r *http.Request, guestIP string, guestPort uint16, deadline time.Time) {
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

	// Defense-in-depth CR/LF sanitization. vmmd already strips
	// CR/LF in streamBridgeEnv (pkg/vmmdgrpc/forward.go), but
	// the bridge is a stand-alone binary that may be invoked
	// from other surfaces (tests, future operator override,
	// `FAAS_BRIDGE_*=value` env-set on a misconfigured host).
	// Stripping again here means a hostile or buggy caller
	// cannot smuggle a header line into the trusted inner
	// envelope via FAAS_BRIDGE_HOST / FAAS_BRIDGE_HEADERS.
	// CR/LF are illegal in HTTP/1.1 field-values (RFC 9110
	// §5.5); stripping is lossless for legitimate input.
	method = sanitizeCRLF(method)
	url = sanitizeCRLF(url)
	host = sanitizeCRLF(host)
	for i := range extraHeaders {
		extraHeaders[i].Name = sanitizeCRLF(extraHeaders[i].Name)
		extraHeaders[i].Value = sanitizeCRLF(extraHeaders[i].Value)
	}

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
	if err := writeH1RequestHead(conn, method, url, host, guestIP, guestPort, extraHeaders); err != nil {
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

	// Drain the body goroutine best-effort. The handler is
	// returning; if writeChunkedBody hasn't finished (e.g.
	// the guest stopped reading the request body and r.Body
	// never reaches EOF), don't block here — the ctx watcher
	// has already closed conn, which will unblock r.Body.Read
	// with `use of closed network connection` and the body
	// goroutine will exit on its own. We only wait briefly
	// so a clean finish logs any encoding error to stderr.
	select {
	case err := <-bodyErr:
		if err != nil && !errors.Is(err, io.EOF) {
			// Best-effort: the response is already written,
			// so we can't change the status. Log to stderr.
			fmt.Fprintf(os.Stderr, "writeChunkedBody: %v\n", err)
		}
	case <-ctx.Done():
		// Body goroutine still running; the ctx watcher
		// already closed conn — let it finish on its own.
	}
}

// writeH1RequestHead writes the HTTP/1.1 request line + headers
// + `Transfer-Encoding: chunked` to the guest conn. The header
// list is the FAAS_BRIDGE_HEADERS entries with Content-Length
// dropped (chunked is hard-coded). Headers go through the
// `textproto.MIMEHeader.Set` canonical form (Title-Case header
// names, no trailing whitespace on values).
func writeH1RequestHead(w io.Writer, method, url, host, hostIP string, hostPort uint16, headers []headerEntry) error {
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
		// it; the v1 shell script always wrote one. The v1 script
		// emitted `Host: 10.0.0.2:<port>` (forward.go:1007-1008)
		// so vhost routers (Nginx server_name, Express vhost,
		// Rails request.host) see the port too. v2 must match.
		// Falling back to 10.0.0.2 only is a regression: an app
		// pinned to AppPort=3000 (per-deployment override) would
		// route on `Host: 10.0.0.2` (port-less) and miss the
		// server_name match.
		if _, err := fmt.Fprintf(w, "Host: %s:%d\r\n", hostIP, hostPort); err != nil {
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

// headerEntry + parseHeaders + sanitizeCRLF moved to framing.go
// (ADR-126 §Decision 1) so the h1 and h2c framing paths share one
// parser + one CR/LF strip — single source of truth for the
// trusted inner envelope sanitization.

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
