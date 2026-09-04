// Issue #98 / ADR-028: vmmd's HTTP bridge into per-instance netns.
//
// gatewayd-internal's hot path speaks HTTP to vmmd over the Tailscale/Wireguard
// overlay (no second transport — pkg/wire.DialContext does TCP+overlay+mTLS
// already, issue #95). vmmd receives the request, nsenter's the
// per-instance netns, dials netns.GuestIP:netns.AppPort on the inner side,
// streams the response back as a bidi gRPC stream, and exits.
//
// Why gRPC-bridged netns forwarding instead of a per-instance HTTP listener
// bound on the host side: the latter would need one new TCP port per live
// instance (range-allocator + nft publish per Wake + scan-free collision
// detection) AND a second dial leg on the gateway side. This design keeps
// vmmd's listener count flat at one — ForwardHTTPStream is the ONLY RPC
// surface today — and inherits all the auth + overlay configuration from
// the existing vmmd gRPC server.
//
// Why nsenter rather than bind-mounting the per-instance netfs into vmmd's
// process namespace: nsenter is exactly the resume-hook pattern ADR-022
// already uses (guest-init) and it stays inside the kernel's namespace
// boundary so the per-instance nftables ruleset (forward chain, egress
// deny list, per-plan tc cap) keeps policing traffic exactly as if the
// guest were talking to a local caller. The bridge only translates the
// transport; it does NOT widen the egress policy.
//
// Failure → gRPC status:
//   - Unknown instance → NotFound (the gateway will re-wake on the next
//     request and the placement engine will pick a fresh node).
//   - nsenter failure (netns gone, kernel EACCES) → Internal. nsenter can
//     only fail on a real kernel bug; logging is enough.
//   - guest dial refused / read timeout → Unavailable. The next gateway
//     retry should re-wake; surfacing Unavailable is what tells the
//     gateway "this node is sick, drop the cached target".
//
// All caps live as exported package-level constants so the proto file's
// inline docstring is the only place they have to be repeated.
//
// PR-D / ADR-047: the pre-PR-D legacy unary FetchHTTP RPC was removed —
// streaming is the only bridge today.

package vmmdgrpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ForwardStreamMaxBodyBytes is the per-request body cap on the
// streaming path (ADR-047 PR-B + PR-C, PR-D). Mirrors the
// Hobby/Pro/Scale 100 MB cap in pkg/api.MaxResponseBodyBytes. The
// pre-PR-D legacy unary path was capped at 25 MiB; PR-D removes
// that path so this is the only body cap.
const ForwardStreamMaxBodyBytes = 100 * 1024 * 1024

// ForwardStreamResponseTimeout is the bridge-side response timeout
// on the streaming path (ADR-047 PR-B + PR-C, PR-D). Mirrors the
// Hobby/Pro/Scale ResponseWriteTimeout (15 min / 900 s) so an LLM
// stream that takes 30 s end-to-end fits comfortably inside the
// window.
const ForwardStreamResponseTimeout = 900 * time.Second

// streamBridgeSessionDeadline is the wall-clock ceiling for a
// single v2 bridge session. Matches rawStreamSessionDeadline in
// pkg/gateway/forwardproxy.go (24 h). The bridge binary enforces
// it internally (cmd/vmmd-stream-bridge/main.go::defaultSessionDeadline);
// vmmd passes the timestamp as argv[4] so the bridge can hand it
// to context.WithDeadline on the per-request goroutine.
const streamBridgeSessionDeadline = 24 * time.Hour

// streamBridgeSocketReadyTimeout covers the host-side `ip netns exec`
// startup cost before the v2 bridge binds its per-instance socket. The old
// 50 ms probe was fast on a developer laptop but routinely killed a valid
// bridge on a loaded compute node before it could bind, turning every first
// request into a misleading 503.
const streamBridgeSocketReadyTimeout = 2 * time.Second

// streamBridgeShutdownTimeout bounds the cleanup wait for a child that does
// not honor SIGTERM. Every spawned bridge must be reaped before the handler
// returns, but a wedged child must not pin the vmmd RPC forever.
const streamBridgeShutdownTimeout = 5 * time.Second

