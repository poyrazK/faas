// Command vmmd-raw-bridge (issue #676 / ADR-080) is the raw-bytes
// bridge that owns the guest's netns TCP socket for Upgrade
// (WebSocket / h2c / MQTT-over-WS / long-poll) traffic.
//
// vmmd's ForwardHTTPStream handler (pkg/vmmdgrpc/forward.go) cannot
// carry Upgrade traffic because the shell-script bridge
// (buildStreamingBridgeScript, forward.go:454-511) hard-codes
// Transfer-Encoding: chunked on the request line and rewrites the
// Host header to the inner netns IP — both destroy the bytes an
// Upgrade handshake needs. This binary replaces the bash `/dev/tcp`
// pattern with explicit Go netns entry + net.Dial + io.Copy bidi.
//
// Args (positional, no flags):
//
//	argv[1] = guest IP (e.g. 10.0.0.2; the same every instance
//	          because ADR-009 gives every guest identical inner
//	          network world)
//	argv[2] = guest TCP port (defaults to netns.AppPort on the
//	          caller side; this binary does not default)
//
// Wire output (stdout), framed exactly as the existing shell
// bridge:
//
//	<status line>\n<header lines>\n\n<raw body bytes>
//
// stderr carries human-readable error messages on bridge failure;
// the caller (vmmd) surfaces the captured stderr in the gRPC
// error so the gateway can log it.
//
// Failure modes (exit code != 0):
//
//	2 = usage error (bad argv)
//	3 = dial refused (guest not listening, or guest at-capacity)
//	4 = read/write error on the guest socket
//
// The binary is intentionally tiny: a few hundred lines. It runs
// inside the per-instance netns via `nsenter --target <firecracker-pid>
// --net -- vmmd-raw-bridge <ip> <port>` and inherits stdin/stdout from the
// parent process, so the gRPC handler's pipe plumbing is the
// natural carry.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// dialTimeout caps how long the initial TCP dial against the
// guest can take. Past 30 s the gateway has already-abandoned the
// wake (the gateway's DialTimeout is 10 s; 30 s here is for the
// cold-boot case where the guest is still bootstrapping).
const dialTimeout = 30 * time.Second

// readHeaderTimeout caps the time the bridge waits for the
// guest's response head (status line + headers + blank line).
// Long-poll and WS upgrade responses land within milliseconds
// of the dial succeeding; anything past 30 s is a wedged guest.
const readHeaderTimeout = 30 * time.Second

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: vmmd-raw-bridge <guest-ip> <port>\n")
		os.Exit(2)
	}
	ip := os.Args[1]
	portStr := os.Args[2]
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid port %q: %v\n", portStr, err)
		os.Exit(2)
	}

	// 1. Dial the guest. The bridge is already inside the per-instance
	//    netns (via `nsenter --target <firecracker-pid> --net -- ...`), so
	//    the 10.0.0.2/30 inner network is directly reachable.
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.Dial("tcp", net.JoinHostPort(ip, strconv.FormatUint(port, 10)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial guest %s:%d: %v\n", ip, port, err)
		os.Exit(3)
	}
	defer func() { _ = conn.Close() }()

	// 2. Pipe stdin → conn (the request body, fed by the vmmd
	//    gRPC handler). The body goroutine in forward.go's
	//    ForwardRawStream closes stdinW on EOF; that EOF flows
	//    through to the conn as a half-close, which is the
	//    canonical HTTP/1.1 request-body terminator.
	//
	//    Race the conn write against the conn close: if the guest
	//    closes the conn before we finish writing, the next
	//    conn.Write returns an error and the goroutine exits
	//    cleanly.
	writeErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		writeErrCh <- err
	}()

	// 3. Read the response head. The guest sends
	//    "<status>\n<header lines>\n\n" then raw body bytes.
	//    We split on the first \n\n and write the head to
	//    stdout first, then stream the body.
	//
	//    Cap the head-read at readHeaderTimeout; an unbounded read
	//    on a wedged guest would let the bridge process hang
	//    indefinitely and exhaust the vmmd pipe.
	if err := conn.SetReadDeadline(time.Now().Add(readHeaderTimeout)); err != nil {
		fmt.Fprintf(os.Stderr, "set read deadline: %v\n", err)
		os.Exit(4)
	}
	reader := bufio.NewReader(conn)
	head, body, err := readResponseHead(reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read head: %v\n", err)
		os.Exit(4)
	}
	if _, err := os.Stdout.Write(head); err != nil {
		fmt.Fprintf(os.Stderr, "write head: %v\n", err)
		os.Exit(4)
	}
	// The body bytes that arrived inside the head read are
	// already in `body`; write them to stdout before streaming
	// further reads. The platform already sent the response
	// init frame to the gRPC client by the time we get here
	// (forward.go's readUntilBlankLine + parseBridgeOutput
	// issued it before this loop runs).
	if len(body) > 0 {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "write body prefix: %v\n", err)
			os.Exit(4)
		}
	}

	// 4. Stream the response body. Drop the read deadline so the
	//    long-poll / WS session can stay idle without the bridge
	//    timing out. The session deadline is enforced on the
	//    gateway side via RawStreamSessionDeadline (24h).
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		fmt.Fprintf(os.Stderr, "clear read deadline: %v\n", err)
		os.Exit(4)
	}
	if _, err := io.Copy(os.Stdout, reader); err != nil && !errors.Is(err, io.EOF) {
		// io.EOF is the clean-close signal (guest closed the
		// connection). Anything else is a real read error.
		fmt.Fprintf(os.Stderr, "copy body: %v\n", err)
		// Drain the write goroutine so we don't leak.
		<-writeErrCh
		os.Exit(4)
	}

	// The guest closed the connection. Drain the write goroutine
	// (which has been copying stdin to conn and now sees
	// io.ErrClosedPipe / "use of closed network connection").
	// Classify the result: a clean close is the normal termination
	// path; anything else means the guest hung up mid-request-body
	// and we lost bytes — surface as exit 4 so vmmd reports an
	// Internal to the gateway instead of a silent OK on a
	// truncated body. (issue #676 review fix; the prior
	// discard-the-error path returned 0 even when half the
	// request body never reached the guest.)
	if err := <-writeErrCh; err != nil && !isCleanConnClose(err) {
		fmt.Fprintf(os.Stderr, "write body: %v\n", err)
		os.Exit(4)
	}
}

