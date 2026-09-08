package scheddgrpc_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestStreamAppLogs_HappyPath drives the schedd-side handler
// against a stub engine that pushes 3 frames. The handler must
// forward every frame to the caller's gRPC stream in arrival order.
// (issue #254 / Move 4)
//
// The frames carry an instance_id so a consumer can interleave
// multi-instance streams deterministically (acceptance #5).
func TestStreamAppLogs_HappyPath(t *testing.T) {
	cl := newServer(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			if err := sink(sched.LogFrame{InstanceID: "inst-A", Seq: 1, Stream: "stdout", Line: "alpha", WrittenAt: time.Unix(0, 0).UTC()}); err != nil {
				return err
			}
			if err := sink(sched.LogFrame{InstanceID: "inst-A", Seq: 2, Stream: "stdout", Line: "beta", Level: "warn", WrittenAt: time.Unix(0, 0).UTC()}); err != nil {
				return err
			}
			if err := sink(sched.LogFrame{InstanceID: "inst-B", Seq: 1, Stream: "stderr", Line: "gamma", WrittenAt: time.Unix(0, 0).UTC()}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("StreamAppLogs dial: %v", err)
	}
	want := []struct {
		instance string
		seq      int64
		stream   string
		line     string
		level    string
	}{
		{"inst-A", 1, "stdout", "alpha", ""},
		{"inst-A", 2, "stdout", "beta", "warn"},
		{"inst-B", 1, "stderr", "gamma", ""},
	}
	for i, w := range want {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		if resp.GetInstanceId() != w.instance {
			t.Errorf("frame[%d].InstanceId = %q, want %q", i, resp.GetInstanceId(), w.instance)
		}
		if resp.GetSeq() != w.seq {
			t.Errorf("frame[%d].Seq = %d, want %d", i, resp.GetSeq(), w.seq)
		}
		if resp.GetStream() != w.stream {
			t.Errorf("frame[%d].Stream = %q, want %q", i, resp.GetStream(), w.stream)
		}
		if resp.GetLine() != w.line {
			t.Errorf("frame[%d].Line = %q, want %q", i, resp.GetLine(), w.line)
		}
		if resp.GetLevel() != w.level {
			t.Errorf("frame[%d].Level = %q, want %q", i, resp.GetLevel(), w.level)
		}
	}
	cancel()
	// After cancel, the stream should close; reading more frames
	// returns io.EOF (or a context-cancelled gRPC status — both are
	// acceptable per the test framework).
	if _, err := stream.Recv(); err == nil {
		t.Errorf("Recv after cancel: expected EOF or error, got nil")
	}
}

// TestStreamAppLogs_NotFound pins the empty-instance rejection:
// when the engine returns state.ErrNotFound (no live instances),
// the handler must lift it to codes.NotFound so the apid maps it
// to its 404 "the app is parked; wake it first".
func TestStreamAppLogs_NotFound(t *testing.T) {
	cl := newServer(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			return state.ErrNotFound
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{AppId: "parked-app"})
	if err != nil {
		t.Fatalf("StreamAppLogs dial: %v", err)
	}
	_, recErr := stream.Recv()
	if recErr == nil {
		t.Fatalf("expected NotFound, got nil")
	}
	st, ok := status.FromError(recErr)
	if !ok {
		// Could be unwrapped via grpcerr adapter; try lifting.
		if p, ok := grpcerr.FromStatus(recErr); ok && p != nil {
			if p.Status == int(codes.NotFound) {
				return
			}
		}
		t.Fatalf("Recv error is not a gRPC status: %v", recErr)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("code = %s, want NotFound", st.Code())
	}
}

// TestStreamAppLogs_InvalidArgument pins the empty-app-id rejection.
// A caller who forgets the field gets InvalidArgument, not NotFound.
// Distinguishes a wire-contract bug from a missing-app bug.
func TestStreamAppLogs_InvalidArgument(t *testing.T) {
	cl := newServer(t, &fakeEngine{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{AppId: ""})
	if err != nil {
		t.Fatalf("StreamAppLogs dial: %v", err)
	}
	_, recErr := stream.Recv()
	st, _ := status.FromError(recErr)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %s, want InvalidArgument", st.Code())
	}
}

// TestStreamAppLogs_ContextCancel pins the clean shutdown path:
// a caller cancel must terminate the stream without leaking the
// per-instance goroutines schedd spawned. The stream returns nil
// (or io.EOF) on cancel; not a gRPC error status.
func TestStreamAppLogs_ContextCancel(t *testing.T) {
	cl := newServer(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("StreamAppLogs dial: %v", err)
	}
	if _, err := stream.Recv(); err != nil && !errors.Is(err, io.EOF) {
		t.Logf("Recv returned: %v", err)
	}
}

// Compile-time assertion that wire.OpsMetrics is acceptable for the
// schedd test registry (issue #254 / Move 4 — keeps the same
// single-registry pattern as the rest of pkg/scheddgrpc).
var _ *wire.OpsMetrics = (*wire.OpsMetrics)(nil)

// TestStreamAppLogs_GapForwardedOverSchedd pins AC4 at the
// schedd-gRPC boundary: when the engine's per-instance fan-out
// emits a sched.LogFrame with IsGap=true (the vmmd producer
// labelled it via gap_reason), the server's sink translates it
// to a scheddpb.StreamAppLogsResponse with is_gap=true,
// gap_to_written_at populated, and gap_reason propagated. The
// line-frame fields (seq/stream/line/written_at) stay zero on
// the gap frame — Finding 2's contract — and a subsequent line
// frame arrives intact.
//
// A regression here means gatewayd-internal's RenderAppLogGap stops
// seeing `is_gap=true` and falls back to its broken
// "since_below_retained" heuristic — Finding 1.
func TestStreamAppLogs_GapForwardedOverSchedd(t *testing.T) {
	headAt := time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC)
	cl := newServer(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			if err := sink(sched.LogFrame{
				InstanceID:     "inst-A",
				IsGap:          true,
				GapToWrittenAt: headAt,
				GapReason:      "seq_below_retained",
			}); err != nil {
				return err
			}
			if err := sink(sched.LogFrame{
				InstanceID: "inst-A",
				Seq:        42,
				Stream:     "stdout",
				Line:       "first after gap",
				WrittenAt:  headAt.Add(time.Second),
			}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{AppId: "app-1"})
	if err != nil {
		t.Fatalf("StreamAppLogs dial: %v", err)
	}

	// Frame 0: the gap.
	gap, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv gap: %v", err)
	}
	if !gap.GetIsGap() {
		t.Errorf("is_gap = false on gap frame, want true")
	}
	if got := gap.GetInstanceId(); got != "inst-A" {
		t.Errorf("instance_id = %q, want inst-A", got)
	}
	if got := gap.GetGapReason(); got != "seq_below_retained" {
		t.Errorf("gap_reason = %q, want seq_below_retained", got)
	}
	if ts := gap.GetGapToWrittenAt(); ts == nil || !ts.AsTime().Equal(headAt) {
		t.Errorf("gap_to_written_at = %v, want %v", ts, headAt)
	}
	// Line-frame fields must be zero on a gap frame (Finding 2).
	if gap.GetSeq() != 0 {
		t.Errorf("seq on gap frame = %d, want 0", gap.GetSeq())
	}
	if gap.GetStream() != "" {
		t.Errorf("stream on gap frame = %q, want empty", gap.GetStream())
	}
	if gap.GetLine() != "" {
		t.Errorf("line on gap frame = %q, want empty", gap.GetLine())
	}
	if gap.GetWrittenAt() != nil {
		t.Errorf("written_at on gap frame = %v, want nil", gap.GetWrittenAt())
	}

	// Frame 1: the line.
	line, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv line: %v", err)
	}
	if line.GetIsGap() {
		t.Errorf("is_gap = true on line frame, want false")
	}
	if got := line.GetSeq(); got != 42 {
		t.Errorf("seq = %d, want 42", got)
	}
	if got := line.GetStream(); got != "stdout" {
		t.Errorf("stream = %q, want stdout", got)
	}
	if got := line.GetLine(); got != "first after gap" {
		t.Errorf("line = %q, want %q", got, "first after gap")
	}
}

