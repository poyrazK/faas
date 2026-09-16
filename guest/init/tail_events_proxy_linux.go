//go:build linux

// Unix-socket proxy the guest's runners (node22, python312, go124,
// etc.) use to ship per-task waitUntil terminal events to the host
// (issue #667 / ADR-078, the consolidated follow-up PR).
//
// Why a proxy?
//
// The runner process inside the guest doesn't speak AF_VSOCK
// directly — it runs in the customer's app namespace and only
// has access to the localhost unix domain socket at
// /run/guest-init/tail-events.sock. The proxy bridges:
// runner → AF_UNIX stream → guest-init → AF_VSOCK STREAM → host.
//
// Why a separate socket from framework_ready.sock?
//
// Two reasons. (a) It keeps the runner-side shim narrow: the
// tail host doesn't need to multiplex message types on its side
// — the proxy receives one line per terminal event, frames it,
// and ships it. (b) It cuts the per-event hot path: the runner
// opens a fresh stream connection per tail-event (it's behind a
// sync.WaitGroup drain, so the connection rate is bounded by
// tailCapMax = 16 in pkg/api/limits.go), and the proxy is a
// stateless line→STREAM pump with no parser state.
//
// The proxy is started in boot() BEFORE the supervisor starts the
// runners, so the first tail terminal can't race the proxy coming
// up; see the wiring in main_linux.go.
//
// Wire (runner → proxy → vsock STREAM):
//
//	proxy connect sends a single line: "<outcome_byte> <elapsed_ms>\n"
//
//	where outcome_byte is one of:
//	  1 = completed (the customer's promise resolved)
//	  2 = failed    (the customer's promise rejected, or the
//	                 runner's wait observed ctx.Err() and the
//	                 task panicked)
//	  3 = timeout   (the runner's per-task context.WithTimeout
//	                 fired before the promise resolved)
//
//	elapsed_ms is the wall-clock duration from waitUntil
//	registration to terminal, in milliseconds, encoded as a
//	decimal integer (NOT hex). The host's
//	cmd/vmmd/framework_ready_recv.go::parseFWReadyTailWire reads
//	the same shape — see the comment on that function for the
//	"runner is the canonical clock" rationale.
//
//	proxy replies with one of:
//	  "ok\n"   ← receipt accepted (forwarded to vsock)
//
//	proxy forwards to vsock STREAM (port 1027, msg_type 4) with
//	the 16-byte body:
//	  [1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64]
//
// The STREAM body is exactly the same shape the guest-init's
// own sidecar_events_proxy_linux.go::sendTail pipeline emits —
// the host's recv loop already demuxes on the type byte. This
// proxy is the runner-side alternate path (runner → AF_UNIX over
// /run/guest-init → guest-init → AF_VSOCK over port 1027) that
// works around the runner's inability to bind AF_VSOCK directly.
// The two paths produce identical bytes on the wire.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// TailEventsProxyPath is the localhost unix-socket path the
// runners connect to. Duplicated in guest/runners/internal/
// tail_host.go::TailEventsProxyPath because guest/runners
// doesn't import guest/init (separate binaries compiled into
// different images). The constant MUST stay in sync — a drift
// produces a silent "emit silently dropped" failure mode (the
// runner's connect gets ECONNREFUSED, the tail host logs a Warn,
// the runner keeps draining).
const TailEventsProxyPath = "/run/guest-init/tail-events.sock"

// TailEventsProxyDir is the directory the proxy socket lives
// in. Reuses FrameworkReadyProxyDir; the proxy is created
// idempotently via os.MkdirAll. The runtime images ship an
// empty /run/guest-init tmpfs; the proxy is responsible for
// materializing it.
const TailEventsProxyDir = "/run/guest-init"

// TailEventsProxyMode is the file mode of the proxy socket
// (0660 — owner+group read/write). Identical to
// FrameworkReadyProxyMode; the runner processes inherit the
// guest-init uid and the group is created by imaged at image
// build time.
const TailEventsProxyMode = 0o660

// The tail-event wire shape reuses the constants already
// defined in sidecar_events_proxy_linux.go:
//
//   - tailEventMaxDatagram = 16 (the bounded encode size)
//   - tailEventOutcomeCompleted / Failed / Timeout (the
//     closed-set of 0x04 outcome bytes, mirroring
//     pkg/fcvm.TailOutcome* and
//     guest/runners/internal/tail_host.go::TailOutcome*)
//
// All three definitions live in one place (sidecar_events_proxy_linux.go)
// so a drift produces a compile-time error in guest/init rather
// than a silent "wrong byte on the wire" in production.

