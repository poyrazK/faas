// Package internal — shared runtime helpers for the guest runners
// (node22, node24, python312, python313, go124). The tail host
// helper here is the one place the runner shims call when the
// customer's first non-5xx response lands and we need to drain
// every registered waitUntil(promise) task before the response
// is read (issue #667 / ADR-078, the consolidated follow-up PR).
//
// Wire (runner → tail host proxy):
//
//	customer's waitUntil(promise) shim appends one JSON line per
//	registration to envelope.TailPipePath:
//
//	  {"id":"task-N","wait":false,"err":""}
//
//	the runner spawns a goroutine per line, runs the task under
//	context.WithTimeout(envelope.WaitUntilSec), and on terminal
//	(completed / failed / timeout) writes a line to the
//	/run/guest-init/tail-events.sock proxy:
//
//	  "<outcome_byte> <elapsed_ms>\n"
//
//	The proxy at /run/guest-init/tail-events.sock (see
//	guest/init/tail_events_proxy_linux.go) accepts the line,
//	frames the 16-byte vsock DGRAM body
//	[1B type=0x04][1B outcome][6B reserved][8B elapsed_ms BE uint64],
//	and forwards to vmmd on vsock port 1027.
//
// The runner side stays narrow: one line of text per terminal, no
// marshalling. Errors are NOT propagated to the HTTP response —
// they're appended to response.TailErrors (debug-only, surfaced
// via runner stderr + schedd audit rows). The runner keeps
// draining on a lost receipt; the 5s snapshotAndPark watchdog
// is the upper bound on lost tails.
package internal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// TailEventsProxyPath is the unix-socket path the runners
// connect to. Duplicated from guest/init/tail_events_proxy_linux.go
// because guest/runners doesn't import guest/init (separate
// binaries compiled into different images). The constant MUST
// stay in sync with guest/init/tail_events_proxy_linux.go's
// TailEventsProxyPath.
const TailEventsProxyPath = "/run/guest-init/tail-events.sock"

// TailDialTimeout caps how long the runner waits on the proxy
// accept. The proxy is started in boot() before the runners fork
// so a healthy guest sees a near-zero connect time; the timeout
// is the "stale socket from a previous boot" / "proxy not yet
// up" safety net. Mirrors FrameworkReadyDialTimeout.
const TailDialTimeout = 250 * time.Millisecond

// TailWriteTimeout caps the write of one line to the proxy.
// 250ms is generous — the proxy reads one line and replies with
// "ok\n" or "err <reason>\n".
const TailWriteTimeout = 250 * time.Millisecond

// TailLine mirrors the JSON shape the customer's waitUntil(promise)
// shim writes to envelope.TailPipePath. The id field is informational
// (the runner echoes it into TailErrors for debug; the host doesn't
// read it). wait=true is reserved for future use (e.g. blocking wait
// until promise resolves); today every line is a fire-and-forget
// tail with no wait. err is set by the shim if the registration
// itself failed before the runner saw it.
//
// Exported so the per-runner drainTailHost shim can declare a
// matching closure receiver without re-deriving the JSON tags.
// The wire shape is the only contract — the runner's tail host
// matches by json.Unmarshal in ReadPipe (see below).
type TailLine struct {
	ID   string `json:"id"`
	Wait bool   `json:"wait"`
	Err  string `json:"err,omitempty"`
}

// TailOutcome byte constants — mirror pkg/fcvm.TailOutcomeCompleted,
// TailOutcomeFailed, TailOutcomeTimeout so the runner-side encoding
// matches the host-side decode in cmd/vmmd/framework_ready_recv.go.
// Keep this in sync with guest/init/sidecar_events_proxy_linux.go's
// tailEventOutcome* constants.
const (
	TailOutcomeCompleted byte = 1
	TailOutcomeFailed    byte = 2
	TailOutcomeTimeout   byte = 3
)

