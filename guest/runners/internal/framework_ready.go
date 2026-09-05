// Package internal — shared runtime helpers for the guest runners
// (node22, node24, python312, python313, go124). The framework_ready
// helper here is the one place the runner shims call when the
// customer's first non-5xx response lands (issue #470 / PR
// #470-FU-B).
//
// Wire (runner → proxy):
//
//	proxy connect sends a single line: "<runtime> <warmup_ms>\n"
//
// The proxy at /run/guest-init/framework-ready.sock (see
// guest/init/framework_ready_proxy_linux.go) accepts the line,
// frames the vsock DGRAM body
// ([1B type=0x01][optional 4B BE uint32 warmup_ms][NUL][runtime]),
// and forwards to the host. The runner side stays narrow: one
// line of text, no marshalling.
package internal

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// FrameworkReadyProxyPath is the unix-socket path the runners
// connect to. Duplicated from guest/init/framework_ready_proxy_linux.go
// because guest/runners doesn't import guest/init (separate
// binaries compiled into different images). The constant MUST
// stay in sync with guest/init/framework_ready_proxy_linux.go's
// FrameworkReadyProxyPath.
const FrameworkReadyProxyPath = "/run/guest-init/framework-ready.sock"

// FrameworkReadyDialTimeout caps how long the runner waits on
// the proxy accept. The proxy is started in boot() before the
// runners fork so a healthy guest sees a near-zero connect time;
// the timeout is the "stale socket from a previous boot" /
// "proxy not yet up" safety net.
const FrameworkReadyDialTimeout = 250 * time.Millisecond

// FrameworkReadyWriteTimeout caps the write of one line to the
// proxy. 250ms is generous — the proxy reads one line and
// replies with one of "ok\n" or "err <reason>\n".
const FrameworkReadyWriteTimeout = 250 * time.Millisecond

// RunnerSignal is the runner-side fire-and-forget signal: "first
// non-5xx response" — registers a per-runner sync.Once so a hot
// burst of requests can't fire the signal more than once per
// wake. The runtime field is the runner id ("node22", "python312",
// etc.) the host stamps onto the framework_ready_at histogram's
// `runner` label.
type RunnerSignal struct {
	once      sync.Once
	runtime   string
	startTime time.Time
	stderr    *os.File
}

// NewRunnerSignal returns a fresh fire-and-forget signal helper.
// runtime is the runner id (e.g. "node22"); startTime is the
// moment the runner came up (typically time.Now() in main()).
// The wake_local signal is the dedupe key — the home-grown
// helper skips double-firing on the same wake.
func NewRunnerSignal(runtime string, startTime time.Time) *RunnerSignal {
	return &RunnerSignal{runtime: runtime, startTime: startTime, stderr: os.Stderr}
}

// StartTime returns the moment the runner booted. The signal
// helper's caller computes warmupMs as time.Since(s.StartTime())
// at the moment of the first non-5xx response — the host stamps
// that duration onto the per-instance `framework_ready_at` and
// the vmmd_guest_framework_warmup_seconds histogram.
func (s *RunnerSignal) StartTime() time.Time { return s.startTime }

// SignalReady should be called from the runner's response
// middleware AFTER the handler returns a non-5xx status. Safe to
// call on every request — the underlying sync.Once collapses
// parallel calls into one (the engine's captureWarmSnapshot in
// PR #470-FU-A waits on the SQL-column first-non-NULL row, not
// on the wire signal itself, so an extra signal is a no-op).
//
// warmupMs is the wall-clock duration between runner start and
// this signal — the host stamps it onto the per-instance
// `framework_ready_at` and the
// vmmd_guest_framework_warmup_seconds histogram. 0 = "guest
// didn't measure" (the proxy's body then has no 4-byte warmup_ms
// field).
//
// Errors are NOT returned: the framework-ready signal is
// observation, not source-of-truth. A failed connect (proxy not
// up, stale socket) is logged and the guest's normal request
// handling proceeds. The engine's warm-capture wait times out
// and falls through to init-tier (correctness-preserving).
// The one bounded signal runs in the background: waiting on the proxy here
// would keep net/http from finishing the response, including concurrent
// responses waiting on the same sync.Once.
func (s *RunnerSignal) SignalReady(warmupMs int64) {
	s.once.Do(func() {
		// Capture the dialer before dispatch so restoring a test hook cannot
		// change an already queued signal's destination.
		dial := dialProxy
		go func() {
			if err := signalFrameworkReady(s.runtime, warmupMs, dial); err != nil {
				_, _ = fmt.Fprintf(s.stderr, "framework_ready signal failed: %v\n", err)
			}
		}()
	})
}

// dialProxy opens a unix-socket connection to the guest-init proxy.
// Package-private indirection so unit tests can swap the dialer
// (e.g. point at a net.Pipe in-memory pair) — see WithProxyDialHook.
// Production callers go through net.Dialer's Timeout-bounded default,
// which is the "stale socket from a previous boot" safety net.
var dialProxy = func(network, path string) (net.Conn, error) {
	d := net.Dialer{Timeout: FrameworkReadyDialTimeout}
	return d.Dial(network, path)
}

// SetProxyDialHook swaps the dialer used by signalFrameworkReady.
// It exists for unit tests that cannot bind a real unix socket at
// FrameworkReadyProxyPath (e.g. macOS test boxes where /run is
// read-only). Pass nil to restore the production net.Dialer.
//
// MUST be called from a test setup, not from production code. There
// is no concurrency story here because the runner's signal is
// sync.Once-gated; the worst case under a parallel test is a
// half-swapped hook that the next test reverts.
//
// Returns the previous hook so tests can defer a restore.
var SetProxyDialHook = func(dial func(network, path string) (net.Conn, error)) func(network, path string) (net.Conn, error) {
	prev := dialProxy
	if dial == nil {
		dialProxy = func(network, path string) (net.Conn, error) {
			d := net.Dialer{Timeout: FrameworkReadyDialTimeout}
			return d.Dial(network, path)
		}
	} else {
		dialProxy = dial
	}
	return prev
}

// signalFrameworkReady opens a unix-socket connection to the
// guest-init proxy, writes "<runtime> <warmup_ms>\n", and
// reads the proxy's "ok\n" or "err <reason>\n" reply. The
// proxy is the framing boundary — the runner side stays narrow.
func signalFrameworkReady(runtime string, warmupMs int64, dial func(string, string) (net.Conn, error)) error {
	conn, err := dial("unix", FrameworkReadyProxyPath)
	if err != nil {
		return fmt.Errorf("dial proxy: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetWriteDeadline(time.Now().Add(FrameworkReadyWriteTimeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	line := runtime + " " + strconv.FormatInt(warmupMs, 10) + "\n"
	if _, err := conn.Write([]byte(line)); err != nil {
		return fmt.Errorf("write line: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(FrameworkReadyWriteTimeout)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read reply: %w", err)
	}
	reply := string(buf[:n])
	if reply != "ok\n" {
		return errors.New("proxy rejected: " + reply)
	}
	return nil
}
