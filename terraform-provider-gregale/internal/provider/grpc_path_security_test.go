package provider

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Exercise the provider module's selected plugin transport on the wire. Before
// the security fix, a path without '/' can reach authorization instead of being
// rejected by the transport (GHSA-p77j-4mvh-x3m3). Keep that rejection boundary.
func TestPluginRPCRejectsNonCanonicalMethodPath(t *testing.T) {
	t.Parallel()
	const method = "/grpc.health.v1.Health/Check"
	var authorizations atomic.Int32
	server := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		authorizations.Add(1)
		if info.FullMethod == method {
			return nil, status.Error(codes.PermissionDenied, "canonical method denied")
		}
		return handler(ctx, request)
	}))
	healthpb.RegisterHealthServer(server, health.NewServer())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	go func() { _ = server.Serve(listener) }()
	if got := rawPluginRPCStatus(t, listener.Addr().String(), method); got != "7" {
		t.Fatalf("canonical method grpc-status = %q, want PermissionDenied (7)", got)
	}
	if got := rawPluginRPCStatus(t, listener.Addr().String(), method[1:]); got != "12" {
		t.Fatalf("noncanonical method grpc-status = %q, want Unimplemented (12)", got)
	}
	if got := authorizations.Load(); got != 1 {
		t.Fatalf("authorization calls = %d, malformed path must be rejected before authorization", got)
	}
}

func rawPluginRPCStatus(t *testing.T, address, path string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(http2.ClientPreface)); err != nil {
		t.Fatal(err)
	}
	framer := http2.NewFramer(conn, conn)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := framer.WriteSettings(); err != nil {
		t.Fatal(err)
	}
	var block bytes.Buffer
	encoder := hpack.NewEncoder(&block)
	for _, field := range []hpack.HeaderField{
		{Name: ":method", Value: "POST"},
		{Name: ":scheme", Value: "http"},
		{Name: ":authority", Value: address},
		{Name: ":path", Value: path},
		{Name: "content-type", Value: "application/grpc"},
		{Name: "te", Value: "trailers"},
	} {
		if err := encoder.WriteField(field); err != nil {
			t.Fatal(err)
		}
	}
	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: block.Bytes(), EndHeaders: true}); err != nil {
		t.Fatal(err)
	}
	// A zero-length protobuf HealthCheckRequest in the normal gRPC envelope.
	if err := framer.WriteData(1, true, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	var grpcStatus string
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if err := framer.WriteSettingsAck(); err != nil {
					t.Fatal(err)
				}
			}
		case *http2.MetaHeadersFrame:
			for _, field := range frame.Fields {
				if field.Name == "grpc-status" {
					grpcStatus = field.Value
				}
			}
			if frame.StreamID == 1 && frame.StreamEnded() {
				return grpcStatus
			}
		case *http2.RSTStreamFrame:
			t.Fatalf("RPC reset instead of returning a gRPC status: %s", frame.ErrCode)
		}
	}
}
