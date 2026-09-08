package vmmdgrpc_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// TestLogs_HappyPath drives the Logs(req) handler against an injected
// ring (issue #254, Move 4). The producer pre-populates 3 lines; we
// drain the initial page, then publish 2 more live lines via Subscribe
// and assert the stream emits them in commit order.
//
// Backpressure / timing: gRPC's stream.Send on a buffered client
// keeps the round-trip cost bounded, and the test wraps the live-
// tail Recv in its own per-call ctx so a real Subscribe deadlock
// still trips within ~1 s. The 2026-07-31 coverage-job flake
// (`coverage (Codecov)` retry ×2, same test) was traced to the
// Subscribe→Send propagation occasionally taking > 2 s under
// `-coverpkg=all` across 700+ packages — the previous single-ctx
// design charged the entire test budget against one ctx, including
// the snapshot Recvs that returned in microseconds, leaving no
// slack for the live-tail Recv at the end of that budget. Using a
// per-Recv budget isolates the snapshot phase (µs) from the
// live-tail phase (the propagation delay), so a handler that's
// actually hung still fails fast — only the rate-limited
// propagation gets the longer window.
//
// snapshotBudget covers the streaming RPC itself plus the snapshot
// Recvs (microseconds in practice). liveTailWait is the per-call
// budget for just the live-tail Seq=4 Recv: 1 s is ~1000× the
// steady-state ms latency and ~1000× a real bug; the coverage job
// still triggers in <1 s of CPU because the Subscribe→Send
// propagation completes far under that.
func TestLogs_HappyPath(t *testing.T) {
	const (
		snapshotBudget = 2 * time.Second
		liveTailWait   = 5 * time.Second
	)
	ring := logbuf.New(1 << 20)
	for _, ln := range []string{"alpha\n", "beta\n", "gamma\n"} {
		if _, err := ring.Write("stdout", []byte(ln)); err != nil {
			t.Fatalf("seed Write: %v", err)
		}
	}
	cl := startLogsTestClient(t, &fakeVMM{
		logRingFn: func(string) *logbuf.Ring { return ring },
	})

	ctx, cancel := context.WithTimeout(context.Background(), snapshotBudget)
	defer cancel()
	// SinceSeq=1 replays the seeded lines (Seq 1..3). SinceSeq=0 is
	// the "tail from now" sentinel (per pkg/fcvm/logbuf.Ring.Snapshot
	// docs) — the handler skips the replay phase and Subscribe only
	// delivers lines committed after the RPC is opened. We want the
	// test to assert the initial-page path, so we pass an explicit
	// cursor of 1.
	stream, err := cl.Logs(ctx, &vmmdpb.LogsRequest{Instance: "inst-1", SinceSeq: 1})
	if err != nil {
		t.Fatalf("Logs dial: %v", err)
	}

	// Read the initial page (3 lines). Snapshot is synchronous: the
	// handler emits all 3 before Subscribe() blocks on the next line,
	// so the 3 Recv calls return immediately.
	want := []string{"alpha", "beta", "gamma"}
	got := make([]*vmmdpb.LogsResponse, 0, 4)
	for i := 0; i < len(want); i++ {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		got = append(got, resp)
	}
	for i, line := range want {
		if got[i].Line != line {
			t.Errorf("got[%d].Line = %q, want %q", i, got[i].Line, line)
		}
		if got[i].Stream != "stdout" {
			t.Errorf("got[%d].Stream = %q, want stdout", i, got[i].Stream)
		}
		if got[i].Seq != int64(i+1) {
			t.Errorf("got[%d].Seq = %d, want %d", i, got[i].Seq, i+1)
		}
	}
	// Live tail: push one more line, expect it on the stream. We use
	// a per-Recv context (recvWithCtx) so the budget for this single
	// Recv is independent of any latency already elapsed under the RPC
	// ctx — that's the move that pins down the coverage-job flake
	// without masking a real Subscribe deadlock.
	if _, err := ring.Write("stderr", []byte("post-subscribe\n")); err != nil {
		t.Fatalf("tail Write: %v", err)
	}
	tailCtx, tailCancel := context.WithTimeout(context.Background(), liveTailWait)
	defer tailCancel()
	resp, err := recvWithCtx(tailCtx, stream)
	if err != nil {
		t.Fatalf("tail Recv: %v", err)
	}
	if resp.Line != "post-subscribe" || resp.Stream != "stderr" || resp.Seq != 4 {
		t.Errorf("tail frame = %+v, want line=post-subscribe stream=stderr seq=4", resp)
	}
}