// ForwardHTTPStream (issue #471 PR-B + PR-C / ADR-047) is the
// bidi bridge the gatewayd-internal hot path uses for every request. Wire
// shape:
//
//	client → server: 1× ForwardHTTPRequestInit, then N× body_chunk
//	                 (where the chunks stream in as r.Body is read
//	                 by the gateway's forwardproxy)
//	server → client: 1× ForwardHTTPResponseInit (status + headers),
//	                 then N× body_chunk (as they arrive from the
//	                 bridge script's response loop)
//
// On the streaming path the bridge script pipes the request body
// from a named FIFO (or stdin), reads the response status+headers
// + body in a streaming loop, and the Go-side server shuttles
// each chunk to the gRPC client. The bridge itself is unchanged
// in shape — only the body plumbing differs (chunked reads on
// stdout instead of cat slurp).
//
// Why bidi instead of server-streaming-only: a streaming response
// is often paired with a streaming request body (an SSE handler
// consuming a client feed). Bidirectional streaming keeps the
// protocol symmetric; client retry semantics are unchanged (the
// request is still scoped to a single bidi stream).
//
// The pre-PR-D legacy unary RPC was removed in PR-D — the
// streaming RPC is the only bridge today.
func (s *Server) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	const op = "ForwardHTTPStream"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	ctx := stream.Context()

	// 1. Receive the init frame. The bidi protocol is:
	//    [init] [body_chunk]…  on the inbound side; the server
	//    treats everything before the first init as a protocol
	//    error (the gateway always sends init first).
	init, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "expected init frame: %v", err)
	}
	reqInit := init.GetInit()
	if reqInit == nil {
		return status.Error(codes.InvalidArgument, "first frame must be ForwardHTTPRequestInit")
	}
	if reqInit.GetInstance() == "" {
		return status.Error(codes.InvalidArgument, "instance is required")
	}
	// PR-B (issue #462): in-flight request accounting on the
	// streaming bridge. Begin as soon as the init frame is
	// validated; End runs via defer after the bridge returns
	// or errors. The pair is request-count, not
	// connection-count — the counter captures concurrent
	// ForwardHTTPStream RPCs in flight on vmmd.
	//
	// Streaming-trap note (pkg/fcvm/activity/doc.go "Future
	// work"): the Begin/End pair runs over the whole RPC,
	// not strictly per Recv/Send cycle. A streaming RPC that
	// idles 900 s on the bridge would still hold a gauge
	// tick — this is acceptable as an upper bound on
	// concurrency; a stricter per-chunk accounting would
	// re-enter Begin/End inside the body goroutine and add
	// lock contention for marginal signal value.
	s.beginActivity(reqInit.GetInstance())
	defer s.endActivity(reqInit.GetInstance())
	// Cap-lift (ADR-047 PR-B + PR-C, PR-D finalization): the
	// streaming RPC is the only bridge today, so the base
	// is the streaming cap (100 MiB / 900 s). The legacy
	// unary path that used 25 MiB / 60 s was removed in PR-D.
	// `stream` is still read off the init frame for
	// forward-compatibility with the now-removed unary
	// bridge — a future RPC could re-introduce a smaller
	// canned response for cached HTML pages, but the wire
	// shape today inheres in the streaming envelope.
	maxBody := int64(ForwardStreamMaxBodyBytes)
	respTimeout := ForwardStreamResponseTimeout
	if !reqInit.GetStream() {
		// A non-streaming init frame on the streaming RPC
		// (the legacy `stream=true` field was unimplemented in
		// some early bridge scripts) is treated as the
		// smaller cap. Today gatewayd-internal always sets stream=true
		// (handler.go:setupStreamingWriter stamps the header),
		// so this branch is unreachable from production
		// gatewayd-internal but the guard is cheap insurance.
		maxBody = 25 * 1024 * 1024
		respTimeout = 60 * time.Second
	}

	netnsName, ok := s.vmm.NetnsFor(reqInit.GetInstance())
	if !ok {
		return status.Errorf(codes.NotFound, "instance %q not live", reqInit.GetInstance())
	}

	// Inner-leg bridge selection (issue #686 / ADR-028 draft).
	//   v1 (default): shell-bridge script, hard-coded HTTP/1.1.
	//   v2: H2C-speaking vmmd-stream-bridge binary; staged in the
	//       jailer tmpfs but NOT used by default until a follow-up
	//       PR confirms end-to-end on `make metal-lima`.
	// The selection is a runtime var (not build tag) so the cutover
	// is a one-line change once H2C framing is metal-verified.
	// IMPORTANT: the lookup must be per-request (not captured at
	// package init) so FAAS_STREAM_BRIDGE_VERSION=v1 flips the
	// bridge path live without a vmmd restart. ADR-028 amendment
	// explicitly promises this no-deploy rollback; capturing the
	// env at init time breaks that promise silently.
	if currentStreamBridgeVersion() == "v2" {
		return s.forwardHTTPStreamV2(stream, reqInit, netnsName, maxBody, respTimeout)
	}

	// 2. Bridge the request body via a temp file (so the shell
	//    script can `cat` it without colliding stdin with the
	//    response read) AND a streaming pipe so the body can
	//    grow as chunks arrive. The simplest correct shape: stage
	//    init headers + dial line in the script as today, but
	//    defer the body bytes to streaming reads. The bridge
	//    script takes the body from stdin in chunks.
	//
	//    Architecture: parent goroutine reads from the gRPC
	//    stream and writes body chunks to a pipe; the bridge
	//    script's stdin is the pipe's read end. When the parent
	//    goroutine sees io.EOF on the stream (gateway signaled
	//    end-of-body), it closes the pipe; the bridge reads EOF
	//    from cat, finishes its response write, and exits.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return status.Errorf(codes.Internal, "pipe: %v", err)
	}
	defer func() { _ = stdinR.Close() }()
	// stdinW.Close is idempotent and a no-op after the first close
	// (the body goroutine below closes it once the gRPC stream
	// signals end-of-body; the deferred close here is the
	// belt-and-suspenders for the cmd.Start error path and any
	// early returns). The defer also drains any pending writes
	// from the body goroutine before closing.
	defer func() { _ = stdinW.Close() }()

	// 3. Spawn the bridge. Bridge stdout pipes to the server
	//    goroutine below via an os.Pipe (NOT a buffer) so the
	//    server can stream the response body chunks out the
	//    gRPC bidi stream as the bridge writes them — the
	//    buffering path defeats the streaming purpose. Stderr
	//    is captured for the Unavailable surfacing path on
	//    bridge failure.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return status.Errorf(codes.Internal, "stdout pipe: %v", err)
	}
	defer func() { _ = stdoutR.Close() }()

	// The v1 compatibility bridge uses Bash's /dev/tcp pseudo-device.
	// Launching it with /bin/sh works on some images only until the first
	// request, then dash reports "/dev/tcp/...: Directory nonexistent" and
	// the documented live rollback becomes unusable. Keep the legacy script
	// isolated to bash explicitly; v2 remains the default and does not
	// depend on a shell.
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName, "bash", "-c",
		buildStreamingBridgeScript(reqInit, respTimeout))
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		_ = stdoutW.Close()
		return status.Errorf(codes.Unavailable, "bridge start: %v", err)
	}
	// The bridge owns the write end of stdout; the reader
	// goroutine below owns the read end. Closing stdoutW
	// after cmd.Wait ensures the pipe reader sees EOF.
	stdoutReaderClosed := false
	defer func() {
		if !stdoutReaderClosed {
			_ = stdoutR.Close()
		}
	}()

	// Body-copy goroutine: copies client body_chunks → bridge
	// stdin. Errors are aggregated; the first one cancels the
	// bidi stream via the cancelErr closure capture. The
	// goroutine owns the stdinW writer and closes it on exit —
	// this is the EOF signal that lets the bridge script's
	// stdin read loop return and the process exit. Closing it
	// before reporting via bodyErrCh would race the server's
	// post-loop read; closing AFTER the channel send is safe
	// because the channel is buffered (size 1) and the server
	// only reads from bodyErrCh after the goroutine has
	// returned. (Issue #471 review F1 fix.)
	bodyErrCh := make(chan error, 1)
	go func() {
		var written int64
		// close stdinW exactly once at exit, regardless of
		// which branch returned.
		defer func() { _ = stdinW.Close() }()
		for {
			f, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				bodyErrCh <- nil
				return
			}
			if err != nil {
				bodyErrCh <- err
				return
			}
			chunk := f.GetBodyChunk()
			if len(chunk) == 0 {
				continue
			}
			written += int64(len(chunk))
			if written > maxBody {
				bodyErrCh <- status.Errorf(codes.InvalidArgument,
					"streaming body exceeds %d bytes", maxBody)
				return
			}
			if _, err := stdinW.Write(chunk); err != nil {
				bodyErrCh <- err
				return
			}
		}
	}()

	// 4. Stdout reader goroutine: reads headers from the bridge
	//    stdout pipe, parses them via parseBridgeOutput, sends
	//    the init frame on the gRPC stream, then streams the
	//    body bytes as ForwardHTTPStreamResponse_BodyChunk
	//    frames. The body stream is chunked-decoded on the fly
	//    via httputil.NewChunkedReader when the parsed
	//    Transfer-Encoding header indicates chunked encoding
	//    (issue #471 review F2 fix; the prior buffered +
	//    bytes.Buffer path forwarded raw chunked-encoded bytes
	//    including chunk-size lines to the client).
	//
	//    The reader owns the read end of stdoutR; the bridge
	//    process owns the write end. The reader sees EOF when
	//    the bridge closes its stdout (i.e. when the bridge
	//    process exits after `cat <&3` returns).
	streamErrCh := make(chan error, 1)
	go func() {
		defer close(streamErrCh)
		br := bufio.NewReader(stdoutR)
		// Read until the header/body separator (\n\n). Cap
		// the header read at 64 KiB so a malformed guest
		// that never sends the separator can't OOM the
		// server; in practice, HTTP/1.1 headers are <8 KiB.
		var headBuf bytes.Buffer
		for {
			line, err := br.ReadString('\n')
			if err != nil && line == "" {
				streamErrCh <- status.Errorf(codes.Internal, "read bridge headers: %v", err)
				return
			}
			headBuf.WriteString(line)
			if line == "\n" {
				break
			}
			if headBuf.Len() > 64*1024 {
				streamErrCh <- status.Error(codes.ResourceExhausted, "bridge headers exceed 64 KiB")
				return
			}
		}
		resp, err := parseBridgeOutput(headBuf.Bytes())
		if err != nil {
			streamErrCh <- status.Errorf(codes.Internal, "parse bridge headers: %v", err)
			return
		}

		// Send the init frame first (status + headers, no body yet).
		if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
			Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
				Init: &vmmdpb.ForwardHTTPResponseInit{
					Status:  resp.Status,
					Headers: resp.Headers,
				},
			},
		}); err != nil {
			streamErrCh <- status.Errorf(codes.Internal, "send init: %v", err)
			return
		}

		// Wrap the body stream in a chunked decoder if the
		// guest emitted Transfer-Encoding: chunked. The
		// decoder consumes the chunked framing (size-line,
		// body bytes, CRLF terminator per chunk) and emits
		// the decoded payload as the next Read result;
		// io.EOF signals end-of-body.
		var bodySrc io.Reader = br
		if responseIsChunked(resp.Headers) {
			bodySrc = httputil.NewChunkedReader(br)
		}

		// Stream the body in 8 KiB chunks. The chunk size
		// matches the bridge's per-read `cat <&3` granularity
		// at the byte level — chunks emerge from the bridge
		// as they arrive, the gateway's statusRecorder.Write
		// triggers maybeFlush on the 256 KiB / 200 ms
		// boundary, and the per-flush tx_bytes increments
		// attribute every egress byte to
		// (instance_id, current minute).
		buf := make([]byte, 8*1024)
		for {
			n, rerr := bodySrc.Read(buf)
			if n > 0 {
				if serr := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
					Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{
						BodyChunk: append([]byte(nil), buf[:n]...),
					},
				}); serr != nil {
					streamErrCh <- status.Errorf(codes.Internal, "send body chunk: %v", serr)
					return
				}
			}
			if errors.Is(rerr, io.EOF) {
				streamErrCh <- nil
				return
			}
			if rerr != nil {
				streamErrCh <- status.Errorf(codes.Internal, "read body: %v", rerr)
				return
			}
		}
	}()

	// Drain the body-copy goroutine BEFORE waiting on the bridge.
	// The bridge's stdin read loop only returns EOF when stdinW
	// is closed, and the goroutine closes stdinW on exit (above).
	// Reading from bodyErrCh first forces that sequence:
	//   1. stream.Recv returns io.EOF on the body goroutine
	//   2. body goroutine sends nil to bodyErrCh + closes stdinW
	//   3. bridge stdin read returns EOF → bridge exits
	//   4. cmd.Wait returns promptly
	// (Issue #471 review F1 fix; the prior ordering
	// cmd.Wait → stdinW.Close deadlocked every well-formed
	// streaming request until the 900 s exec.CommandContext
	// killed the bridge.)
	bodyErr := <-bodyErrCh
	bridgeErr := cmd.Wait()
	// Close stdoutW now that the bridge has exited; the reader
	// goroutine will see EOF and exit cleanly.
	_ = stdoutW.Close()
	streamErr := <-streamErrCh
	stdoutReaderClosed = true
	_ = stdoutR.Close()

	if bridgeErr != nil {
		var exitErr *exec.ExitError
		if errors.As(bridgeErr, &exitErr) {
			s.log.Warn("vmmd: streaming bridge non-zero exit",
				"instance", reqInit.GetInstance(),
				"netns", netnsName,
				"exit_code", exitErr.ExitCode(),
				"stderr", stderr.String())
			return status.Errorf(codes.Unavailable,
				"guest unreachable (exit %d): %s",
				exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return status.Errorf(codes.Unavailable, "bridge exec: %v", bridgeErr)
	}
	if bodyErr != nil && !errors.Is(bodyErr, io.EOF) {
		return status.Errorf(codes.InvalidArgument, "body stream: %v", bodyErr)
	}
	if streamErr != nil {
		return streamErr
	}
	return nil
}

// ForwardRawStreamMaxRequestBytes is the default inbound cap on the
// raw-bytes bridge (issue #676 / ADR-080). The value lives in
// pkg/api (api.RawStreamMaxRequestBytes) per CLAUDE.md "every
// plan quota/limit lives in this one table — never inline a
// limit"; this re-export keeps the vmmdgrpc test surface stable
// (pkg/api has no // +build metal counterpart) and documents the
// intent for the test reader. The init frame's max_request_bytes
// is clamped DOWN to api.RawStreamMaxRequestBytes in
// rawBridgeBodyLoop — callers can ask for less, never more.
const ForwardRawStreamMaxRequestBytes = api.RawStreamMaxRequestBytes

// ForwardRawStreamMaxResponseBytes is the per-session egress cap
// on the raw-bytes bridge (issue #676 / ADR-080 follow-up, PR-C).
// Mirrors the inbound cap shape: the gateway stamps the constant
// unchanged onto the init frame (PR-1 / PR-2 placement), and
// rawBridgePumpBody clamps DOWN to api.RawStreamMaxResponseBytes
// so a misconfigured caller cannot grow the cap past the limit.
//
// Sizing rationale: WS workloads are long-lived by design
// (rawStreamSessionDeadline is 24 h, see
// pkg/gateway/forwardproxy.go:464). The 100 MiB per-request cap
// (ForwardRawStreamMaxRequestBytes) bounds a single HTTP request
// body, but a session that holds for hours and streams bytes
// faster than the customer reads them can balloon the gateway's
// bidi goroutine pair. The 1 GiB per-session egress cap is the
// load-bearing memory bound on the bridge side; a guest that
// exceeds it sees the response stream close cleanly with
// ResourceExhausted (mirrors the inbound cap's customer-facing
// signal at rawBridgeBodyLoop). Customer impact: a chat-
// completion WS that streams 100 KB/s for 3 h hits 1 GiB
// cleanly — well above any sane single-session workload; a
// runaway guest app surfaces as a deterministic 502 at the
// gateway rather than an OOM-kill on vmmd.
//
// The cap is NOT a billing surface — the (plan_ram + 8) per-
// running-second cost (CLAUDE.md §"Hard limits") already pays
// for WS residency. The cap is memory-safety + abuse-
// prevention only.
const ForwardRawStreamMaxResponseBytes = api.RawStreamMaxResponseBytes

// vmmdRawBridgePath is the install path of the cmd/vmmd-raw-bridge
// Go binary. Mirrors the deployment convention of vmmd itself
// (/opt/faas/current/bin/vmmd-raw-bridge). MUST be an absolute
// path: vmmd is the only root component on the host (CLAUDE.md)
// and the bridge reads the customer's raw request bytes from
// stdin — a $PATH-relative "vmmd-raw-bridge" lookup would let an
// attacker who can write to any PATH directory plant a malicious
// binary that exfiltrates auth headers. Tests that need to run
// the bridge without the production install override via
// FAAS_VMMD_RAW_BRIDGE_PATH (env var, absolute path only).
const vmmdRawBridgePath = "/opt/faas/current/bin/vmmd-raw-bridge"

// vmmdStreamBridgePath is the absolute path of the vmmd-stream-bridge
// binary that owns the H2C inner-leg path (issue #686 / ADR-028 draft).
// Same privilege-escalation invariant as vmmdRawBridgePath above: no
// $PATH lookup, absolute path only, the env-var override (if used in
// tests) must also be absolute.
//
// The default FAAS_STREAM_BRIDGE_VERSION is "v2" (PR #750, issue #686)
// — the H2C inner-leg bridge wired through `cmd/vmmd-stream-bridge`.
// The v1 codepath (the shell-bridge script that hard-codes HTTP/1.1)
// remains available as the live rollback via
// FAAS_STREAM_BRIDGE_VERSION=v1 on vmmd. The flip was gated by
// `make metal-lima` + TestE2E_Streaming_Metal_H2CInnerLeg.
const vmmdStreamBridgePath = "/opt/faas/current/bin/vmmd-stream-bridge"

// streamBridgeVersion is the active codepath selector. Set to "v2"
// (or override via FAAS_STREAM_BRIDGE_VERSION env var in tests) to
// route the inner leg through the H2C bridge. Default "v2" (issue #686)
// commits to H2C on the inner leg end-to-end. Override via
// FAAS_STREAM_BRIDGE_VERSION=v1 for the live rollback to the
// shell-bridge path while v2 is being validated on the wire.
//
// The lookup is per-request, NOT captured at package init: ADR-028's
// amendment promises a no-deploy rollback (set env var → next RPC
// flips to v1). Package-init capture would silently ignore a
// post-start env mutation and break that promise; see
// currentStreamBridgeVersion below.
//
// The package-level var is kept for two reasons: (1) unit tests
// that pre-set the version via t.Setenv need a synchronous read
// surface, (2) SetStreamBridgeVersion below is the documented seam
// for tests that want to pin the version across a test (avoiding
// the per-request env lookup). Production code MUST NOT read this
// var directly; always go through currentStreamBridgeVersion().
var streamBridgeVersion = "v2"

// SetStreamBridgeVersion overrides the package-level selector for
// the duration of a test. Production must NOT call this — it
// bypasses the env-var rollback story. Tests use t.Cleanup to
// restore the default.
func SetStreamBridgeVersion(v string) {
	streamBridgeVersion = v
}

// currentStreamBridgeVersion is the per-request lookup. It returns
// the FAAS_STREAM_BRIDGE_VERSION env var if set, otherwise the
// default "v2". This is the function production code calls; reading
// the package var directly is a test-only path. Reading the env
// per-request keeps the documented no-deploy rollback honest.
func currentStreamBridgeVersion() string {
	if v := os.Getenv("FAAS_STREAM_BRIDGE_VERSION"); v != "" {
		return v
	}
	return "v2"
}

// persistentStreamBridgeEnabled controls the v2 bridge lifecycle. It is on by
// default because v2 is the production path; setting
// FAAS_STREAM_BRIDGE_PERSISTENT=0 (or false/off/no) restores the original
// process-per-RPC behavior without changing the bridge protocol.
func persistentStreamBridgeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(streamBridgePersistentEnv))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// (No deprecated alias for getStreamBridgeVersion — the previous
// version kept one for external callers, but the symbol was
// unexported and had no callers in-tree; PR #754's lint pass
// removed it. The current entry point is currentStreamBridgeVersion.)