// startTailEventsProxy brings up the unix-socket listener and forwards each
// runner event over a fresh vsock STREAM. Returns are tolerated at the boot()
// caller — the platform contract is "no signal" (a missing
// receipt is bounded by the 5s snapshotAndPark watchdog on
// schedd).
//
// Lifecycle: caller (boot) invokes this once after
// startFrameworkReadyProxy, before supervise() starts the
// runners. The accept loop runs forever on a single goroutine;
// per-connection work is in its own goroutine so a slow runner
// can't pin the accept queue. After a process restart the
// socket is unlinked first (stale dirent from a crash); netcat
// to /run/guest-init/tail-events.sock is the operator debug
// entry-point.
func startTailEventsProxy(log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}

	// 1. Build /run/guest-init. MkdirAll is idempotent on an
	// existing dir; framework_ready proxy already created it,
	// but we don't depend on the order — both proxies need the
	// same dir to exist.
	if err := os.MkdirAll(TailEventsProxyDir, 0o755); err != nil {
		return fmt.Errorf("tail events proxy mkdir: %w", err)
	}
	// Stale socket from a previous boot of the same guest.
	// Unlink unconditionally; the open below will fail loud if
	// a real socket is bound at the path.
	if err := os.Remove(TailEventsProxyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("tail events proxy unlink %s: %w", TailEventsProxyPath, err)
	}

	// 2. AF_UNIX stream listener on the proxy socket.
	addr, err := net.ResolveUnixAddr("unix", TailEventsProxyPath)
	if err != nil {
		return fmt.Errorf("tail events proxy resolve: %w", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("tail events proxy listen: %w", err)
	}
	if err := os.Chmod(TailEventsProxyPath, TailEventsProxyMode); err != nil {
		_ = ln.Close()
		return fmt.Errorf("tail events proxy chmod: %w", err)
	}

	// 3. Accept loop. Each connection is one tail terminal
	// event from the runner's tail host — the runner's
	// emit() opens a fresh connection per terminal (no
	// connection re-use: the per-connection work is bounded by
	// the elapsed_ms read, and the tail host's Drain() awaits
	// the "ok\n" reply before closing).
	go func() {
		defer func() { _ = ln.Close() }()
		for {
			conn, err := ln.AcceptUnix()
			if err != nil {
				log.Debug("tail events proxy accept ended", "err", err)
				return
			}
			go handleTailEventsConn(conn, log)
		}
	}()

	log.Info("tail_events proxy started",
		"socket", TailEventsProxyPath,
		"vsock_port", VsockFrameworkReadyPort)
	return nil
}

// parseTailEventLine parses one "<outcome_byte> <elapsed_ms>"
// line from the runner side of the proxy. Extracted so the
// bounded conversion is unit-testable without spinning up a
// unix socket + vsock fd.
//
// Returns outcome (one of {1,2,3}), elapsedMs (0..max uint64),
// and err for any malformed input. outcome is validated against
// the closed set {completed, failed, timeout} so the encode
// side rejects garbage early — the host's histogram would
// otherwise silently bucket unknown bytes as "failed" (the
// parseFWReadyTailWire fallback).
func parseTailEventLine(line string) (byte, uint64, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("format: '<outcome_byte> <elapsed_ms>'")
	}
	outcome, perr := strconv.ParseUint(fields[0], 10, 8)
	if perr != nil {
		return 0, 0, fmt.Errorf("parse outcome: %w", perr)
	}
	switch byte(outcome) {
	case tailEventOutcomeCompleted, tailEventOutcomeFailed, tailEventOutcomeTimeout:
	default:
		return 0, 0, fmt.Errorf("outcome %d outside closed set {1,2,3}", outcome)
	}
	// 64-bit unsigned — the wire reserves 8B BE uint64 so
	// anything outside [0, math.MaxUint64] is unrepresentable.
	// ParseUint with bitSize=64 does the upper-bound check
	// AND rejects negative inputs in the same call — both
	// directions go/integer-overflow flags.
	elapsedMs, perr := strconv.ParseUint(fields[1], 10, 64)
	if perr != nil {
		return 0, 0, fmt.Errorf("parse elapsed_ms: %w", perr)
	}
	return byte(outcome), elapsedMs, nil
}

// handleTailEventsConn reads one line from the runner
// ("<outcome_byte> <elapsed_ms>\n"), frames it for vsock STREAM,
// and sends to VMADDR_CID_HOST:VsockFrameworkReadyPort. Closes
// the connection on return regardless of error. Writes back
// "ok\n" on success or "err <reason>\n" on failure so the
// runner shim can log without re-implementing the dgram
// framing.
//
// Mirrors handleFrameworkReadyConn's shape exactly (same
// single-line read, same "ok\n" / "err <reason>\n" reply
// format, same unix.SendmsgN-vsock send). The two proxies
// differ only in the wire layout they frame — this one
// encodes the fixed-size 16-byte 0x04 body.
func handleTailEventsConn(conn *net.UnixConn, log *slog.Logger) {
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		_, _ = conn.Write([]byte("err read_line\n"))
		log.Warn("tail events proxy read line", "err", err)
		return
	}
	outcome, elapsedMs, perr := parseTailEventLine(line)
	if perr != nil {
		// Mirror the historical error strings on the wire so
		// the runner-side log shim doesn't change shape.
		msg := perr.Error()
		switch {
		case strings.HasPrefix(msg, "format:"):
			_, _ = conn.Write([]byte("err format: '<outcome_byte> <elapsed_ms>'\n"))
		case strings.HasPrefix(msg, "parse outcome:"):
			_, _ = conn.Write([]byte("err parse outcome\n"))
		case strings.HasPrefix(msg, "parse elapsed_ms:"):
			_, _ = conn.Write([]byte("err parse elapsed_ms\n"))
		case strings.HasPrefix(msg, "outcome "):
			_, _ = conn.Write([]byte("err outcome_out_of_range\n"))
		default:
			_, _ = conn.Write([]byte("err parse\n"))
		}
		return
	}

	// Frame: [1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64].
	// The host's parseFWReadyTailWire parses the type byte first,
	// then the outcome, then skips the 6 reserved bytes, then reads
	// the 8-byte elapsed_ms. The buf allocation is bounded by the
	// compile-time tailEventMaxDatagram constant.
	buf := make([]byte, tailEventMaxDatagram)
	buf[0] = VsockTailEventType
	buf[1] = outcome
	// buf[2:8] reserved, already zero from make().
	binary.BigEndian.PutUint64(buf[8:16], elapsedMs)

	if err := sendGuestEventFrame(buf, time.Second); err != nil {
		_, _ = conn.Write([]byte("err send_vsock\n"))
		log.Warn("tail events proxy send vsock", "err", err, "outcome", outcome)
		return
	}
	_, _ = conn.Write([]byte("ok\n"))
	log.Debug("tail_event forwarded",
		"outcome", outcome, "elapsed_ms", elapsedMs)
}
