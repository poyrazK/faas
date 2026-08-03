// Whitebox tests for cmd/gatewayd/app_logs.go — the AppLogsHandler
// receive pump. The PR-2 wiring pushes the customer-facing log
// stream from cmd/apid to cmd/gatewayd (issue #254 / Move 4), so
// these tests are the corresponding whitebox surface, ported from
// cmd/apid/schedd_client_test.go (which is now deleted).
//
// The handler's Auth chain is exercised in pkg/auth/middleware_test.go;
// here we drive serveAppLogs directly with a controllable LogStream
// so the receive-pump / heartbeat / backstop / clean-EOF / error /
// NotFound paths are pinned in isolation. This matches the
// cmd/apid test pattern that proved the receive-pump itself 1:1
// (the receive-pump logic is unchanged between the two daemons; it
// moved packages).
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// controllableScheddClient is the whitebox seam for tests that
// drive serveAppLogs's receive pump against a programmable
// upstream. The single RPC returned by StreamAppLogs is a
// controllableScheddLogStream whose Recv blocks until the test
// sends a frame, an error, or closes the stream.
type controllableScheddClient struct {
	stream scheddgrpc.LogStream
}

func (c *controllableScheddClient) StreamAppLogs(_ context.Context, _ string, _ int64, _ time.Time, _ string) (scheddgrpc.LogStream, error) {
	return c.stream, nil
}

// fixedScheddResolver adapts a single controllableScheddClient to
// the logStreamerResolver surface the AppLogsHandler consumes
// (Phase 2 / Gate A). Production wires a per-app router; tests
// share one stream across every appID lookup.
type fixedScheddResolver struct {
	c *controllableScheddClient
}

func (f *fixedScheddResolver) ScheddForApp(_ context.Context, _ string) (logStreamer, error) {
	return f.c, nil
}

// controllableScheddLogStream is the per-test stream. Frames and
// errors are queued on buffered channels so the test can drive
// timing from the outside.
//
// The select inside Recv makes this safe under ctx cancel: a
// production-style ctx-cancel unblocks the receive.
type controllableScheddLogStream struct {
	frames  chan scheddgrpc.LogFrame
	errCh   chan error
	closed  bool
	closeMu sync.Mutex
}

func newControllableScheddStream() *controllableScheddLogStream {
	return &controllableScheddLogStream{
		frames: make(chan scheddgrpc.LogFrame, 16),
		errCh:  make(chan error, 1),
	}
}

func (s *controllableScheddLogStream) pushFrame(f scheddgrpc.LogFrame) {
	s.frames <- f
}

func (s *controllableScheddLogStream) finish(err error) {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return
	}
	s.closed = true
	s.errCh <- err
	s.closeMu.Unlock()
}

func (s *controllableScheddLogStream) closeStream() {
	s.finish(io.EOF)
}

func (s *controllableScheddLogStream) Recv() (scheddgrpc.LogFrame, error) {
	// Prefer a queued frame; only fall through to errCh if no
	// frame is ready. This mirrors real gRPC behaviour: data
	// frames dominate; error/EOF closes the stream.
	select {
	case f, ok := <-s.frames:
		if !ok {
			return scheddgrpc.LogFrame{}, io.EOF
		}
		return f, nil
	default:
	}
	select {
	case f := <-s.frames:
		return f, nil
	case err := <-s.errCh:
		return scheddgrpc.LogFrame{}, err
	}
}

// runServeAppLogs drives serveAppLogs against a fresh
// controllableScheddClient and returns the recorder body it wrote.
// appID is opaque; the function does not touch the store, so the
// "no running instance" 404 from LoadApp does not fire here. This
// isolates the receive-pump paths from the rest of the handler.
func runServeAppLogs(t *testing.T, h *AppLogsHandler, stream *controllableScheddLogStream, heartbeat, backstop time.Duration) string {
	t.Helper()
	h.Heartbeat = heartbeat
	h.Backstop = backstop
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		h.serveAppLogs(context.Background(), rec, rec, "app-1", 0, time.Time{}, "")
		close(done)
	}()
	<-done
	return rec.body.String()
}

// --- helpers ------------------------------------------------------------

// flusherRecorder satisfies both http.ResponseWriter and
// http.Flusher so serveAppLogs's `if flusher != nil` checks (and
// the RenderAppLogEvent path) exercise the same code they would in
// production. Body is captured into a sync.Mutex-guarded buffer to
// stay -race clean (memory "e2etest safeBuffer").
type flusherRecorder struct {
	body *safeBuffer
	h    http.Header
}