// rawBridgePathEnv is the env-var name that lets the test suite
// (and any future operator override) point at an alternate
// install path without rebuilding. The handler rejects
// non-absolute overrides to keep the privilege-escalation
// invariant — there is no PATH search regardless of how the
// override is supplied.
const rawBridgePathEnv = "FAAS_VMMD_RAW_BRIDGE_PATH"

// resolveRawBridgePath returns the absolute path to the
// vmmd-raw-bridge binary that rawBridgeSpawn will exec. The
// resolution order is:
//
//  1. FAAS_VMMD_RAW_BRIDGE_PATH env var, if set and absolute
//  2. The hard-coded production path
//
// Anything else (relative override, missing binary) returns a
// stable error rather than falling back to a $PATH lookup — the
// legacy shell bridge avoided the same surface by inlining the
// script via `sh -c` (string content is not unlinkable), and the
// Go bridge must hold the same invariant.
func resolveRawBridgePath() (string, error) {
	if override := os.Getenv(rawBridgePathEnv); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("raw bridge override %s=%q must be an absolute path", rawBridgePathEnv, override)
		}
		return override, nil
	}
	return vmmdRawBridgePath, nil
}

// ForwardRawStream (issue #676 / ADR-080) is the raw-bytes bridge
// for Upgrade (WebSocket / h2c / MQTT-over-WS / long-poll) traffic.
// The legacy ForwardHTTPStream strips Upgrade + Connection as
// hop-by-hop headers and the shell-script bridge hard-codes
// Transfer-Encoding: chunked + a Host rewrite — both destroy the
// raw bytes an Upgrade handshake needs. ForwardRawStream carries
// the customer's verbatim HTTP request bytes (status line + headers
// + body) into the guest's netns TCP socket and reads back the
// raw response — no HTTP parsing, no chunked framing, no header
// rewriting by vmmd.
//
// Wire shape (mirrors ForwardHTTPStream):
//
//	client → server: 1× ForwardRawRequestInit (instance, port, max
//	                 request bytes), then N× body_chunk bytes that
//	                 the bridge writes verbatim to the guest TCP
//	                 socket. Half-close = EOF.
//	server → client: 1× ForwardRawResponseInit (status + headers
//	                 + error message), then N× body_chunk bytes
//	                 that the bridge read off the guest TCP
//	                 socket. Server half-closes when the guest
//	                 closes the connection.
//
// The bridge that owns the guest TCP socket is the new
// vmmd-raw-bridge Go binary (cmd/vmmd-raw-bridge/), spawned by
// vmmd under the stream context via `ip netns exec <netns>
// vmmd-raw-bridge <ip> <port>`. The Go binary replaces the bash
// /dev/tcp pattern with explicit Go netns entry + net.Dial + a
// framing protocol that sends the HTTP response head first
// (status line + headers + blank line) and then raw body bytes —
// parseBridgeOutput consumes the head the same way the existing
// shell bridge does. The bridge spawn is identical in shape to
// the existing ForwardHTTPStream handler (cmd + os.Pipe + exec),
// so the cancellation, body-goroutine, and endActivity plumbing
// are line-for-line the same and stripped nothing from the
// existing RPC.
//
// Errors map the same way as ForwardHTTPStream: NotFound for
// unknown instance, Internal for nsenter / bridge crash,
// Unavailable for guest dial refused. The raw RPC is the
// durable Upgrade path; the legacy ForwardHTTPStream is the
// durable plain-HTTP path. Both RPCs coexist per ADR-016.
func (s *Server) ForwardRawStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]) error {
	const op = "ForwardRawStream"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	// Receive the init frame and resolve the netns + dial port
	// (single-frame rule, same contract as ForwardHTTPStream at
	// line 110-124).
	init, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "expected init frame: %v", err)
	}
	reqInit := init.GetInit()
	if reqInit == nil {
		return status.Error(codes.InvalidArgument, "first frame must be ForwardRawRequestInit")
	}
	if reqInit.GetInstance() == "" {
		return status.Error(codes.InvalidArgument, "instance is required")
	}

	// Per-instance concurrency accounting (mirrors ForwardHTTPStream).
	s.beginActivity(reqInit.GetInstance())
	defer s.endActivity(reqInit.GetInstance())

	netnsName, ok := s.vmm.NetnsFor(reqInit.GetInstance())
	if !ok {
		return status.Errorf(codes.NotFound, "instance %q not live", reqInit.GetInstance())
	}
	dialPort := reqInit.GetPort()
	if dialPort == 0 {
		dialPort = uint32(netns.AppPort)
	}

	// Spawn the bridge, wire the body-copy goroutine, then run
	// the head read + body pump. Each step is its own helper so
	// the handler stays under the CLAUDE.md 50-line cap and the
	// individual concerns (process lifecycle, streaming, error
	// mapping) are testable in isolation.
	cmd, stdinR, stdinW, stdoutR, stderr, err := rawBridgeSpawn(stream.Context(), netnsName, dialPort)
	if err != nil {
		return err
	}
	defer func() { _ = stdinR.Close() }()
	defer func() { _ = stdinW.Close() }()
	defer func() { _ = stdoutR.Close() }()

	bodyErrCh, bodyWG := rawBridgeBodyLoop(stream, stdinW, reqInit.GetMaxRequestBytes())

	headReader := bufio.NewReader(stdoutR)
	parsed, headErr := rawBridgeReadHead(headReader, stderr.String())
	if headErr != nil {
		// Send the init frame with Error populated so callers
		// reading init.error (per vmmd.proto:1325-1337) detect
		// the failure on the wire, then return the gRPC error
		// so the standard status mechanism also fires. The init
		// status field carries 502 because the body never
		// started (matches the proto's "before the body even
		// started" framing — see vmmd.proto:1335-1336).
		sendErr := stream.Send(&vmmdpb.ForwardRawResponse{
			Frame: &vmmdpb.ForwardRawResponse_Init{
				Init: &vmmdpb.ForwardRawResponseInit{
					Status: 502,
					Error:  headErr.Error(),
				},
			},
		})
		// Wait for the body goroutine to actually exit (not
		// just probe the channel — a still-blocked goroutine
		// would have us read the zero value and silently drop
		// the error). rawBridgeFinish handles the cmd.Wait +
		// WaitGroup coordination.
		_ = rawBridgeFinish(cmd, bodyErrCh, bodyWG, stream.Context(), stderr.String())
		if sendErr != nil {
			return status.Errorf(codes.Internal,
				"raw bridge head failed (%v) AND init send failed: %v", headErr, sendErr)
		}
		return headErr
	}
	if err := stream.Send(&vmmdpb.ForwardRawResponse{
		Frame: &vmmdpb.ForwardRawResponse_Init{
			Init: &vmmdpb.ForwardRawResponseInit{
				Status:  parsed.Status,
				Headers: parsed.Headers,
			},
		},
	}); err != nil {
		_ = rawBridgeFinish(cmd, bodyErrCh, bodyWG, stream.Context(), stderr.String())
		return status.Errorf(codes.Internal, "raw bridge init send: %v", err)
	}

	if err := rawBridgePumpBody(stream, headReader, parsed.Body, reqInit.GetMaxRequestBytes()); err != nil {
		_ = rawBridgeFinish(cmd, bodyErrCh, bodyWG, stream.Context(), stderr.String())
		return err
	}
	return rawBridgeFinish(cmd, bodyErrCh, bodyWG, stream.Context(), stderr.String())
}