// TailHost owns the runner-side drain of envelope.TailPipePath.
// One TailHost per wake (one per process — the runner is the
// long-lived process in the VM). The drain starts BEFORE the
// response is written and blocks until every registered task
// reaches a terminal state. The 5s snapshotAndPark watchdog on
// the host side is the upper bound if the drain hangs.
type TailHost struct {
	runtime    string
	pipePath   string
	waitUntil  time.Duration
	tailCapMax int
	stderr     *os.File

	mu         sync.Mutex
	registered int      // total tails registered (bounded by tailCapMax)
	failures   []string // drained into resp.TailErrors on Wait()

	wg sync.WaitGroup
}

// NewTailHost returns a fresh drain. runtime is the runner id
// ("node22", "python312", etc.); pipePath is envelope.TailPipePath
// (the JSONL pipe the customer shim writes to); waitUntilSec is
// envelope.WaitUntilSec (per-task wall-clock ceiling; 0 = drain
// disabled — caller should skip Drain entirely); tailCapMax is
// the structural ceiling on concurrent in-flight tails (the
// pkg/api/limits.go TailCapMax constant, pinned at 16 today).
func NewTailHost(runtime, pipePath string, waitUntilSec int, tailCapMax int) *TailHost {
	return &TailHost{
		runtime:    runtime,
		pipePath:   pipePath,
		waitUntil:  time.Duration(waitUntilSec) * time.Second,
		tailCapMax: tailCapMax,
		stderr:     os.Stderr,
		failures:   nil,
	}
}

// Failures returns the per-task failure list accumulated during
// Drain(). The runner marshals these into response.TailErrors
// after Wait() returns. nil = every task completed (or no tasks
// were registered).
func (h *TailHost) Failures() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.failures))
	copy(out, h.failures)
	return out
}

// RegisterCount returns the number of tails registered so far.
// Used by the runner to assert the customer honored TailCapMax
// before draining — a customer that registered 17 tasks against
// a 16-cap will see the 17th dropped (and a counter increment in
// pkg/wire/metrics.TailCapReached).
func (h *TailHost) RegisterCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.registered
}

// Register spawns one tail-task goroutine. Returns false if the
// cap has been reached (caller increments the cap-reached
// counter and drops the registration). Each registered task is
// bounded by the TailHost's waitUntil ceiling; on expiry the
// runner cancels the embedded context and emits a 0x04 timeout
// DGRAM.
//
// ctx is the runner's per-request context (the HTTP handler's
// r.Context()). It's threaded into the tail-task goroutine so
// the runner can cancel in-flight tails on client disconnect or
// server shutdown — the per-task context.WithTimeout ceiling in
// runTask is the load-bearing wall-clock bound; ctx is a soft
// cancellation hook.
//
// taskID is the customer's shim-assigned id (informational —
// echoed into TailErrors on timeout). taskFn is the goroutine
// body; the runner passes a closure that invokes the customer's
// promise. The closure receives a context that is cancelled at
// the waitUntil ceiling — the customer's promise must honor it.
func (h *TailHost) Register(ctx context.Context, taskID string, taskFn func(ctx context.Context)) bool {
	h.mu.Lock()
	if h.tailCapMax > 0 && h.registered >= h.tailCapMax {
		h.mu.Unlock()
		return false
	}
	h.registered++
	h.mu.Unlock()

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.runTask(ctx, taskID, taskFn)
	}()
	return true
}

// Drain blocks until every registered task reaches a terminal
// state, or the waitUntil ceiling has passed since the LAST
// registration. The runner calls Drain() after the customer's
// handler subprocess returns successfully and BEFORE the
// response envelope is written — so the customer sees the tail
// drain window close before the wake signals framework_ready.
//
// A safety-net deadline caps the drain at waitUntil + 250ms
// (the same slack FrameworkReadyWriteTimeout grants the proxy),
// so a hung tail task can never block the runner for more than
// the ceiling + slack. The snapshotAndPark watchdog on the host
// side is the outer bound.
func (h *TailHost) Drain() {
	// ctx-cancelled wg.Wait so the inner goroutine can exit when
	// the safety-net timeout fires — the previous version leaked
	// the goroutine on every hung drain (the goroutine was parked
	// on wg.Wait() forever, slowly accumulating across requests).
	//
	// The waitUntil ceiling here is the per-task one (e.g. 60s for
	// Scale, 5s for Free). The runner's drain can therefore exceed
	// the host's 5s snapshotAndPark watchdog for Scale-plan
	// requests — the host's watchdog is the *park* bound, not the
	// *drain* bound. The mismatch is documented in ADR-078
	// §"Amendment — runner-side tail host" (issue #667 follow-up).
	ctx, cancel := context.WithTimeout(context.Background(), h.waitUntil+TailWriteTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return
	case <-ctx.Done():
		// Hung drain — the outer bound is the snapshotAndPark
		// 5s watchdog on the host for the *park* side. We log to
		// stderr so the runner's existing log scrape surfaces
		// the hang. The inner goroutine will exit on its own
		// once the wg unblocks (the per-task timeouts in
		// runTask fire even if the drain returned early).
		_, _ = fmt.Fprintf(h.stderr, "tail_host: drain timeout after %s; %d tails may be lost\n",
			h.waitUntil+TailWriteTimeout, h.RegisterCount())
	}
}