func newFlusherRecorder() *flusherRecorder {
	return &flusherRecorder{body: &safeBuffer{}, h: http.Header{}}
}

func (r *flusherRecorder) Header() http.Header         { return r.h }
func (r *flusherRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *flusherRecorder) WriteHeader(int)             {}
func (r *flusherRecorder) Flush()                      {}

// --- the receive-pump cases --------------------------------------------

// TestServeAppLogs_BackstopFiresOnIdleStream pins the bug from
// pre-fix: the old `select { default: }` loop only checked timers
// between frames, so the backstop never fired on a quiet stream.
// With the fix, serveAppLogs returns within the backstop interval
// and emits `event: end {reason: timeout}`.
func TestServeAppLogs_BackstopFiresOnIdleStream(t *testing.T) {
	h := &AppLogsHandler{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	stream := newControllableScheddStream()
	body := runServeAppLogs(t, h, stream,
		10*time.Second,      // heartbeat: long; we want the backstop to win
		50*time.Millisecond, // backstop: short enough to bound the test
	)
	if !strings.Contains(body, `event: end`) {
		t.Fatalf("no terminal end frame in body: %q", body)
	}
	if !strings.Contains(body, `"reason":"timeout"`) {
		t.Errorf("missing timeout reason: %q", body)
	}
}

// TestServeAppLogs_CtxCancelReturnsWithoutTerminalFrame pins the
// goroutine-leak guarantee: cancelling the request context exits the
// handler without emitting a terminal frame (the client is gone;
// writing `event: end` to a torn-down ResponseWriter is a no-op +
// a goroutine-wake-up trap).
func TestServeAppLogs_CtxCancelReturnsWithoutTerminalFrame(t *testing.T) {
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second,
		Backstop:  10 * time.Second,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	stream := newControllableScheddStream()

	ctx, cancel := context.WithCancel(context.Background())
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		h.serveAppLogs(ctx, rec, rec, "app-1", 0, time.Time{}, "")
		close(done)
	}()
	// Let the receive goroutine settle into Recv().
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveAppLogs did not return after ctx cancel")
	}
	body := rec.body.String()
	if strings.Contains(body, "event: end") {
		t.Errorf("ctx cancel must not emit terminal frame; body=%q", body)
	}
}