// rawBridgeSpawn resolves the vmmd-raw-bridge binary path, opens
// the stdio pipes, and starts the bridge under `ip netns exec`.
// Returns the running *exec.Cmd + the pipe ends + the stderr
// capture buffer. Callers own stdinR/stdoutR (close on exit) and
// stdinW (closed by the body-loop goroutine on its own exit).
//
// Bridge path resolution: production uses
// /opt/faas/current/bin/vmmd-raw-bridge (resolveRawBridgePath);
// tests override via FAAS_VMMD_RAW_BRIDGE_PATH. The function
// pre-validates the path is absolute and the binary exists so
// the legacy shell bridge's `sh -c <inline-script>` TOCTOU-free
// property is preserved — the resolved file is opened by
// execve(2) before the kernel unlinks its inode can race us.
func rawBridgeSpawn(ctx context.Context, netnsName string, dialPort uint32) (*exec.Cmd, *os.File, *os.File, *os.File, *bytes.Buffer, error) {
	bridgePath, err := resolveRawBridgePath()
	if err != nil {
		return nil, nil, nil, nil, nil, status.Errorf(codes.FailedPrecondition, "raw bridge path: %v", err)
	}
	if _, statErr := os.Stat(bridgePath); statErr != nil {
		return nil, nil, nil, nil, nil, status.Errorf(codes.FailedPrecondition,
			"raw bridge binary missing at %s (override via %s): %v",
			bridgePath, rawBridgePathEnv, statErr)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, nil, nil, status.Errorf(codes.Internal, "pipe: %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, nil, nil, nil, nil, status.Errorf(codes.Internal, "stdout pipe: %v", err)
	}

	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName,
		bridgePath, netns.GuestIP, strconv.FormatUint(uint64(dialPort), 10))
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return nil, nil, nil, nil, nil, status.Errorf(codes.Unavailable, "raw bridge start: %v", err)
	}
	// The bridge owns the write end of stdout; we own the read end.
	// Closing stdoutW after cmd.Wait ensures the pipe reader sees EOF.
	defer func() { _ = stdoutW.Close() }()
	return cmd, stdinR, stdinW, stdoutR, &stderr, nil
}

// rawBridgeBodyLoop copies inbound body_chunks → bridge stdin.
// The goroutine owns stdinW (closes it on exit — the EOF signal
// that lets the bridge's stdin read loop return). The byte
// counter enforces maxBody; past the cap the goroutine stops
// reading and surfaces ResourceExhausted via the returned
// channel.
//
// The caller's maxRequestBytes is clamped DOWN to
// api.RawStreamMaxRequestBytes — the vmmd side is authoritative
// on the cap. A caller that sets max_request_bytes to
// math.MaxInt64 still gets the 100 MiB ceiling, so the
// pkg/api/limits.go invariant ("never inline a limit; the table
// is the source of truth") survives a misconfigured gatewayd-internal.
//
// Returns the body-error channel AND a *sync.WaitGroup that
// signals when the goroutine has actually exited. rawBridgeFinish
// MUST wait on the WaitGroup (not just probe the channel): a
// client disconnect mid-response leaves the goroutine blocked
// in stream.Recv() until the stream context cancels — probing
// the channel while it is still blocked reads the zero value
// (nil) and surfaces OK for a truncated response. The
// combination WaitGroup + stream.Context() makes the disconnect
// detection deterministic.
func rawBridgeBodyLoop(stream grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], stdinW *os.File, maxRequestBytes int64) (chan error, *sync.WaitGroup) {
	maxBody := api.RawStreamMaxRequestBytes
	if maxRequestBytes > 0 && maxRequestBytes < maxBody {
		maxBody = maxRequestBytes
	}
	bodyErrCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { _ = stdinW.Close() }()
		var written int64
		for {
			f, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				bodyErrCh <- nil
				return
			}
			if err != nil {
				bodyErrCh <- err
				return
			}
			chunk := f.GetBodyChunk()
			if len(chunk) == 0 {
				continue
			}
			if written+int64(len(chunk)) > maxBody {
				bodyErrCh <- status.Errorf(codes.ResourceExhausted,
					"raw bridge request body exceeded %d bytes", maxBody)
				return
			}
			if _, err := stdinW.Write(chunk); err != nil {
				bodyErrCh <- err
				return
			}
			written += int64(len(chunk))
		}
	}()
	return bodyErrCh, &wg
}

// rawBridgeReadHead reads the response head from the bridge's
// stdout, parses it via parseBridgeOutput, and returns the
// status + headers + initial body buffer for the caller to ship
// in the ForwardRawResponseInit frame.
//
// On failure the returned error carries an informational
// message; the caller is responsible for sending the
// ForwardRawResponseInit frame with Error populated (so PR-2
// gateway code that reads init.error != ” works) and for
// draining bodyErrCh + cmd.Wait. Body-goroutine drain is the
// caller's job because the caller decides whether to attempt
// the Error-frame send first (the proto's "before the body even
// started" failure signal).
func rawBridgeReadHead(r *bufio.Reader, stderr string) (*parsedBridgeResponse, error) {
	head, err := readUntilBlankLine(r)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "raw bridge head read: %v (stderr=%q)", err, stderr)
	}
	parsed, err := parseBridgeOutput(head)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "raw bridge head parse: %v", err)
	}
	return parsed, nil
}

// rawBridgePumpBody streams the guest's response body to the
// client. The first body buffer (parsed.Body) arrives inside
// the head read; subsequent bytes are read from the same
// bufio.Reader (which the bridge is feeding in 8 KiB-friendly
// chunks). The 8 KiB chunk is hoisted out of the loop so the
// hot path doesn't allocate per iteration.
//
// On any send error the function drains the body goroutine and
// surfaces Internal — the bidi stream is already half-closed by
// the guest at that point.
// rawBridgePumpBody pipes bytes from the bridge's stdout into the
// gRPC response stream. The rx cap (issue #676 / ADR-080 follow-up,
// PR-C) is clamped DOWN to ForwardRawStreamMaxResponseBytes — a
// misconfigured gatewayd-internal cannot grow the cap past the
// limit. The init frame's existing max_request_bytes field is
// reused as the rx-cap source for now (a separate
// max_response_bytes field is a wire change deferred to a follow-up
// ADR; PR-C ships memory-safety without a wire bump). When the cap
// fires the function returns ResourceExhausted, which surfaces as a
// 502 + body="raw bridge response cap exceeded" at the gateway's
// rawStreamOnceWithEvents (mirrors the inbound cap's behaviour at
// rawBridgeBodyLoop).
func rawBridgePumpBody(stream grpc.BidiStreamingServer[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], r *bufio.Reader, initialBody []byte, maxResponseBytes int64) error {
	const respChunkSize = 8 * 1024
	// Clamp DOWN: callers may ask for less, never more. The default
	// (maxResponseBytes == 0) is the api.RawStreamMaxResponseBytes
	// ceiling — a zero init field means "use the platform default",
	// matching the inbound cap's behaviour at rawBridgeBodyLoop:781-784.
	maxBody := ForwardRawStreamMaxResponseBytes
	if maxResponseBytes > 0 && maxResponseBytes < maxBody {
		maxBody = maxResponseBytes
	}
	if len(initialBody) > 0 {
		// The initial body was already counted in parsed.Body by
		// rawBridgeReadHead; if it alone exceeds the cap, surface
		// the cap error immediately so the gateway's
		// rawStreamOnceWithEvents emits a deterministic 502 rather
		// than streaming a partial response.
		if int64(len(initialBody)) > maxBody {
			return status.Errorf(codes.ResourceExhausted,
				"raw bridge response cap exceeded: %d > %d", len(initialBody), maxBody)
		}
		if err := stream.Send(&vmmdpb.ForwardRawResponse{
			Frame: &vmmdpb.ForwardRawResponse_BodyChunk{
				BodyChunk: append([]byte(nil), initialBody...),
			},
		}); err != nil {
			return status.Errorf(codes.Internal, "raw bridge body send: %v", err)
		}
	}
	buf := make([]byte, respChunkSize)
	var totalRx int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			totalRx += int64(n)
			if totalRx > maxBody {
				return status.Errorf(codes.ResourceExhausted,
					"raw bridge response cap exceeded: >%d bytes (initial + body)", maxBody)
			}
			if err := stream.Send(&vmmdpb.ForwardRawResponse{
				Frame: &vmmdpb.ForwardRawResponse_BodyChunk{
					BodyChunk: append([]byte(nil), buf[:n]...),
				},
			}); err != nil {
				return status.Errorf(codes.Internal, "raw bridge body send: %v", err)
			}
		}
		if errors.Is(rerr, io.EOF) {
			return nil
		}
		if rerr != nil {
			return status.Errorf(codes.Internal, "raw bridge body read: %v", rerr)
		}
	}
}

// rawBridgeFinish waits on the bridge process AND the body
// goroutine, then maps the combined result to a gRPC error.
//
// Coordination contract (issue #676 review fix): the body
// goroutine can be blocked in stream.Recv() when the bridge
// exits — probing bodyErrCh at that moment reads the zero
// value (nil) from the buffered channel and the handler
// returns OK on a truncated response. The fix is to wait on
// the WaitGroup so the goroutine has actually exited, and
// fall back to the stream context if the wait would block
// indefinitely (the goroutine's stream.Recv will unblock when
// the client disconnects, but only after the gRPC server
// processes the RST_STREAM — for a hung gateway we cap the
// wait at stream.Context().Done() and derive the body error
// from the context cancellation).
//
// cmd.Wait is load-bearing — without it the child becomes a
// zombie and the captured stderr is unavailable when the
// bridge crashes. ResourceExhausted from the body-loop's
// cap-enforcement branch is the customer-facing signal; any
// other body error is Internal. A clean EOF on both sides
// returns nil.
func rawBridgeFinish(cmd *exec.Cmd, bodyErrCh <-chan error, bodyWG *sync.WaitGroup, streamCtx context.Context, stderr string) error {
	waitErr := cmd.Wait()

	// Wait for the body goroutine to actually exit. If it
	// doesn't exit within the stream-context deadline, the
	// client disconnect path is the cause — surface the
	// context error so the gateway sees Canceled instead of
	// OK on a truncated response.
	var bodyErr error
	doneCh := make(chan struct{})
	go func() { bodyWG.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
		// Goroutine exited; drain the buffered error if any.
		select {
		case bodyErr = <-bodyErrCh:
		default:
		}
	case <-streamCtx.Done():
		// Client disconnected (or stream context cancelled
		// for any other reason). Surface as Canceled so the
		// gateway knows the response was truncated. The
		// goroutine will exit when stream.Recv() unblocks
		// from the context cancellation — but we don't wait
		// for it; the handler is returning now and the
		// goroutine will be cleaned up by the gRPC server's
		// stream teardown.
		bodyErr = status.Errorf(codes.Canceled, "raw bridge body: client disconnected before request body complete: %v", streamCtx.Err())
	}

	if bodyErr != nil {
		if st, ok := status.FromError(bodyErr); ok && st.Code() == codes.ResourceExhausted {
			return bodyErr
		}
		if st, ok := status.FromError(bodyErr); ok && st.Code() == codes.Canceled {
			return bodyErr
		}
		// Bridge crash (non-zero exit) — surface stderr so the
		// gateway can log the bridge's last words.
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) || errors.As(bodyErr, &exitErr) {
			return status.Errorf(codes.Internal, "raw bridge body: %v wait=%v (stderr=%q)", bodyErr, waitErr, stderr)
		}
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return status.Errorf(codes.Internal, "raw bridge exited: %v (stderr=%q)", exitErr, stderr)
		}
	}
	return nil
}

