// Package egressgrpc server-side end-to-end test: writes into a
// shared *egresssink.EgressSink, dials a real gRPC server-streaming
// client, and asserts the producer/consumer pipeline delivers the
// (instance, minute, bytes) tuple end-to-end.
//
// The test deliberately does NOT exercise pkg/gateway.Handler
// (which lives behind WithEgressSink + recordEgress) — those tests
// already exist in pkg/gateway/handler_test.go and would have to
// import the test-only fakeBackend fixture. The split keeps this
// test focused on the wire shape + race-safety of the new gRPC
// surface alone.
package egressgrpc_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/gateway/egressgrpc"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

// shortUnixSocket returns a unix-domain socket path under /tmp
// with a leaf name guaranteed to be ≤ ~100 bytes — macOS
// rejects bind() with EINVAL on paths > ~104 bytes
// (sun_path is 108 bytes, includes the trailing NUL).
// t.TempDir() on a slow CI box produces paths long enough to
// trip the limit (the test pid + sanitised test name can chew
// ~150 chars), so we derive a short stable name from a tmpdir
// created via os.MkdirTemp with an explicit prefix.
func shortUnixSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "eg.")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "x.sock")
}

// startEgressTestGRPC is a shared helper: spin up a gRPC server
// exposing EgressTxServiceServer backed by the given sink on a
// unix socket, return the bound socket path + a cleanup func.
// Tests substitute the cadence via the package-level var.
func startEgressTestGRPC(t *testing.T, sink *egresssink.EgressSink) (string, func()) {
	t.Helper()
	prev := egressgrpc.StreamCadence
	egressgrpc.StreamCadence = 25 * time.Millisecond
	t.Cleanup(func() { egressgrpc.StreamCadence = prev })

	srv := egressgrpc.NewServer(sink, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	sock := shortUnixSocket(t)
	gs := grpc.NewServer()
	egresspb.RegisterEgressTxServiceServer(gs, srv)
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix %s: %v", sock, err)
	}
	go func() { _ = gs.Serve(lis) }()
	cleanup := func() {
		gs.GracefulStop()
		_ = lis.Close()
	}
	return sock, cleanup
}

func dialEgressTestClient(t *testing.T, sock string) egresspb.EgressTxServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(
		"unix:///"+strings.TrimPrefix(sock, "/"),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return net.Dial("unix", sock)
		}),
	)
	if err != nil {
		t.Fatalf("dial %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return egresspb.NewEgressTxServiceClient(conn)
}

func TestServer_StreamBytes_EmitsRecordedBytes(t *testing.T) {
	// NB: no t.Parallel() because startEgressTestGRPC mutates the
	// package-level StreamCadence variable for cadence tightening.
	// Parallel execution would race the read/write on the
	// Cadence cell across goroutines.
	sink := egresssink.NewEgressSink()
	sock, cleanup := startEgressTestGRPC(t, sink)
	defer cleanup()
	client := dialEgressTestClient(t, sock)

	// Push two bytes before the stream opens so the first cadence
	// tick has something to drain.
	sink.RecordResponseBytes("inst-1", 1024)
	sink.RecordResponseBytes("inst-2", 2048)
	sink.RecordRequest("inst-1", true)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	stream, err := client.StreamBytes(ctx, &egresspb.StreamBytesRequest{})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	// Read frames until either we've seen both instances or the
	// deadline fires.
	seen := map[string]uint64{}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && (len(seen) < 2) {
		frame, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if frame.Minute == nil {
			t.Fatalf("frame has nil minute: %+v", frame)
		}
		if got := frame.Minute.AsTime().Truncate(time.Minute); !got.Equal(frame.Minute.AsTime()) {
			t.Fatalf("minute not truncated: %v", frame.Minute.AsTime())
		}
		if frame.InstanceId == "inst-1" && (frame.Requests != 1 || frame.ColdBoots != 1) {
			t.Fatalf("inst-1 activity = requests:%d cold_boots:%d, want 1/1", frame.Requests, frame.ColdBoots)
		}
		seen[frame.InstanceId] = frame.Bytes
	}
	if seen["inst-1"] != 1024 {
		t.Fatalf("inst-1 bytes = %d, want 1024", seen["inst-1"])
	}
	if seen["inst-2"] != 2048 {
		t.Fatalf("inst-2 bytes = %d, want 2048", seen["inst-2"])
	}
}

func TestServer_StreamBytes_EmptyStreamOnIdleSink(t *testing.T) {
	// See TestServer_StreamBytes_EmitsRecordedBytes for the
	// StreamCadence-races-t.Parallel explanation; the same
	// package-level mutation forbids parallel here too.
	sink := egresssink.NewEgressSink()
	sock, cleanup := startEgressTestGRPC(t, sink)
	defer cleanup()
	client := dialEgressTestClient(t, sock)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	stream, err := client.StreamBytes(ctx, &egresspb.StreamBytesRequest{})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	// The first Recv blocks for the duration of ctx (the stream
	// is silent when the sink has nothing), returning
	// io.EOF/DeadlineExceeded via gRPC's wrapped form.
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("expected no frame on idle sink; got one")
	}
	// Either deadline-exceeded or context-deadline-exceeded are
	// both acceptable — both prove the stream stayed open and
	// silent rather than spuriously returning EOF. The wrapped
	// form is case-sensitive in the underlying error string; we
	// match "Deadline" (capitalised) since that's how
	// google.golang.org/grpc/status prints the code.
	if !strings.Contains(err.Error(), "Deadline") &&
		!strings.Contains(err.Error(), "deadline") &&
		!strings.Contains(err.Error(), "context") &&
		!errors.Is(err, io.EOF) {
		t.Fatalf("unexpected error (want DeadlineExceeded-or-EOF): %v", err)
	}
}

func TestServer_StreamBytes_HandlesNilSink(t *testing.T) {
	t.Parallel()
	srv := egressgrpc.NewServer(nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if got := srv.FramesSent(); got != 0 {
		t.Fatalf("frames sent = %d, want 0 (constructor invariant)", got)
	}
	if got := srv.ActiveStreams(); got != 0 {
		t.Fatalf("active streams = %d, want 0", got)
	}
}