// readResponseHead reads from r until it sees "\n\n" (the
// HTTP/1.1 response head terminator after CR stripping), returning
// the head bytes (up to but not including the terminator) and any
// body bytes that arrived in the same read.
//
// The split is byte-exact on LF-only: the bridge strips the CR
// from each line of the head on the way in so the terminator
// shape matches the existing shell bridge's `read -r` + LF
// output. The Go stdlib HTTP server emits CRLF on the wire; the
// shell bridge handles that via `read -r` which keeps the CR and
// the printf '\n' that follows it (`\r\n`). The new bridge does
// the same CR-strip on emit, so the caller (parseBridgeOutput)
// sees LF-only heads and the wire contract is identical.
//
// r is a *bufio.Reader so the body bytes that arrive after the
// head remain buffered for the caller to consume via io.Copy on
// the same reader. parseBridgeOutput on the caller side consumes
// the head shape unchanged.
func readResponseHead(r *bufio.Reader) (head, body []byte, err error) {
	var acc []byte
	for {
		frag, ferr := r.ReadBytes('\n')
		if len(frag) > 0 {
			// Strip the trailing CR (if present) while keeping
			// the LF so the head shape matches the existing
			// shell bridge's `printf '\n'` output. The body
			// bytes that arrive after the head are preserved
			// untouched (bodies are not CRLF-stripped; the
			// gateway sees the raw bytes the guest sent).
			if frag[len(frag)-1] == '\n' {
				if len(frag) >= 2 && frag[len(frag)-2] == '\r' {
					// "...\r\n" → "...\n"
					frag = append([]byte(nil), frag[:len(frag)-2]...)
					frag = append(frag, '\n')
				}
				acc = append(acc, frag...)
				if idx := indexDoubleLF(acc); idx >= 0 {
					headEnd := idx
					return acc[:headEnd], acc[headEnd+2:], nil
				}
			} else {
				// No trailing \n — partial line at EOF.
				acc = append(acc, frag...)
			}
		}
		if errors.Is(ferr, io.EOF) {
			// No terminator found. The guest sent a partial
			// response; surface as a head-read error so the
			// caller can log the bridge state.
			return acc, nil, fmt.Errorf("unexpected EOF before head terminator (got %d bytes)", len(acc))
		}
		if ferr != nil {
			return acc, nil, ferr
		}
	}
}

// indexDoubleLF returns the index of the first "\n\n" in b,
// or -1 if not present. bytes.Index uses Boyer–Moore under the
// hood for short needles (the runtime picks SIMD when available)
// so this is faster than a hand-rolled scan, particularly on
// large head buffers where the loop would otherwise touch every
// byte.
func indexDoubleLF(b []byte) int {
	return bytes.Index(b, []byte("\n\n"))
}

// isCleanConnClose reports whether err is the canonical
// "connection closed by peer / closed locally" signal — the
// expected terminal state when the guest finishes its response
// and the bridge's write goroutine drains its remaining stdin
// bytes. The bridge exit-0 path must classify these as success;
// any other error (EPIPE on the kernel side, write timeout,
// context cancellation surfacing through the conn) means the
// guest hung up mid-request-body and bytes were lost — the
// bridge must surface exit 4 so vmmd's
// rawBridgeFinish maps to Internal rather than the gateway
// seeing OK on a truncated body.
//
// Recognised clean-close errors:
//   - io.EOF (no bytes written / clean half-close)
//   - io.ErrClosedPipe (we closed the conn before the goroutine
//     observed the close — happens during the success path's
//     defer conn.Close)
//   - "use of closed network connection" (the conn was closed
//     before io.Copy returned; same root cause as ErrClosedPipe
//     but a different string from net pkg internals)
//   - "broken pipe" (EPIPE on Linux — peer closed its read side
//     while we were still writing; rare but legal during
//     long-poll body writes)
func isCleanConnClose(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "broken pipe")
}