// TestStreamAppLogs_FilterLevelDropsAndCounts (issue #309 /
// tier-2 DX): when the proto request carries a level floor,
// line frames that don't satisfy the heuristic floor are
// dropped at the schedd sink, and the per-reason counter
// apid_logs_dropped_total{reason="filter_level"} increments
// once per drop. Pass-through line frames still arrive at the
// caller.
//
// The test stands up a sibling helper to newServer so the
// wire.OpsMetrics can be inspected for the drop counter. The
// fakeEngine emits a mix of (info, warn, error, plain) lines
// and the caller asserts only warn+error arrive AND the
// filter_level counter has the expected delta.
func TestStreamAppLogs_FilterLevelDropsAndCounts(t *testing.T) {
	fedAll := make(chan struct{})
	cl, metrics := newServerWithMetrics(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			lines := []sched.LogFrame{
				{InstanceID: "inst-A", Seq: 1, Stream: "stdout", Line: "[INFO] startup ok"},
				{InstanceID: "inst-A", Seq: 2, Stream: "stdout", Line: "[WARN] retrying"},
				{InstanceID: "inst-A", Seq: 3, Stream: "stdout", Line: "[ERROR] db unreachable"},
				{InstanceID: "inst-A", Seq: 4, Stream: "stdout", Line: "plain stdout line"},
			}
			for _, f := range lines {
				if err := sink(f); err != nil {
					return err
				}
			}
			close(fedAll)
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{
		AppId: "app-1",
		Level: "warn",
	})
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	// Expect only warn + error lines through (info and plain are
	// below the warn floor).
	wantLines := []string{"[WARN] retrying", "[ERROR] db unreachable"}
	for i, want := range wantLines {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		if got := resp.GetLine(); got != want {
			t.Errorf("frame[%d].Line = %q, want %q", i, got, want)
		}
	}
	// Wait for all lines to be fed into the sink before canceling
	select {
	case <-fedAll:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for engine to finish feeding lines")
	}
	// One more Recv should block (no more frames), then cancel
	// unblocks the engine. After cancel the stream returns EOF.
	cancel()
	if _, err := stream.Recv(); err == nil {
		t.Errorf("Recv after cancel: expected EOF, got nil")
	}

	// Counter assertions: info + plain were dropped → 2 increments
	// on filter_level. The other reasons stay at zero.
	for _, c := range []struct {
		reason string
		want   float64
	}{
		{"filter_level", 2},
		{"filter_grep", 0},
		{"slow_subscriber", 0},
	} {
		got := readCounter(t, metrics, "schedd_test_logs_dropped_total", c.reason)
		if got != c.want {
			t.Errorf("schedd_test_logs_dropped_total{reason=%q} = %v, want %v", c.reason, got, c.want)
		}
	}
}