// readUntilBlankLine reads from r until it sees the HTTP/1.1
// head terminator ("\n\n") or EOF. Returns the head bytes WITH
// each line's trailing "\n" intact (parseBridgeOutput's
// contract — see the legacy ForwardHTTPStream head-read at
// line 294-313) and the terminator line itself dropped. The
// body bytes that arrived inside the same read as the head
// remain on the bufio.Reader for the body loop below.
//
// Mirrors the legacy pattern: line-by-line ReadString('\n')
// + a 64 KiB cap. The cap is the load-bearing piece: a
// malicious or buggy guest that never sends the terminator
// must not OOM the server. In practice HTTP/1.1 heads are
// <8 KiB; 64 KiB is the same budget ForwardHTTPStream uses.
//
// Returns the partial bytes + io.EOF when the bridge closes the
// stream mid-head — the caller decides whether the partial
// bytes are a valid head (they almost never are) and surfaces
// a Unavailable to the gateway.
func readUntilBlankLine(r *bufio.Reader) ([]byte, error) {
	const headCap = 64 * 1024
	var out []byte
	for {
		line, err := r.ReadString('\n')
		out = append(out, line...)
		if line == "\n" {
			// terminator line — return everything before it.
			return out[:len(out)-1], nil
		}
		if len(out) > headCap {
			return nil, fmt.Errorf("bridge head exceeds %d bytes", headCap)
		}
		if errors.Is(err, io.EOF) {
			return out, io.EOF
		}
		if err != nil {
			return out, err
		}
	}
}

// buildStreamingBridgeScript (issue #471 PR-B + PR-C / ADR-047)
// is the streaming-only bridge script. Differences from the
// pre-PR-D legacy script (removed in PR-D):
//
//   - Request body comes from stdin in chunks (the Go server's
//     body-copy goroutine writes each ForwardHTTPStreamRequest
//     body_chunk to the bridge's stdin). The script reads
//     stdin and chunked-encodes it to fd 3; EOF on stdin
//     terminates the body read and the bridge continues to
//     read the response.
//   - Response: status line + headers are written to stdout
//     (terminated by a blank line, mirroring the legacy
//     script's parseBridgeOutput contract); the body bytes
//     are then streamed to stdout via a single `cat <&3` —
//     raw from the wire, including chunked framing if the
//     guest emitted `Transfer-Encoding: chunked`. The Go-side
//     ForwardHTTPStream server reads the body stream
//     incrementally via a pipe (NOT a buffer) and applies
//     `httputil.NewChunkedReader` when the parsed
//     Transfer-Encoding header indicates chunked encoding.
//     This is what lets the bridge stay shell-simple while
//     the wire-level decoding happens in Go (where binary
//     data + per-byte framing is straightforward).
//   - Host header rewrite + port-resolution logic mirror the
//     legacy script (the same per-deployment override port,
//     the same inner-IP Host).
//
// The bridge's request path uses the chunked-encoding
// pattern (`read -r -t 1 -n 8192 CHUNK` → `<hex-len>\r\n<body>\r\n`)
// because the gateway already emits Transfer-Encoding: chunked
// to the guest. respTimeout is reserved for a future per-line
// read deadline inside the cat loop (today, `cat <&3` is the
// simpler streaming primitive and the total budget is enforced
// by exec.CommandContext on the Go side).
func buildStreamingBridgeScript(req *vmmdpb.ForwardHTTPRequestInit, respTimeout time.Duration) string {
	dialPort := req.GetPort()
	if dialPort == 0 {
		dialPort = uint32(netns.AppPort)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "set -eu\n")
	fmt.Fprintf(&b, "exec 3<>/dev/tcp/%s/%d\n",
		netns.GuestIP, dialPort)
	// Request line.
	fmt.Fprintf(&b, "printf '%%s %%s HTTP/1.1\\r\\n' %s %s >&3\n",
		shellQuote(req.GetMethod()), shellQuote(req.GetRequestUri()))
	// Host header (rewritten to inner identity).
	fmt.Fprintf(&b, "printf 'Host: %%s\\r\\n' %s >&3\n",
		shellQuote(fmt.Sprintf("%s:%d", netns.GuestIP, dialPort)))
	fmt.Fprintf(&b, "printf 'Transfer-Encoding: chunked\\r\\n' >&3\n")
	// Caller-supplied headers (already had hop-by-hop stripped
	// upstream). Skip Content-Length because chunked encoding
	// governs; emitting both would be a protocol violation.
	for _, h := range req.GetHeaders() {
		if strings.EqualFold(h.GetName(), "Content-Length") {
			continue
		}
		fmt.Fprintf(&b, "printf '%%s: %%s\\r\\n' %s %s >&3\n",
			shellQuote(h.GetName()), shellQuote(h.GetValue()))
	}
	fmt.Fprintf(&b, "printf '\\r\\n' >&3\n")
	// Request body: streaming read from stdin. The Go server
	// writes body_chunk bytes to stdin; EOF on stdin closes
	// the request body and the guest sees the chunked
	// terminator.
	fmt.Fprintf(&b, "while IFS= read -r -t 1 -n 8192 CHUNK; do\n")
	fmt.Fprintf(&b, "  printf '%%x\\r\\n' ${#CHUNK} >&3\n")
	fmt.Fprintf(&b, "  printf '%%s' \"$CHUNK\" >&3\n")
	fmt.Fprintf(&b, "  printf '\\r\\n' >&3\n")
	fmt.Fprintf(&b, "done\n")
	fmt.Fprintf(&b, "printf '0\\r\\n\\r\\n' >&3\n")
	// Response status + headers (terminated by a blank line).
	fmt.Fprintf(&b, "read -r STATUS <&3 || true\n")
	fmt.Fprintf(&b, "printf '%%s\\n' \"$STATUS\"\n")
	fmt.Fprintf(&b, "while IFS= read -r -t %d LINE <&3; do\n",
		int(respTimeout.Seconds()))
	fmt.Fprintf(&b, "  [ -z \"$LINE\" ] && break\n")
	fmt.Fprintf(&b, "  printf '%%s\\n' \"$LINE\"\n")
	fmt.Fprintf(&b, "done\n")
	fmt.Fprintf(&b, "printf '\\n'\n")
	// Body stream: copy raw bytes from fd 3 to stdout. The Go
	// side reads stdout via a pipe (NOT a buffer) and applies
	// chunked decoding if the parsed Transfer-Encoding header
	// indicates chunked encoding. `cat <&3` is the simplest
	// streaming primitive available in the minimal guest base
	// image (POSIX-required, ships in busybox + dash); it
	// exits when the guest closes the connection (the
	// end-of-body signal for both Content-Length and
	// chunked-encoded responses).
	fmt.Fprintf(&b, "cat <&3\n")
	return b.String()
}

// responseIsChunked reports whether the guest's response carries
// a chunked Transfer-Encoding coding (issue #471 PR-B + PR-C /
// ADR-047, F2 review fix). Per RFC 7230 §3.3.1, the
// Transfer-Encoding header value is a comma-separated list of
// codings and tokens are case-insensitive. A "chunked" coding
// anywhere in the list means the body stream needs to be
// decoded via httputil.NewChunkedReader before forwarding to
// the client — otherwise chunk-size lines and CRLF separators
// leak into the response body.
//
// The helper accepts the parsed header slice returned by
// parseBridgeOutput; nil/empty headers yield false (no chunked
// decoding, pass-through).
func responseIsChunked(headers []*vmmdpb.Header) bool {
	for _, h := range headers {
		if !strings.EqualFold(h.GetName(), "Transfer-Encoding") {
			continue
		}
		for _, tok := range strings.Split(h.GetValue(), ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "chunked") {
				return true
			}
		}
	}
	return false
}

// parseBridgeOutput splits "<status>\n<header lines>\n\n<body bytes>"
// back into proto types. The script prints bytes verbatim for the body,
// so binary payloads (image/jpeg, etc.) survive. The return shape
// mirrors the proto envelope minus the wire-only types removed in
// PR-D (the legacy unary ForwardHTTPRequest/ForwardHTTPResponse
// envelopes were torn out — the streaming RPC surfaces the headers
// via ForwardHTTPResponseInit instead).
func parseBridgeOutput(raw []byte) (*parsedBridgeResponse, error) {
	// Split on the first \n\n that marks end-of-headers.
	sep := []byte("\n\n")
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		return nil, fmt.Errorf("bridge: malformed output (no header terminator)")
	}
	head, body := raw[:idx], raw[idx+len(sep):]

	lines := bytes.Split(head, []byte("\n"))
	if len(lines) == 0 {
		return nil, fmt.Errorf("bridge: empty status line")
	}
	statusLine := strings.TrimSpace(string(lines[0]))
	// "HTTP/1.1 200 OK" — take the middle token.
	parts := strings.SplitN(statusLine, " ", 3)
	if len(parts) < 2 {
		return nil, fmt.Errorf("bridge: bad status line %q", statusLine)
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("bridge: bad status code %q", parts[1])
	}
	// Bound check before the int32 cast: a guest app emitting a
	// synthetic status line with a multi-digit code (e.g.
	// "HTTP/1.1 9999") would otherwise wrap to negative in proto's
	// int32 and look like an Unavailable at the gateway.
	if code < 100 || code > 599 {
		return nil, fmt.Errorf("bridge: out-of-range status code %d", code)
	}

	resp := &parsedBridgeResponse{
		Status: int32(code), //nolint:gosec // Bounded above to a valid HTTP status range.
		Body:   body,
	}
	for _, h := range lines[1:] {
		h := string(h)
		if h == "" {
			continue
		}
		colon := strings.IndexByte(h, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(h[:colon])
		value := strings.TrimSpace(h[colon+1:])
		if name == "" {
			continue
		}
		resp.Headers = append(resp.Headers, &vmmdpb.Header{Name: name, Value: value})
	}
	return resp, nil
}

// shellQuote wraps s in single quotes and escapes any embedded single
// quotes. The bridge script never executes caller-controlled bytes
// through eval — the printf format strings are fixed and the only
// caller input is the %s slot — but quoting is cheap insurance.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// parsedBridgeResponse is the local envelope returned by
// parseBridgeOutput. It mirrors the pre-PR-D proto type
// ForwardHTTPResponse (status, headers, body) but lives in the
// package instead of the wire so the streaming RPC can populate
// its own ForwardHTTPResponseInit frame without holding a
// removed proto type. Tests reach the value via
// ParseBridgeOutputForTest.
type parsedBridgeResponse struct {
	Status  int32
	Headers []*vmmdpb.Header
	Body    []byte
}

// ParseBridgeOutputForTest exposes the package's response parser so
// unit tests can drive the pure piece without standing up the full
// ForwardHTTPStream server (which depends on `ip netns exec` and is
// gated to //go:build metal). The signature mirrors parseBridgeOutput
// exactly; the only difference is the visibility. Returning the
// envelope as a pointer, not a value, preserves the byte slice
// ownership (the body carries through without an extra copy).
func ParseBridgeOutputForTest(raw []byte) (ParsedBridgeResponseForTest, error) {
	r, err := parseBridgeOutput(raw)
	if r == nil {
		return ParsedBridgeResponseForTest{}, err
	}
	return ParsedBridgeResponseForTest{
		Status:  r.Status,
		Headers: r.Headers,
		Body:    r.Body,
	}, err
}