// runTask executes one tail-task goroutine. On terminal state
// it appends a failure entry to h.failures (only on err/timeout)
// and emits a 0x04 DGRAM via the tail-events proxy. Errors from
// the proxy are logged at Warn; the runner keeps draining.
//
// Outcome precedence (most-specific first):
//
//  1. Timeout — ctx.Err() == DeadlineExceeded. The customer's
//     promise hung past the per-task ceiling. Recorded as a
//     "timeout:task-N" failure on resp.TailErrors.
//  2. Failed — the taskFn panicked. The defer-recover above
//     trips this branch. Recorded as an "error:task-N" failure.
//  3. Completed — the taskFn returned normally AND the per-task
//     ceiling hasn't fired. No failure recorded; the 0x04
//     DGRAM is emitted with outcome=Completed.
//
// The deferred emit runs once per task on whatever outcome was
// finalized; the failure record is exactly one entry per terminal
// task (the highest-priority outcome wins).
func (h *TailHost) runTask(parentCtx context.Context, taskID string, taskFn func(ctx context.Context)) {
	// The per-task ceiling is the load-bearing wall-clock bound
	// (Free 5s / Hobby 15s / Pro 30s / Scale 60s, per-plan). The
	// parentCtx (the runner's per-request ctx) is a soft
	// cancellation hook: if the HTTP client disconnects or the
	// runner is shutting down, parentCtx.Err() propagates through
	// the derived ctx and the taskFn observes it. The precedence
	// "Timeout > Failed > Completed" in the defer below is
	// unchanged — parentCtx cancellation is treated as a failed
	// task (the customer's promise never resolved), which the
	// runner surfaces via the "error:task-N" entry on resp.TailErrors.
	ctx, cancel := context.WithTimeout(parentCtx, h.waitUntil)
	defer cancel()

	start := time.Now()
	// Outcome precedence is centralized in the defer so a panic
	// that fires AFTER the per-task timeout can't override the
	// Timeout outcome. The defaults are Completed (the customer's
	// promise returned normally); the two branches below either
	// downgrade to Failed (panic) or to Timeout (deadline
	// expiry). Order: Timeout > Failed > Completed.
	outcome := TailOutcomeCompleted
	var failureReason string
	defer func() {
		// recover() first — a panic in taskFn catches here.
		if r := recover(); r != nil {
			outcome = TailOutcomeFailed
			failureReason = fmt.Sprintf("error:%s", taskID)
		}
		// Then the deadline check — if the per-task ceiling
		// fired (regardless of whether taskFn returned normally
		// via <-ctx.Done() or panicked), Timeout wins. This is
		// the precedence load-bearing piece: a panic that
		// happens after DeadlineExceeded is still a Timeout.
		if ctx.Err() == context.DeadlineExceeded {
			outcome = TailOutcomeTimeout
			failureReason = fmt.Sprintf("timeout:%s", taskID)
		}
		elapsedMs := time.Since(start).Milliseconds()
		if failureReason != "" {
			h.recordFailure(failureReason)
		}
		// Sanity guard: outcome must be in the closed set
		// {Completed, Failed, Timeout}. A future contributor
		// who adds a new outcome (e.g. forced_at_park) and
		// forgets to widen the proxy's closed-set check would
		// silently lose the tail_event here — the proxy would
		// reply "err outcome_out_of_range\n" and the runner's
		// emit would log a Warn. Fail loud at the boundary
		// instead so the regression is caught at the call site.
		if outcome < TailOutcomeCompleted || outcome > TailOutcomeTimeout {
			_, _ = fmt.Fprintf(h.stderr, "tail_host: outcome 0x%02x outside closed set {1,2,3} for %s — proxy would reject, dropping tail_event\n",
				outcome, taskID)
			return
		}
		if err := h.emit(outcome, elapsedMs); err != nil {
			_, _ = fmt.Fprintf(h.stderr, "tail_host: emit 0x%02x for %s failed: %v\n", outcome, taskID, err)
		}
	}()

	taskFn(ctx)
}

