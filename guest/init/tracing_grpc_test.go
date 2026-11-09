package main

// adr: 20261108100000 — the in-VM receiver accepts OTLP/gRPC trace and log
// exports on 127.0.0.1:4317 from a real gRPC client and maps host acks to
// the gRPC codes OTLP exporters retry on.

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/onebox-faas/faas/pkg/api"
)

type grpcBridgeHarness struct {
	conn   *grpc.ClientConn
	mu     sync.Mutex
	frames [][]byte
	ack    byte
}

func startGRPCBridge(t *testing.T, ack byte) (*grpcBridgeHarness, *traceBridge) {
	t.Helper()
	h := &grpcBridgeHarness{ack: ack}
	bridge := newTraceBridge(func(_ context.Context, frame []byte) (byte, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.frames = append(h.frames, append([]byte(nil), frame...))
		return h.ack, nil
	})
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTraceGRPCServer(bridge.grpcHandler())
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	h.conn = conn
	return h, bridge
}

func sampleTraceRequest() *collectortracepb.ExportTraceServiceRequest {
	return &collectortracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
			TraceId: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
			SpanId:  []byte{1, 2, 3, 4, 5, 6, 7, 8},
			Name:    "GET /orders",
		}}}},
	}}}
}

func callCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestTraceGRPCReceiverForwardsTraceExport(t *testing.T) {
	h, _ := startGRPCBridge(t, api.TraceAckAccepted)
	req := sampleTraceRequest()
	if _, err := collectortracepb.NewTraceServiceClient(h.conn).Export(callCtx(t), req); err != nil {
		t.Fatalf("Export: %v", err)
	}
	want, _ := proto.Marshal(req)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.frames) != 1 {
		t.Fatalf("frames = %d", len(h.frames))
	}
	frame := h.frames[0]
	if frame[4] != api.TraceCodecProtobuf || string(frame[5:]) != string(want) {
		t.Fatalf("frame codec %#x, body matches %v", frame[4], string(frame[5:]) == string(want))
	}
}

func TestTraceGRPCReceiverForwardsLogExportAndGzip(t *testing.T) {
	h, _ := startGRPCBridge(t, api.TraceAckAccepted)
	req := &collectorlogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{
		ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{SeverityText: "INFO"}}}},
	}}}
	if _, err := collectorlogspb.NewLogsServiceClient(h.conn).Export(callCtx(t), req, grpc.UseCompressor(gzip.Name)); err != nil {
		t.Fatalf("Export: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if got := h.frames[0][4]; got != api.TraceCodecGzipProtobuf|api.TraceCodecLogsSignal {
		t.Fatalf("frame codec = %#x, want gzip protobuf logs", got)
	}
}

func TestTraceGRPCReceiverStatusMapping(t *testing.T) {
	cases := []struct {
		ack  byte
		code codes.Code
	}{
		{api.TraceAckLimited, codes.Unavailable},
		{api.TraceAckUnavailable, codes.Unavailable},
		{api.TraceAckRejected, codes.InvalidArgument},
	}
	for _, tc := range cases {
		h, _ := startGRPCBridge(t, tc.ack)
		_, err := collectortracepb.NewTraceServiceClient(h.conn).Export(callCtx(t), sampleTraceRequest())
		if status.Code(err) != tc.code {
			t.Fatalf("ack %d: code = %v (%v), want %v", tc.ack, status.Code(err), err, tc.code)
		}
	}
}

func TestTraceGRPCReceiverEmptyExportSendsNothing(t *testing.T) {
	h, _ := startGRPCBridge(t, api.TraceAckAccepted)
	if _, err := collectortracepb.NewTraceServiceClient(h.conn).Export(callCtx(t), &collectortracepb.ExportTraceServiceRequest{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.frames) != 0 {
		t.Fatalf("empty export sent %d frames", len(h.frames))
	}
}

func TestTraceGRPCReceiverRejectsOtherMethods(t *testing.T) {
	h, _ := startGRPCBridge(t, api.TraceAckAccepted)
	err := h.conn.Invoke(callCtx(t), "/opentelemetry.proto.collector.profiles.v1development.ProfilesService/Export", sampleTraceRequest(), &collectortracepb.ExportTraceServiceResponse{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("code = %v, want Unimplemented", status.Code(err))
	}
}

func TestTraceGRPCReceiverBusy(t *testing.T) {
	h, bridge := startGRPCBridge(t, api.TraceAckAccepted)
	for range api.TraceMaxConcurrentUploads {
		bridge.slots <- struct{}{}
	}
	_, err := collectortracepb.NewTraceServiceClient(h.conn).Export(callCtx(t), sampleTraceRequest())
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("code = %v, want Unavailable", status.Code(err))
	}
}

func TestTraceGRPCReceiverCopiesToForwarder(t *testing.T) {
	sink, srv := newForwardSink()
	defer srv.Close()
	h, bridge := startGRPCBridge(t, api.TraceAckAccepted)
	fwd := newTraceForwarder(api.TracingForward{Endpoint: srv.URL}, func() (http.Header, error) { return http.Header{}, nil })
	bridge.forward = fwd
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go fwd.run(ctx, 1)
	req := sampleTraceRequest()
	if _, err := collectortracepb.NewTraceServiceClient(h.conn).Export(callCtx(t), req); err != nil {
		t.Fatalf("Export: %v", err)
	}
	want, _ := proto.Marshal(req)
	got := sink.wait(t, 1)[0]
	if got.path != "/v1/traces" || got.contentType != "application/x-protobuf" || got.body != string(want) {
		t.Fatalf("forwarded = path %q type %q body matches %v", got.path, got.contentType, got.body == string(want))
	}
}

func TestGRPCUnaryMessage(t *testing.T) {
	for _, bad := range [][]byte{nil, {0, 0, 0}, {2, 0, 0, 0, 0}, {0, 0, 0, 0, 2, 'x'}} {
		if _, _, ok := grpcUnaryMessage(bad); ok {
			t.Fatalf("%v accepted", bad)
		}
	}
	msg, compressed, ok := grpcUnaryMessage([]byte{1, 0, 0, 0, 1, 'x'})
	if !ok || !compressed || string(msg) != "x" {
		t.Fatalf("got %q %v %v", msg, compressed, ok)
	}
}