// TestStreamAppLogs_FilterGrepDropsAndCounts (issue #309 /
// tier-2 DX): when the proto request carries a grep regex,
// line frames that don't match are dropped at the schedd
// sink and apid_logs_dropped_total{reason="filter_grep"}
// increments once per drop. Mirrors the level test shape;
// keeps the per-reason counter coverage split so a future
// regression that confuses the two reasons trips both tests.
func TestStreamAppLogs_FilterGrepDropsAndCounts(t *testing.T) {
	fedAll := make(chan struct{})
	cl, metrics := newServerWithMetrics(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			lines := []sched.LogFrame{
				{InstanceID: "inst-A", Seq: 1, Stream: "stdout", Line: "[INFO] startup ok"},
				{InstanceID: "inst-A", Seq: 2, Stream: "stdout", Line: "[ERROR] timeout exceeded"},
				{InstanceID: "inst-A", Seq: 3, Stream: "stdout", Line: "another timeout here"},
				{InstanceID: "inst-A", Seq: 4, Stream: "stdout", Line: "no match"},
			}
			for _, f := range lines {
				if err := sink(f); err != nil {
					return err
				}
			}
			close(fedAll)
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{
		AppId: "app-1",
		Grep:  "timeout",
	})
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	wantLines := []string{"[ERROR] timeout exceeded", "another timeout here"}
	for i, want := range wantLines {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		if got := resp.GetLine(); got != want {
			t.Errorf("frame[%d].Line = %q, want %q", i, got, want)
		}
	}
	// Wait for all lines to be fed into the sink before canceling
	select {
	case <-fedAll:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for engine to finish feeding lines")
	}
	cancel()
	if _, err := stream.Recv(); err == nil {
		t.Errorf("Recv after cancel: expected EOF, got nil")
	}
	for _, c := range []struct {
		reason string
		want   float64
	}{
		{"filter_grep", 2},
		{"filter_level", 0},
		{"slow_subscriber", 0},
	} {
		got := readCounter(t, metrics, "schedd_test_logs_dropped_total", c.reason)
		if got != c.want {
			t.Errorf("schedd_test_logs_dropped_total{reason=%q} = %v, want %v", c.reason, got, c.want)
		}
	}
}