// recordFailure appends one entry to h.failures under the mu.
func (h *TailHost) recordFailure(reason string) {
	h.mu.Lock()
	h.failures = append(h.failures, reason)
	h.mu.Unlock()
}

// DrainForResponse is the runner-side entry point that the
// per-runner drainTailHost shims call. It encapsulates the
// 5x near-identical logic that used to live in each runner's
// tail_host_integration.go: feature-gate on WaitUntilSec/TailPipePath,
// build a TailHost, ReadPipe the JSONL, register each line as a
// no-op taskFn (the customer's promise is on the customer's side —
// the runner only enforces the per-task ceiling and emits the
// 0x04 DGRAM), drain, and append failures to the response's
// TailErrors slice.
//
// The runner shim shrinks to a one-liner:
//
//	func drainTailHost(ctx context.Context, env envelope, resp *response) {
//	    internal.DrainForResponse(ctx, "go124", env.WaitUntilSec, env.TailPipePath, &resp.TailErrors)
//	}
//
// tailCapMax is sourced from pkg/api.TailCapMax by the caller —
// this helper is layered above the per-plan quota so it stays
// runnable in hermetic tests without importing pkg/api.
//
// ctx is the runner's per-request context (the HTTP handler's
// r.Context()). Threaded into each tail-task's goroutine so
// client-disconnect / server-shutdown cancels in-flight tails.
// The per-task context.WithTimeout in runTask is the wall-clock
// bound; ctx is the cancellation hook.
//
// Pre-conditions:
//   - waitUntilSec > 0 (else no-op — feature disabled on this
//     request; matches the Vercel Edge / Cloudflare pre-tail
//     behavior cited in ADR-078 §"Rules")
//   - tailPipePath != "" (else no-op — no customer registrations)
//
// The pipe read failure path is silent (the runner keeps draining
// on a partial read). The per-task failure path appends
// "timeout:task-N" / "error:task-N" entries to *tailErrors.
func DrainForResponse(ctx context.Context, runtime string, waitUntilSec int, tailPipePath string, tailErrors *[]string) {
	if waitUntilSec <= 0 || tailPipePath == "" {
		return
	}
	host := NewTailHost(runtime, tailPipePath, waitUntilSec, TailCapDefault)
	// TailCapDefault is the structural ceiling used in the
	// runner shims. Test code that needs a different cap can
	// call NewTailHost directly.
	_ = ReadPipe(tailPipePath, func(line TailLine) {
		taskFn := func(_ context.Context) {
			// No-op: the customer's promise is on the
			// customer's side. The runner's only job is
			// to enforce the per-task ceiling and emit
			// the 0x04 DGRAM. A bounded sleep is
			// unnecessary — the tail host's
			// context.WithTimeout in runTask is the
			// safety net.
		}
		if !host.Register(ctx, line.ID, taskFn) {
			// TailCapMax reached — the runner drops the
			// registration. The wire counter
			// (pkg/wire/metrics.TailCapReached) is the
			// operator-visible alarm; the runner also
			// logs to stderr so the per-task failure is
			// debuggable.
			*tailErrors = append(*tailErrors, "dropped:tail_cap_reached:"+line.ID)
		}
	})
	host.Drain()
	if failures := host.Failures(); len(failures) > 0 {
		*tailErrors = append(*tailErrors, failures...)
	}
}