// ParsedBridgeResponseForTest is the test-only exported mirror of
// parsedBridgeResponse. We keep the production type unexported
// (its only call site is the server's streaming RPC) and re-export
// a copy under the test-visibility name so the unit tests can read
// the headers and body without leaking the helper into the public
// package surface. The fields are identical to parsedBridgeResponse.
type ParsedBridgeResponseForTest struct {
	Status  int32
	Headers []*vmmdpb.Header
	Body    []byte
}

// BuildStreamingBridgeScriptForTest exposes the streaming script
// generator so unit tests can assert the dial-port + Host header
// rewrite (issue #460 / ADR-053 PR-C) without an ip netns exec.
// Tests grep the rendered script for the dial line + Host header;
// the strings are stable (see buildStreamingBridgeScript doc).
func BuildStreamingBridgeScriptForTest(req *vmmdpb.ForwardHTTPRequestInit, respTimeout time.Duration) string {
	return buildStreamingBridgeScript(req, respTimeout)
}

// keep guard reference: io is used in the body-copy goroutine
// (errors.Is(err, io.EOF)) and net/http is used by the chunked
// decoder wrapper (httputil.NewChunkedReader lives in
// net/http/httputil).
var _ = io.EOF
var _ = http.MethodGet

// beginActivity (PR-B, issue #462) is the nil-safe bridge to
// ActivityTracker.Begin. Centralising the guard keeps every
// caller's defer pair simple and makes the "Server wired without
// activity cache" path a no-op without scattering nil checks.
func (s *Server) beginActivity(instanceID string) {
	if s == nil || s.activity == nil {
		return
	}
	s.activity.Begin(instanceID)
}

// endActivity (PR-B, issue #462) is the nil-safe bridge to
// ActivityTracker.End. Pairs with beginActivity as the defer
// after ForwardHTTPStream's bridge work.
func (s *Server) endActivity(instanceID string) {
	if s == nil || s.activity == nil {
		return
	}
	s.activity.End(instanceID)
}

// forwardHTTPStreamV2 is the H2C inner-leg bridge (issue #686 /
// ADR-028 amendment). It is selected when streamBridgeVersion == "v2"
// (the default after this PR lands). Wire shape:
//
//  1. vmmd starts one /opt/faas/current/bin/vmmd-stream-bridge per live
//     instance inside the netns via `ip netns exec <netns> …`. The bridge
//     listens on /var/run/faas/stream/<instance>.sock and is reaped after
//     an idle timeout or vmmd shutdown.
//  2. Request metadata is sent on each H2C request using private headers;
//     process-wide environment variables are reserved for the rollback
//     lifecycle. This makes concurrent requests independent.
//  3. vmmd reuses an HTTP/2 client connection (plaintext, H2C) to the
//     per-instance unix socket using golang.org/x/net/http2.Transport.
//  4. The body is bridged per-chunk from the gRPC stream into the
//     H2C request body. The bridge chunks the body in 8 KiB
//     chunks via Transfer-Encoding: chunked (the v1 contract).
//  5. The response head is read via http.ReadResponse and sent as
//     ForwardHTTPResponseInit. The response body is streamed as
//     ForwardHTTPStreamResponse_BodyChunk frames. Transfer-Encoding:
//     chunked on the response side is decoded by httputil.NewChunkedReader
//     (matches v1's behaviour at forward.go:355).
//  6. Cancellation: stream.Context() is bound to the H2C request ctx
//     AND to a watcher goroutine that closes the unix socket on
//     cancel — the v1 body-goroutine-leak fix that issue #686 wired
//     through.
//  7. Cleanup: idle bridges and vmmd shutdown close the transport, remove
//     the unix socket, and terminate/reap the child. Set
//     FAAS_STREAM_BRIDGE_PERSISTENT=0 to restore process-per-RPC startup.
//
// Compared to v1 (the shell bridge): the inner leg now speaks H2C
// instead of HTTP/1.1, so gRPC unary and streaming clients see H2
// framing all the way to the guest container. Per-connection
// overhead is reduced by the H2 frame coalescing — the wire is
// also small enough that the liveness checks (e.g.gatewayd-internal
// health probing) keep working without changes.
func (s *Server) forwardHTTPStreamV2(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], reqInit *vmmdpb.ForwardHTTPRequestInit, netnsName string, maxBody int64, respTimeout time.Duration) error {
	const op = "ForwardHTTPStreamV2"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	ctx := stream.Context()
	// respTimeout was the v1 shell-bridge response-writer budget
	// (ForwardStreamResponseTimeout = 900 s). For v2 the bridge
	// session is capped at defaultSessionDeadline (24 h, set below
	// as sessionDeadline) and the H2C request inherits stream.Context()
	// directly. The parameter is kept on the signature so the v1 /
	// v2 dispatch site stays symmetric; vmmd-side callers do not
	// need to know which bridge handles the request.
	_ = respTimeout

	dialPort := reqInit.GetPort()
	if dialPort == 0 {
		dialPort = netns.AppPort
	}
	persistent := persistentStreamBridgeEnabled()
	requestBridgeProtocol := streamBridgeProtocol(reqInit)
	var (
		bridgeLease  *streamBridgeLease
		client       *http.Client
		cmd          *exec.Cmd
		stderr       *bytes.Buffer
		bridgeWaited bool
		err          error
	)
	if persistent {
		if s.streamBridges == nil {
			s.streamBridges = newStreamBridgeManager(s.log)
		}
		bridgeLease, err = s.streamBridges.acquire(ctx, reqInit, netnsName)
		if err != nil {
			s.log.Warn("vmmd: persistent stream bridge unavailable",
				"instance", reqInit.GetInstance(), "netns", netnsName, "err", err)
			return status.Errorf(codes.Unavailable, "stream bridge start: %v", err)
		}
		defer bridgeLease.release()
		client = bridgeLease.entry.client
	} else {
		// Rollback path: retain the original process-per-RPC lifecycle.
		bridgePath, pathErr := resolveStreamBridgePath()
		if pathErr != nil {
			return status.Errorf(codes.FailedPrecondition, "stream bridge path: %v", pathErr)
		}
		if _, statErr := os.Stat(bridgePath); statErr != nil {
			return status.Errorf(codes.FailedPrecondition,
				"stream bridge binary missing at %s (ship via the immutable bundle, see cmd/vmmd-stream-bridge): %v",
				bridgePath, statErr)
		}
		sockPath := streamBridgeSockPathForRequest(reqInit.GetInstance())
		sessionDeadline := time.Now().Add(streamBridgeSessionDeadline).UTC().Format(time.RFC3339)
		bridgeEnv := streamBridgeEnv(reqInit)
		defer func() { _ = os.Remove(sockPath) }()
		cmd, stderr, err = streamBridgeSpawn(ctx, bridgePath, netnsName, sockPath, netns.GuestIP, dialPort, sessionDeadline, bridgeEnv)
		if err != nil {
			s.log.Warn("vmmd: stream bridge spawn failed",
				"instance", reqInit.GetInstance(), "netns", netnsName,
				"guest_ip", netns.GuestIP, "guest_port", dialPort, "err", err.Error())
			return status.Errorf(codes.Unavailable, "stream bridge start: %v", err)
		}
		defer func() {
			if !bridgeWaited {
				_ = stopStreamBridge(ctx, cmd, stderr)
			}
		}()
		if err := waitForUnixSock(sockPath, streamBridgeSocketReadyTimeout); err != nil {
			s.log.Warn("vmmd: stream bridge socket not ready",
				"instance", reqInit.GetInstance(), "netns", netnsName,
				"socket", sockPath, "stderr", stderr.String(), "err", err.Error())
			return status.Errorf(codes.Unavailable, "stream bridge socket not ready: %v (stderr: %s)", err, stderr.String())
		}
		transport := newStreamBridgeH2CTransport(sockPath)
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport}
	}

	bodyPr, bodyPw := io.Pipe()
	// H2C request ctx: inherit stream.Context() directly. v1 used
	// exec.CommandContext(stream.Context(), …) which only cancelled
	// on client disconnect — wrapping with a WithDeadline(time.Now().
	// Add(respTimeout)) silently truncated long-poll / SSE / WS
	// responses at 15 min under v2 (the bridge-internal ctx still
	// has a 24h ceiling via the sessionDeadline above). The bridge
	// also bounds the response-HEAD read at readHeaderTimeout in
	// cmd/vmmd-stream-bridge/main.go:96, so a wedged guest fails
	// loud; streaming bodies use the conn-wide deadline (none here)
	// and rely on the watcher goroutine to close the conn on ctx
	// cancellation.
	reqCtx, reqCancel := context.WithCancel(ctx)
	defer reqCancel()

	method := reqInit.GetMethod()
	if method == "" {
		method = "POST"
	}
	httpReq, err := http.NewRequestWithContext(reqCtx, method, "http://unix"+reqInit.GetRequestUri(), bodyPr)
	if err != nil {
		return status.Errorf(codes.Internal, "build H2C request: %v", err)
	}
	for _, h := range reqInit.GetHeaders() {
		// Skip Transfer-Encoding — the bridge hard-codes chunked.
		if strings.EqualFold(h.GetName(), "Transfer-Encoding") {
			continue
		}
		httpReq.Header.Add(h.GetName(), h.GetValue())
	}
	if persistent {
		// Persistent bridges receive request metadata on this H2C stream,
		// not through process-wide environment variables. The bridge strips
		// these private headers before it forwards anything to the guest.
		httpReq.Header.Set("X-Faas-Bridge-Persistent", "1")
		httpReq.Header.Set("X-Faas-Bridge-Protocol", requestBridgeProtocol)
		httpReq.Header.Set("X-Faas-Bridge-Port", strconv.FormatUint(uint64(dialPort), 10))
		for _, h := range reqInit.GetHeaders() {
			if strings.EqualFold(h.GetName(), "Host") {
				host := sanitizeHeaderValue(h.GetValue())
				if host != "" {
					httpReq.Header.Set("X-Faas-Bridge-Host", host)
					httpReq.Host = host
				}
				break
			}
		}
	}

	// 6. Body goroutine: stream gRPC body_chunks → H2C request
	// body. Mirrors the v1 body goroutine (forward.go:257-288).
	bodyErrCh := make(chan error, 1)
	go func() {
		var written int64
		defer func() { _ = bodyPw.Close() }()
		for {
			f, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				bodyErrCh <- nil
				return
			}
			if err != nil {
				bodyErrCh <- err
				return
			}
			chunk := f.GetBodyChunk()
			if len(chunk) == 0 {
				continue
			}
			written += int64(len(chunk))
			if written > maxBody {
				bodyErrCh <- status.Errorf(codes.InvalidArgument,
					"streaming body exceeds %d bytes", maxBody)
				return
			}
			if _, err := bodyPw.Write(chunk); err != nil {
				bodyErrCh <- err
				return
			}
		}
	}()

	// 7. Issue the bridge request and read the response head. This phase is
	// intentionally measured separately from the full RPC: it captures the
	// vmmd Unix-socket hop plus the bridge-to-guest dial/protocol setup and
	// response-header latency. The metric makes connection-pool changes
	// observable instead of relying on end-to-end latency alone.
	bridgeOp := "bridge_h1_roundtrip"
	if requestBridgeProtocol == "h2c" {
		bridgeOp = "bridge_h2c_roundtrip"
	}
	bridgeStart := time.Now()
	httpResp, err := client.Do(httpReq)
	if s.ops != nil {
		s.ops.Observe(bridgeOp, time.Since(bridgeStart), err)
	}
	if err != nil {
		_ = bodyPr.Close()
		_ = bodyPw.Close()
		var bridgeErr error
		if persistent {
			s.streamBridges.invalidate(bridgeLease)
		} else {
			bridgeErr = stopStreamBridge(ctx, cmd, stderr)
			bridgeWaited = true
		}
		s.log.Warn("vmmd: stream bridge H2C request failed",
			"instance", reqInit.GetInstance(), "netns", netnsName,
			"guest_ip", netns.GuestIP, "guest_port", dialPort,
			"bridge_err", bridgeErr, "err", err.Error())
		return status.Errorf(codes.Unavailable, "H2C request: %v (bridge: %v)", err, bridgeErr)
	}
	defer func() { _ = httpResp.Body.Close() }()

	// 8. Mirror guest response headers into the gRPC init frame.
	initHeaders := make([]*vmmdpb.Header, 0, len(httpResp.Header))
	for k, vs := range httpResp.Header {
		// Bridge-side control-plane header (ADR-127 §D3 Layer 7).
		// The bridge frames its own framing decision here so vmmd
		// can compute match/mismatch for vmmd_bridge_framing_total.
		// Stripped from the gRPC init frame — this is a vmmd↔bridge
		// signal, not a guest response header.
		if strings.EqualFold(k, "X-Faas-Bridge-Framing") {
			continue
		}
		if strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		// Multi-value headers are flattened to one Header entry per
		// value (the proto's Header.Value is single-value; the v1
		// path expands via parseBridgeOutput which produces one
		// entry per value line).
		for _, v := range vs {
			initHeaders = append(initHeaders, &vmmdpb.Header{Name: k, Value: v})
		}
	}

	// 8a. Bridge framing telemetry (ADR-127 §D3 Layer 7). The
	// bridge writes X-Faas-Bridge-Framing into the response head
	// (cmd/vmmd-stream-bridge/main.go::newHandler); we read it here
	// to increment vmmd_bridge_framing_total with the closed
	// cross-product (app_protocol, bridge_protocol, framing) where
	// framing ∈ {match, mismatch}. mismatch means
	// bridge_protocol ≠ appProtocolToBridgeProtocol(app_protocol) —
	// the operator-forced surgical-rollback signal. This is the
	// only producer of the counter on the vmmd side; the bridge is
	// a stand-alone process that doesn't import pkg/wire (counter
	// lifecycle is owned by vmmd's NewOpsMetrics at boot, commit 9
	// of PR #1051). Falls back to "unknown" / "h1" if the header
	// is absent — the guard is defence-in-depth against a bridge
	// regression that drops the header.
	appProtocol := reqInit.GetAppProtocol()
	bridgeProtocol := httpResp.Header.Get("X-Faas-Bridge-Framing")
	if bridgeProtocol == "" {
		bridgeProtocol = "unknown"
	}
	expected := appProtocolToBridgeProtocol(appProtocol)
	framingLabel := "match"
	if bridgeProtocol != expected {
		framingLabel = "mismatch"
	}
	if s.ops != nil {
		s.ops.BridgeFramingTotal(appProtocol, bridgeProtocol, framingLabel).Inc()
	}
	// 8b. Trailers (ADR-126 / G19). For the H1+chunked bridge path
	// (app_protocol=http1), the guest's H1 `Trailer:` headers arrive
	// in httpResp.Trailer after body EOF. Stdlib populates this map
	// when the guest declares a `Trailer:` response header naming the
	// trailer fields; for H2C terminator paths (app_protocol in
	// {http2, grpc}), this map is empty because trailers ride HTTP/2
	// HEADERS frames instead — the bridge's H2C terminator forwards
	// those frames verbatim and a future bridge-side revision will
	// populate httpResp.Trailer from the trailer HEADERS frame. For
	// now (PR-A, this commit), the H1+chunked path is the only
	// populated branch; legacy callers (no Tra proto field) observe
	// byte-identical behavior because Trailers defaults to nil.
	//
	// We only forward trailers once the body has been fully read,
	// which is true by the time we reach this code path — the
	// bridge's H1+chunked reader (httputil.NewChunkedReader) blocks
	// on httpResp.Body until EOF before unblocking
	// httpResp.Body.Close() above, and httpResp.Trailer is populated
	// at that EOF.
	initTrailers := make([]*vmmdpb.Header, 0)
	for k, vs := range httpResp.Trailer {
		for _, v := range vs {
			initTrailers = append(initTrailers, &vmmdpb.Header{Name: k, Value: v})
		}
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
		Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
			Init: &vmmdpb.ForwardHTTPResponseInit{
				Status:   int32(httpResp.StatusCode),
				Headers:  initHeaders,
				Trailers: initTrailers,
			},
		},
	}); err != nil {
		return status.Errorf(codes.Internal, "send init: %v", err)
	}

	// 9. Stream the response body. Mirrors v1 (forward.go:359-388).
	streamErrCh := make(chan error, 1)
	go func() {
		defer close(streamErrCh)
		buf := make([]byte, 8*1024)
		for {
			n, rerr := httpResp.Body.Read(buf)
			if n > 0 {
				if serr := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{
					Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{
						BodyChunk: append([]byte(nil), buf[:n]...),
					},
				}); serr != nil {
					streamErrCh <- status.Errorf(codes.Internal, "send body chunk: %v", serr)
					return
				}
			}
			if errors.Is(rerr, io.EOF) {
				streamErrCh <- nil
				return
			}
			if rerr != nil {
				streamErrCh <- status.Errorf(codes.Internal, "read body: %v", rerr)
				return
			}
		}
	}()

	// 10. Wait for the body goroutine, then the response reader.
	// Order mirrors v1 (forward.go:391-432).
	bodyErr := <-bodyErrCh
	streamErr := <-streamErrCh

	// The rollback bridge is a long-running HTTP server, so it must be
	// explicitly stopped after the response. Persistent mode releases the
	// lease instead and keeps the process/transport available for reuse.
	var bridgeErr error
	if persistent {
		bridgeLease.release()
	} else {
		bridgeErr = stopStreamBridge(ctx, cmd, stderr)
		bridgeWaited = true
	}

	// Error mapping mirrors v1 priority.
	if bridgeErr != nil {
		s.ops.Observe(op, time.Since(start), bridgeErr)
		s.log.Warn("vmmd: stream bridge exited with error",
			"instance", reqInit.GetInstance(), "netns", netnsName,
			"guest_ip", netns.GuestIP, "guest_port", dialPort,
			"stderr", stderr.String(), "err", bridgeErr.Error())
		return status.Errorf(codes.Unavailable, "stream bridge: %v (stderr: %s)", bridgeErr, stderr.String())
	}
	if bodyErr != nil && !errors.Is(bodyErr, io.EOF) {
		s.ops.Observe(op, time.Since(start), bodyErr)
		return status.Errorf(codes.InvalidArgument, "body stream: %v", bodyErr)
	}
	if streamErr != nil {
		s.ops.Observe(op, time.Since(start), streamErr)
		return streamErr
	}
	return nil
}