// TestServeAppLogs_HeartbeatOnIdleStream pins the SSE liveness
// contract: htmx-ext-sse treats silence as a dead connection, so the
// heartbeat must fire on a quiet stream within the configured window.
func TestServeAppLogs_HeartbeatOnIdleStream(t *testing.T) {
	h := &AppLogsHandler{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	stream := newControllableScheddStream()

	// Emit exactly one frame, then sit idle. The handler should
	// emit the frame, then heartbeats until the backstop closes
	// the stream. Expect at least one `:` heartbeat before the
	// terminal `event: end`.
	h.Heartbeat = 30 * time.Millisecond
	h.Backstop = 10 * time.Second // long enough that heartbeats dominate
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	done := make(chan struct{})
	go func() {
		// After the pump is reading, push one frame then sit idle.
		time.Sleep(10 * time.Millisecond)
		stream.pushFrame(scheddgrpc.LogFrame{
			InstanceID: "i-1", Seq: 1, Stream: "stdout",
			Line:      "hello\n",
			WrittenAt: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
		})
		<-done
	}()
	body := runServeAppLogs(t, h, stream,
		30*time.Millisecond,
		200*time.Millisecond, // bound the test
	)
	close(done)
	if !strings.Contains(body, "event: log") {
		t.Errorf("missing log frame: %q", body)
	}
	heartbeats := strings.Count(body, ":\n\n")
	if heartbeats < 1 {
		t.Errorf("expected at least one heartbeat, got %d in body=%q", heartbeats, body)
	}
	if !strings.Contains(body, `"reason":"timeout"`) {
		t.Errorf("backstop should also fire; body=%q", body)
	}
}

// TestServeAppLogs_FramesRenderInOrder pins the contract that frames
// reach the client in the order Recv returned them. The bounded
// channel-of-1 introduces drop semantics; this test ensures the
// drop path doesn't reshuffle or skip.
//
// Producer-side timing matters: pushing both frames BEFORE the
// receive goroutine starts races the channel-of-1 drop — the Recv
// loop can pick up frame 1, send to recvCh, then loop back and pick
// up frame 2 before the main loop has consumed frame 1, hitting the
// "channel full" branch. The earlier test pushed 2 frames in the
// goroutine before serveAppLogs started; CI hit the drop ~1/50 times
// on the 2-vCPU ubuntu-latest runner. The fix is to start the
// handler first, let the receive goroutine settle into Recv, then
// push a frame and wait for it to land in the body before pushing
// the next one. The settle + wait-for-body pattern is what the
// heartbeat test (and the rest of the receive-pump suite) already
// uses.
func TestServeAppLogs_FramesRenderInOrder(t *testing.T) {
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second,
		Backstop:  10 * time.Second,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	stream := newControllableScheddStream()
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		h.serveAppLogs(context.Background(), rec, rec, "app-1", 0, time.Time{}, "")
		close(done)
	}()
	// Let the receive goroutine settle into Recv().
	time.Sleep(20 * time.Millisecond)

	stream.pushFrame(scheddgrpc.LogFrame{InstanceID: "i-1", Seq: 1, Stream: "stdout", Line: "first\n", WrittenAt: time.Now()})
	// Wait for frame 1 to land in the body so the main loop has
	// consumed it from recvCh before we push frame 2. The 2-vCPU
	// CI runner is occasionally slow enough to drop on cap-1.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.body.String(), `"seq":1,`) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	stream.pushFrame(scheddgrpc.LogFrame{InstanceID: "i-1", Seq: 2, Stream: "stdout", Line: "second\n", WrittenAt: time.Now()})

	// Close the stream so the handler returns; otherwise the
	// backstop would block the test for 10s.
	stream.closeStream()
	<-done
	body := rec.body.String()
	i1 := strings.Index(body, `"seq":1,`)
	i2 := strings.Index(body, `"seq":2,`)
	if i1 < 0 || i2 < 0 {
		t.Fatalf("missing frames: body=%q", body)
	}
	if i1 >= i2 {
		t.Errorf("frames out of order: first@%d second@%d", i1, i2)
	}
}

// TestServeAppLogs_CleanEndEmitsEmptyEndEvent pins the io.EOF path:
// schedd closes the stream cleanly (recv goroutine sends nil frame,
// receive goroutine exits and closes recvCh; handler emits empty
// event: end).
func TestServeAppLogs_CleanEndEmitsEmptyEndEvent(t *testing.T) {
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second,
		Backstop:  10 * time.Second,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	stream := newControllableScheddStream()
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		stream.pushFrame(scheddgrpc.LogFrame{InstanceID: "i-1", Seq: 1, Stream: "stdout", Line: "hi\n", WrittenAt: time.Now()})
		stream.closeStream() // -> io.EOF on Recv
		h.serveAppLogs(context.Background(), rec, rec, "app-1", 0, time.Time{}, "")
		close(done)
	}()
	<-done
	body := rec.body.String()
	if !strings.Contains(body, "event: log") {
		t.Errorf("missing log frame: %q", body)
	}
	if !strings.Contains(body, "event: end\ndata: {}\n\n") {
		t.Errorf("expected empty end frame on clean close: %q", body)
	}
	if strings.Contains(body, `"reason"`) {
		t.Errorf("clean close must not carry a reason: %q", body)
	}
}

// TestServeAppLogs_GenericErrorDelegatesToRenderAppLogsError pins
// the error-delegation contract: a non-EOF, non-grace-coded error
// from the stream flows through recvCh and out the RenderAppLogsError
// path, which emits a degraded frame + terminal end with
// reason=schedd_unreachable.
func TestServeAppLogs_GenericErrorDelegatesToRenderAppLogsError(t *testing.T) {
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second,
		Backstop:  10 * time.Second,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	stream := newControllableScheddStream()
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		stream.pushFrame(scheddgrpc.LogFrame{InstanceID: "i-1", Seq: 1, Stream: "stdout", Line: "first\n", WrittenAt: time.Now()})
		stream.finish(errors.New("vmmd dial failed"))
		h.serveAppLogs(context.Background(), rec, rec, "app-1", 0, time.Time{}, "")
		close(done)
	}()
	<-done
	body := rec.body.String()
	if !strings.Contains(body, "event: log") {
		t.Errorf("first frame dropped: %q", body)
	}
	if !strings.Contains(body, "event: degraded") {
		t.Errorf("missing degraded frame: %q", body)
	}
	if !strings.Contains(body, `"reason":"schedd_unreachable"`) {
		t.Errorf("missing terminal reason: %q", body)
	}
}