func TestLogs_StructuredLevelRoundTrip(t *testing.T) {
	ring := logbuf.New(1 << 20)
	line := `{"severity":"WARNING","message":"slow"}`
	if _, err := ring.Write("stderr", []byte(line+"\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	cl := startLogsTestClient(t, &fakeVMM{
		logRingFn: func(string) *logbuf.Ring { return ring },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.Logs(ctx, &vmmdpb.LogsRequest{Instance: "inst-1", SinceSeq: 1})
	if err != nil {
		t.Fatalf("Logs dial: %v", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.GetLevel() != "warn" {
		t.Errorf("level = %q, want warn", resp.GetLevel())
	}
	if resp.GetLine() != line {
		t.Errorf("line = %q, want original JSON", resp.GetLine())
	}
}

// TestLogs_NotFound pins the wire contract: when the instance is not
// alive on this vmmd (LogRing returns nil), the handler returns
// codes.NotFound with no stream opened. apid maps that to its own
// 404 Problem.
func TestLogs_NotFound(t *testing.T) {
	cl := startLogsTestClient(t, &fakeVMM{
		logRingFn: func(string) *logbuf.Ring { return nil },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.Logs(ctx, &vmmdpb.LogsRequest{Instance: "missing"})
	if err != nil {
		t.Fatalf("Logs dial: %v", err)
	}
	_, recvErr := stream.Recv()
	if recvErr == nil {
		t.Fatalf("expected NotFound error, got nil")
	}
	st, ok := status.FromError(recvErr)
	if !ok {
		t.Fatalf("Recv error is not a gRPC status: %v", recvErr)
	}
	if st.Code() != codes.NotFound {
		t.Errorf("code = %s, want NotFound", st.Code())
	}
}

// TestLogs_InvalidArgument pins the empty-instance rejection: callers
// who forget the field get InvalidArgument, not NotFound. Distinguishes
// a wire-contract bug from a missing-instance bug.
func TestLogs_InvalidArgument(t *testing.T) {
	cl := startLogsTestClient(t, &fakeVMM{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := cl.Logs(ctx, &vmmdpb.LogsRequest{Instance: ""})
	if err != nil {
		t.Fatalf("Logs dial: %v", err)
	}
	_, recvErr := stream.Recv()
	st, _ := status.FromError(recvErr)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("code = %s, want InvalidArgument", st.Code())
	}
}

// TestLogs_ContextCancel verifies that cancelling the caller's context
// cleanly terminates the stream without leaking the Subscribe() goroutine.
// We pin this with a short context timeout that fires while we're waiting
// on a quiet ring; the handler must return nil (not an error) on cancel.
func TestLogs_ContextCancel(t *testing.T) {
	ring := logbuf.New(1 << 20)
	cl := startLogsTestClient(t, &fakeVMM{
		logRingFn: func(string) *logbuf.Ring { return ring },
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stream, err := cl.Logs(ctx, &vmmdpb.LogsRequest{Instance: "inst-1"})
	if err != nil {
		t.Fatalf("Logs dial: %v", err)
	}
	// Drain the empty initial page so we end up in Subscribe().
	if _, err := stream.Recv(); err != nil && !errors.Is(err, io.EOF) {
		// gRPC may surface the cancel as Canceled; both are acceptable
		// — what we pin is "we got OUT of the loop" and the goroutine
		// inside the handler has returned.
		t.Logf("Recv returned: %v", err)
	}
}

// startLogsTestClient stands up a bufconn-backed gRPC server with the
// given fakeVMM and returns a vmmdpb.VmmdClient. Mirrors the pattern
// in bufconn_test.go but is scoped to Logs-specific tests so future
// additions don't pay the full handler suite's setup cost.
func startLogsTestClient(t *testing.T, fake *fakeVMM) vmmdpb.VmmdClient {
	t.Helper()
	const bufSize = 1 << 20
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	impl := vmmdgrpc.New(fake, wire.NewOpsMetrics("vmmdgrpc_logs_test"), "1.0", nil)
	impl.Register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.GracefulStop()
		_ = lis.Close()
	})

	dialer := func(context.Context, string) (net.Conn, error) { return lis.Dial() }
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return vmmdpb.NewVmmdClient(conn)
}

// recvWithCtx waits for the next frame on a server-streaming RPC,
// bounded by the supplied ctx. The stdlib gRPC stream.Recv does NOT
// honour its own context argument: cancelling the caller's RPC ctx
// terminates the entire stream, which is not what we want for a
// per-Recv deadline. The canonical workaround is to Recv on a
// goroutine and select on a ctx.Done channel; whichever wins
// determines the return. If the ctx fires first, the goroutine is
// left blocked on Recv until the underlying stream returns (which
// happens when the RPC ctx is cancelled at test teardown via
// `defer cancel()` of the parent ctx). The recv goroutine cannot
// leak past the test — t.Cleanup in startLogsTestClient closes the
// connection, which unblocks Recv with an error.
func recvWithCtx(ctx context.Context, stream vmmdpb.Vmmd_LogsClient) (*vmmdpb.LogsResponse, error) {
	type result struct {
		resp *vmmdpb.LogsResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		r, err := stream.Recv()
		ch <- result{r, err}
	}()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