// streamBridgePathEnv lets the test suite point at an alternate
// install path without rebuilding. Same shape as rawBridgePathEnv
// (forward.go:492) — non-absolute overrides are rejected.
const streamBridgePathEnv = "FAAS_VMMD_STREAM_BRIDGE_PATH"

// resolveStreamBridgePath returns the absolute path to the
// vmmd-stream-bridge binary. Resolution order:
//
//  1. FAAS_VMMD_STREAM_BRIDGE_PATH env var, if set and absolute
//  2. The hard-coded production path
//
// Anything else (relative override, missing binary) returns a
// stable error rather than falling back to a $PATH lookup.
func resolveStreamBridgePath() (string, error) {
	if override := os.Getenv(streamBridgePathEnv); override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("stream bridge override %s=%q must be an absolute path", streamBridgePathEnv, override)
		}
		return override, nil
	}
	return vmmdStreamBridgePath, nil
}

// streamBridgeSpawn is the package-level spawn hook. Production
// uses streamBridgeSpawnReal which runs `ip netns exec <netns> <bin> …`.
// Tests override to inject a fake bridge that listens on a unix
// socket and serves H2C. The signature mirrors the live spawn's
// observable surface; the fake receives the same argv/env plus
// the resolved sockPath so it can bind the same socket the
// production-spawned bridge would.

var streamBridgeSpawn = streamBridgeSpawnReal

func streamBridgeSpawnReal(ctx context.Context, bridgePath, netnsName, sockPath, guestIP string, guestPort uint32, sessionDeadline string, env []string) (*exec.Cmd, *bytes.Buffer, error) {
	cmd := exec.CommandContext(ctx, "ip", "netns", "exec", netnsName,
		bridgePath, sockPath, guestIP, strconv.FormatUint(uint64(guestPort), 10), sessionDeadline)
	cmd.Env = env
	// Pdeathsig: if vmmd is SIGKILLed (OOM-killer, orchestrator
	// restart, kernel panic reboot path that bypasses our deferred
	// SIGTERM), the bridge child must NOT be orphaned — unlike v1's
	// per-request shell subprocess, v2 is a long-running HTTP server
	// that holds the per-instance unix socket for the bridge's
	// entire lifetime. An orphan would block the next wake with
	// EADDRINUSE on the same sock path; on a control-plane node
	// with many restarts that leaks until tmpfs is exhausted (the
	// same gotcha CLAUDE.md flags for /srv/fc/jail). SIGTERM is the
	// same signal vmmd sends on graceful shutdown, so a graceful
	// path is unchanged; only the SIGKILL escape gets cleaned up.
	//
	// The SysProcAttr setup is split into platform-specific files
	// (forward_pdeathsig_linux.go / forward_pdeathsig_other.go) so
	// the linux-only Pdeathsig field stays out of the darwin/macos
	// build, where the type is missing. The v2 spawn itself is
	// always gated on `ip netns exec`, which only works on Linux —
	// darwin compiles and unit-tests the vmmd package without ever
	// hitting the production spawn path.
	cmd.SysProcAttr = streamBridgeSysProcAttr()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, &stderr, nil
}

// streamBridgeSockPath is the host-side unix socket path for the
// instance's stream bridge. Lives under /var/run/faas/stream/ —
// host-side, mode 0700 (per the tmpfiles.d entry). The bridge
// binary chmods the socket 0600 after bind.
func streamBridgeSockPath(instance string) string {
	return "/var/run/faas/stream/" + instance + ".sock"
}