// TestServeAppLogs_NotFoundDelegatesToRenderAppLogsError mirrors
// the generic-error case for the parked-app path: a gRPC
// codes.NotFound flows through the same renderAppLogsError
// mapping. The render path keys on the gRPC code, not on a
// lifted *api.Problem — schedd returns raw status.Error(codes.NotFound, ...)
// when the app is parked.
func TestServeAppLogs_NotFoundDelegatesToRenderAppLogsError(t *testing.T) {
	h := &AppLogsHandler{
		Heartbeat: 10 * time.Second,
		Backstop:  10 * time.Second,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	stream := newControllableScheddStream()
	h.ScheddFor = &fixedScheddResolver{c: &controllableScheddClient{stream: stream}}
	rec := newFlusherRecorder()
	done := make(chan struct{})
	go func() {
		stream.finish(status.Error(codes.NotFound, "state: not found"))
		h.serveAppLogs(context.Background(), rec, rec, "app-1", 0, time.Time{}, "")
		close(done)
	}()
	<-done
	body := rec.body.String()
	if !strings.Contains(body, "event: degraded") {
		t.Errorf("missing degraded frame: %q", body)
	}
	if !strings.Contains(body, `"reason":"not_found"`) {
		t.Errorf("missing not_found reason: %q", body)
	}
	if !strings.Contains(body, "event: end") {
		t.Errorf("missing terminal end: %q", body)
	}
}

// suppress unused-import warnings when io.EOF is no longer
// referenced directly inside this file's tests after refactors.
var _ = io.EOF

// --- plan-gate tests (issue #517 / PR-B, AC3) -----------------------
//
// enforceDeploymentFilter is the package-private helper extracted
// from stream() so the per-plan cap has direct whitebox coverage
// without standing up the full Auth + Store chain. These tests
// pin the contract:
//
//   - Free (LogDeploymentFilterMax == 0): writes the SSE error
//     frame and returns false so stream() short-circuits.
//   - Hobby / Pro / Scale (>0): no write, returns true.
//
// The wire shape is asserted via the flusherRecorder body so the
// stable SSE code "plan_deployment_filter_not_allowed" + the
// `event: error` framing both ship.

func TestEnforceDeploymentFilter_FreeRejects(t *testing.T) {
	rec := newFlusherRecorder()
	allowed := enforceDeploymentFilter(rec, api.PlanFree)
	if allowed {
		t.Fatal("Free plan must be rejected (cap=0)")
	}
	body := rec.body.String()
	if !strings.Contains(body, "event: error") {
		t.Errorf("missing event: error frame: %q", body)
	}
	if !strings.Contains(body, "plan_deployment_filter_not_allowed") {
		t.Errorf("missing stable code: %q", body)
	}
	if !strings.Contains(body, "max=0") {
		t.Errorf("missing cap in message (max=0 for Free): %q", body)
	}
}

func TestEnforceDeploymentFilter_HobbyAllows(t *testing.T) {
	rec := newFlusherRecorder()
	if !enforceDeploymentFilter(rec, api.PlanHobby) {
		t.Fatal("Hobby plan must be allowed (cap=1)")
	}
	if got := rec.body.String(); got != "" {
		t.Errorf("Hobby must not write any frame, got %q", got)
	}
}

func TestEnforceDeploymentFilter_ProAllows(t *testing.T) {
	rec := newFlusherRecorder()
	if !enforceDeploymentFilter(rec, api.PlanPro) {
		t.Fatal("Pro plan must be allowed (cap=10)")
	}
	if got := rec.body.String(); got != "" {
		t.Errorf("Pro must not write any frame, got %q", got)
	}
}

func TestEnforceDeploymentFilter_ScaleAllows(t *testing.T) {
	rec := newFlusherRecorder()
	if !enforceDeploymentFilter(rec, api.PlanScale) {
		t.Fatal("Scale plan must be allowed (cap=50)")
	}
	if got := rec.body.String(); got != "" {
		t.Errorf("Scale must not write any frame, got %q", got)
	}
}