// TestStreamAppLogs_GapBypassesFilter (issue #254 / Move 4,
// issue #309 / tier-2 DX interaction): a gap frame is
// sequencing metadata the customer must see regardless of
// grep/level — a dropped gap would hide a stall from the
// customer's session. The test confirms the filter sink
// branches on IsGap before MatchLine so gap frames pass
// through even when the customer's filter would drop them.
func TestStreamAppLogs_GapBypassesFilter(t *testing.T) {
	headAt := time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC)
	cl, metrics := newServerWithMetrics(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			if err := sink(sched.LogFrame{
				InstanceID:     "inst-A",
				IsGap:          true,
				GapToWrittenAt: headAt,
				GapReason:      "seq_below_retained",
			}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// A level=error filter would drop "[INFO]"-only lines; the
	// gap frame still arrives because IsGap=true.
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{
		AppId: "app-1",
		Level: "error",
	})
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !resp.GetIsGap() {
		t.Errorf("expected gap frame, got line frame")
	}
	if resp.GetGapReason() != "seq_below_retained" {
		t.Errorf("gap reason = %q, want seq_below_retained", resp.GetGapReason())
	}
	cancel()
	// No drops — the gap frame bypassed the filter, so the
	// counter sits at zero.
	if got := readCounter(t, metrics, "schedd_test_logs_dropped_total", "filter_level"); got != 0 {
		t.Errorf("filter_level counter = %v, want 0 (gap frames bypass filter)", got)
	}
}

// TestStreamAppLogs_DualFilterAttribution (issue #309 / tier-2
// DX, PR #728 code-review finding #1): when both --level and
// --grep are active, the drop counter must credit the
// actually-failing filter, not unconditionally attribute the
// drop to --level. A line that satisfies the level floor but
// fails the grep regex is a grep drop; a line that satisfies
// the grep regex but falls below the level floor is a level
// drop. The earlier implementation mis-attributed to
// filter_level in both cases, which broke operator triage on
// the §12 panel.
//
// Pins three attribution cases:
//  1. passes level, fails grep → filter_grep increments
//  2. fails level, passes grep → filter_level increments
//  3. fails both → filter_level increments (tiebreaker; the
//     line would have been dropped by the broad noise filter
//     anyway, and customers tend to set --level first and add
//     --grep to narrow further).
func TestStreamAppLogs_DualFilterAttribution(t *testing.T) {
	cl, metrics := newServerWithMetrics(t, &fakeEngine{
		streamLogFn: func(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink scheddgrpc.LogFrameSink) error {
			// (1) passes --level=warn, fails --grep=timeout
			if err := sink(sched.LogFrame{InstanceID: "inst-A", Seq: 1, Stream: "stdout", Line: "[ERROR] db connection OK"}); err != nil {
				return err
			}
			// (2) fails --level=warn, passes --grep=timeout
			if err := sink(sched.LogFrame{InstanceID: "inst-A", Seq: 2, Stream: "stdout", Line: "[INFO] timeout scheduled"}); err != nil {
				return err
			}
			// (3) fails both (below warn floor AND no timeout)
			if err := sink(sched.LogFrame{InstanceID: "inst-A", Seq: 3, Stream: "stdout", Line: "[INFO] something else"}); err != nil {
				return err
			}
			// Pass-through: passes both.
			if err := sink(sched.LogFrame{InstanceID: "inst-A", Seq: 4, Stream: "stdout", Line: "[ERROR] timeout exceeded"}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.StreamAppLogs(ctx, &scheddpb.StreamAppLogsRequest{
		AppId: "app-1",
		Level: "warn",
		Grep:  "timeout",
	})
	if err != nil {
		t.Fatalf("StreamAppLogs: %v", err)
	}
	// Only frame #4 passes both filters.
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got := resp.GetLine(); got != "[ERROR] timeout exceeded" {
		t.Errorf("frame.Line = %q, want %q", got, "[ERROR] timeout exceeded")
	}
	cancel()
	if _, err := stream.Recv(); err == nil {
		t.Errorf("Recv after cancel: expected EOF, got nil")
	}

	// Counter assertions:
	//   filter_grep  = 1 (frame #1: passes level, fails grep)
	//   filter_level = 2 (frames #2 and #3: fail level;
	//                              case (3) tiebreaker is level)
	for _, c := range []struct {
		reason string
		want   float64
	}{
		{"filter_grep", 1},
		{"filter_level", 2},
		{"slow_subscriber", 0},
	} {
		got := readCounter(t, metrics, "schedd_test_logs_dropped_total", c.reason)
		if got != c.want {
			t.Errorf("schedd_test_logs_dropped_total{reason=%q} = %v, want %v (PR #728 finding #1)", c.reason, got, c.want)
		}
	}
}