// streamBridgeRequestSeq makes each ForwardHTTPStreamV2 RPC own a distinct
// socket even when several requests target the same live instance. The
// bridge is process-per-RPC, so sharing the old instance-only path let a
// second request unlink and replace the first request's listener.
var streamBridgeRequestSeq atomic.Uint64

func streamBridgeSockPathForRequest(instance string) string {
	seq := streamBridgeRequestSeq.Add(1)
	return fmt.Sprintf("/var/run/faas/stream/%s-%d-%d.sock", instance, os.Getpid(), seq)
}

// streamBridgeEnv builds the env-var list set on the spawned
// bridge binary. The bridge reads these to assemble the H1
// request line + headers (cmd/vmmd-stream-bridge main.go).
//
// Host is lifted into its own FAAS_BRIDGE_HOST env var rather
// than carried inside FAAS_BRIDGE_HEADERS so the comma in a
// header VALUE never has to be escaped (Accept: text/html,
// application/json and similar headers pass through unchanged).
// The bridge writes FAAS_BRIDGE_HOST before any Host: line in
// FAAS_BRIDGE_HEADERS, and the bridge-side Host: lines in
// FAAS_BRIDGE_HEADERS overwrite the env value if present.
//
// FAAS_BRIDGE_PROTOCOL (ADR-126, issue / G19) carries the
// per-stream framing selection: `h1` for the legacy H1+chunked
// path (app_protocol=http1, default), `h2c` for the new H2C
// terminator (app_protocol in {http2, grpc}). Derived from
// reqInit.AppProtocol via appProtocolToBridgeProtocol which
// validates the closed-set; unknown / empty values fall back
// to `h1` so legacy callers (no AppProtocol field) keep working.
func streamBridgeEnv(reqInit *vmmdpb.ForwardHTTPRequestInit) []string {
	var host string
	headers := make([]string, 0, len(reqInit.GetHeaders()))
	for _, h := range reqInit.GetHeaders() {
		switch {
		case strings.EqualFold(h.GetName(), "Content-Length"),
			strings.EqualFold(h.GetName(), "Transfer-Encoding"):
			continue
		case host == "" && strings.EqualFold(h.GetName(), "Host"):
			// Lift the first Host into its own env var so
			// comma-bearing header values stay intact in
			// FAAS_BRIDGE_HEADERS below. CR/LF stripped — the
			// bridge writes the value verbatim to the guest TCP
			// socket and CR/LF in a Host header value lets a
			// caller smuggle a complete header line into the
			// trusted inner envelope. v1's shellQuote rejected
			// CR/LF at the vmmd-side build step; v2 must match.
			host = sanitizeHeaderValue(h.GetValue())
			continue
		}
		headers = append(headers, fmt.Sprintf("%s=%s", h.GetName(), sanitizeHeaderValue(h.GetValue())))
	}
	env := []string{
		"FAAS_BRIDGE_METHOD=" + sanitizeHeaderValue(reqInit.GetMethod()),
		"FAAS_BRIDGE_URL=" + sanitizeHeaderValue(reqInit.GetRequestUri()),
		"FAAS_BRIDGE_HOST=" + host,
		// Newline separator (not comma) — real headers like
		// `Accept: text/html, application/json` carry commas
		// in their values; CR/LF are illegal in HTTP/1.1
		// field-values so they make a safe wire separator. Any
		// CR/LF that snuck through sanitizeHeaderValue (e.g. via
		// an env-var re-source) would terminate the line and
		// inject a header — the bridge also strips CR/LF as
		// defense-in-depth (cmd/vmmd-stream-bridge main.go).
		"FAAS_BRIDGE_HEADERS=" + strings.Join(headers, "\n"),
		// Per-stream framing selector (ADR-126). Closed-set
		// translation happens in appProtocolToBridgeProtocol
		// below; the bridge just reads the literal string.
		"FAAS_BRIDGE_PROTOCOL=" + streamBridgeProtocol(reqInit),
		// Env passed to cmd.Env is non-additive with the parent's
		// env — only the keys present below are visible. The
		// bridge does no further exec, but PATH is set to a sane
		// default for any future helper.
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	return env
}

// streamBridgeStaticEnv contains only process-wide bridge settings. Request
// metadata is carried in the H2C request itself when persistent mode is on;
// putting it in the environment would make concurrent requests overwrite one
// another. The full streamBridgeEnv remains available for the per-RPC rollback
// path and its compatibility tests.
func streamBridgeStaticEnv(reqInit *vmmdpb.ForwardHTTPRequestInit) []string {
	return []string{
		"FAAS_BRIDGE_PROTOCOL=" + streamBridgeProtocol(reqInit),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
}

// streamBridgeProtocol applies the operator's vmmd-wide framing override
// before the per-app protocol translation. This keeps the documented
// FAAS_BRIDGE_PROTOCOL=h1 surgical rollback effective for both bridge
// lifecycles; unknown override values intentionally fall back to h1.
func streamBridgeProtocol(reqInit *vmmdpb.ForwardHTTPRequestInit) string {
	if override := strings.TrimSpace(os.Getenv("FAAS_BRIDGE_PROTOCOL")); override != "" {
		switch override {
		case "h1", "h2c":
			return override
		default:
			return "h1"
		}
	}
	return appProtocolToBridgeProtocol(reqInit.GetAppProtocol())
}

// appProtocolToBridgeProtocol translates the customer's app_protocol
// (PR #1023 / ADR-124 closed-set `{http1, http2, grpc}`) into the
// per-stream bridge framing selector (ADR-126 closed-set `{h1, h2c}`).
// Unknown / empty values fall back to `h1` so legacy callers (no
// AppProtocol field set) keep working — the H1+chunked path is
// the default and the load-bearing zero-behavior-change baseline.
//
// Closed-set validation belongs here, not in the bridge — the bridge
// is a stand-alone binary with no DB / no app row access; the
// authoritative closed-set check is at apid (pkg/api/limits.go
// AppProtocol validator) + the column-level CHECK constraint
// `apps_app_protocol_chk` (migrations/00382_apps_app_protocol.sql).
// This function is the per-request translate, not a validator.
func appProtocolToBridgeProtocol(appProtocol string) string {
	switch appProtocol {
	case "http2", "grpc":
		return "h2c"
	case "http1", "":
		return "h1"
	default:
		// Unknown value (should never reach here — apid rejects
		// out-of-set values with 400 app_protocol_invalid before
		// the gRPC frame leaves apid). Fall back to h1 so a
		// misconfigured operator gets the legacy path instead of
		// a crash. The unknown value is logged here as a Warn
		// (CLAUDE.md mandates slog JSON; the package convention
		// is slog.Default() for free functions in this file).
		slog.Warn("vmmdgrpc: unknown app_protocol from ForwardHTTPRequestInit; falling back to legacy h1+chunked bridge path",
			"app_protocol", appProtocol,
			"fallback", "h1",
		)
		return "h1"
	}
}

// sanitizeHeaderValue strips CR and LF bytes from a string destined
// for the bridge's env (FAAS_BRIDGE_HOST, FAAS_BRIDGE_HEADERS,
// FAAS_BRIDGE_METHOD, FAAS_BRIDGE_URL). The bridge writes these
// verbatim to the guest TCP socket; CR/LF in a value would let a
// caller smuggle a complete header line into the trusted inner
// envelope. v1's shellQuote() closed this hole at the vmmd-side
// build step (forward.go:1046); v2 must match. The function is
// deliberately idempotent (any combination of CR/LF collapses to
// nothing) and rejects NUL too — NUL in an env var truncates at
// the OS level and the bridge would see only the prefix.
func sanitizeHeaderValue(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' || c == '\n' || c == 0 {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// newStreamBridgeH2CTransport creates an HTTP/2 client transport
// that speaks H2C (cleartext HTTP/2) over a unix socket. Mirrors
// newInternalProxyH2CTransport (pkg/gateway/internal_proxy.go:181-230)
// but is local to this package so the vmmd bridge has no
// dependency on pkg/gateway.
//
// Why x/net/http2 and not stdlib http.Transport: stdlib's
// ForceAttemptHTTP2 is a no-op for plaintext (memory
// h2c-transport-over-unix-socket lines 10-12). x/net/http2 with
// AllowHTTP=true is the canonical Go client for H2C over a
// non-TLS dialer.
func newStreamBridgeH2CTransport(sockPath string) *http2.Transport {
	return &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
		IdleConnTimeout: 5 * time.Minute,
		ReadIdleTimeout: 30 * time.Second,
		PingTimeout:     15 * time.Second,
		// ADR-127 §D2 (Layer 9) — outbound H2C client transport
		// pins. Mirrors the same pins on the other two H2C
		// transports (newGuestH2CTransport in
		// cmd/vmmd-stream-bridge/h2c_terminator.go and
		// newInternalProxyH2CTransport in
		// pkg/gateway/internal_proxy.go). See those sites for
		// per-field rationale.
		MaxReadFrameSize:           1 << 20, // 1 MiB
		MaxHeaderListSize:          1 << 20, // 1 MiB
		StrictMaxConcurrentStreams: true,
	}
}

// bridgeWait drains cmd (the spawned bridge), preserving the
// v1-style error mapping. The bridge is expected to exit cleanly
// once the H2C stream closes; an exit with non-zero status maps
// to Unavailable just like v1.
func bridgeWait(cmd *exec.Cmd, stderr *bytes.Buffer) error {
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("exit %d: %s", exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

// stopStreamBridge terminates and reaps a spawned bridge. It is safe to call
// from both the normal completion path and deferred early-return cleanup.
// Context cancellation kills immediately; otherwise SIGTERM gets a bounded
// grace period before the child is force-killed. Reaping here prevents a
// readiness or request-construction error from leaving a zombie process.
func stopStreamBridge(ctx context.Context, cmd *exec.Cmd, stderr *bytes.Buffer) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- bridgeWait(cmd, stderr) }()
	timer := time.NewTimer(streamBridgeShutdownTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return bridgeStopError(cmd, err)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return bridgeStopError(cmd, <-done)
	case <-timer.C:
		_ = cmd.Process.Kill()
		return bridgeStopError(cmd, <-done)
	}
}

// bridgeStopError treats the signal used by stopStreamBridge as an expected
// shutdown. A bridge normally catches SIGTERM and exits 0, but a child can be
// replaced by a tiny wrapper or be killed during the grace-period fallback;
// the completed HTTP response must not be turned into an RPC error merely
// because the process was terminated during cleanup. Other exit failures are
// preserved for the caller's existing Unavailable mapping.
func bridgeStopError(cmd *exec.Cmd, err error) error {
	if err == nil || cmd == nil || cmd.ProcessState == nil {
		return err
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() &&
		(status.Signal() == syscall.SIGTERM || status.Signal() == syscall.SIGKILL) {
		return nil
	}
	return err
}

// waitForUnixSock polls for the socket file to exist. The bridge
// binary binds synchronously before serving, so this is a quick
// readiness gate. cap=0 means no wait.
func waitForUnixSock(path string, cap time.Duration) error {
	deadline := time.Now().Add(cap)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