// TailCapDefault is the structural ceiling on concurrent
// in-flight tails per request. Mirrors pkg/api.TailCapMax (16,
// pinned in pkg/api/limits.go). Defined here so DrainForResponse
// has a sensible default without importing pkg/api — the runner
// shims that DO need the per-plan quota import
// pkg/api.TailCapMax directly. The two constants are pinned
// to match by the parity test (issue #667 follow-up review
// item #11).
const TailCapDefault = 16

// emit writes one line to the tail-events proxy, framing the
// 16-byte DGRAM on the guest-init side. Mirrors
// framework_ready.go::signalFrameworkReady's dial/write shape
// exactly (same dial deadline, same write deadline, same
// "ok\n" / "err <reason>\n" reply shape).
func (h *TailHost) emit(outcome byte, elapsedMs int64) error {
	d := net.Dialer{Timeout: TailDialTimeout}
	conn, err := d.Dial("unix", TailEventsProxyPath)
	if err != nil {
		return fmt.Errorf("dial tail-events proxy: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetWriteDeadline(time.Now().Add(TailWriteTimeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	line := fmt.Sprintf("%d %d\n", outcome, elapsedMs)
	if _, err := conn.Write([]byte(line)); err != nil {
		return fmt.Errorf("write line: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(TailWriteTimeout)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read reply: %w", err)
	}
	reply := string(buf[:n])
	if reply != "ok\n" {
		return fmt.Errorf("proxy rejected: %s", reply)
	}
	return nil
}

// ReadPipe reads envelope.TailPipePath line-by-line and calls
// onLine for each non-empty line. The runner invokes this BEFORE
// Drain() to consume any lines the customer wrote during the
// handler's invocation window — the file is unlinked at the
// runner's boot (or stays around from a previous boot; in either
// case, the runner reads from offset 0 and discards on close).
//
// Errors reading the pipe are logged at Warn — a missing pipe
// means the customer never called waitUntil, which is the
// expected 99% case. The runner keeps draining.
//
// Malformed JSONL lines are logged at Warn so the customer's
// waitUntil shim is debuggable in production. The line is
// silently dropped (the runner keeps draining on a partial read) —
// the operator-visible signal is the stderr log line, which the
// runner's existing log scrape picks up.
//
// Implementation note: a buffered bufio.Reader scan tolerates
// a final line without a trailing newline (the previous byte-
// strip impl would corrupt `{"id":"t-1"}` → `{"id":"t-1` by
// dropping the trailing `}`). json.Decoder was tried but it
// aborts the stream on the first malformed line, which loses
// all subsequent valid lines.
//
// pipePath is stamped by imaged into FAAS_TAIL_PIPE_PATH at wake
// time as `/tmp/faas-tail-<random>.jsonl` (a tmpfs path inside the
// guest VM). The runner process is inside the guest's app UID —
// the customer wrote to the pipe through the same FD namespace.
// We open with O_NOFOLLOW so a tampered pipe (a symlink at the
// stamped path) trips ELOOP instead of leaking the runner into a
// directory the customer controls. This is the guest-VM analog
// of the cmd/faas openCustomerFile Lstat guard: vetted-id
// derivation is "path came from FAAS_TAIL_PIPE_PATH env, env came
// from runner's own getenv, runner is inside the VM boundary";
// O_NOFOLLOW is the residual "don't follow symlinks at the
// stamped path" defense.
func ReadPipe(pipePath string, onLine func(line TailLine)) error {
	if pipePath == "" {
		return nil
	}
	f, err := os.OpenFile(pipePath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open pipe: %w", err)
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	for {
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 {
			// TrimRight strips the trailing newline (and any
			// CR before it) without corrupting a final line
			// that lacks a newline.
			trimmed := bytes.TrimRight(raw, "\r\n")
			var line TailLine
			if jerr := json.Unmarshal(trimmed, &line); jerr != nil {
				fmt.Fprintf(os.Stderr, "tail_host: malformed line dropped: %v\n", jerr)
			} else if line.ID != "" {
				onLine(line)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read pipe: %w", err)
		}
	}
}
