package vmmdhop

// adr: 20261121202814 — gRPC against the hop over vmmd's local unix data
// socket, the path prod forwards take when the VM is on the gateway's node
// (FAAS_GATEWAY_LOCAL_VMMD_SOCKET=1): no TLS, the unix peer trusted by the
// same caller-policy interceptor on both. Run with -cpu 1 so ns/op is close
// to the CPU both ends spend:
//
//	go test -run '^$' -bench 'Unix' -benchmem -cpu 1 -count 6 ./pkg/vmmdhop/

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

func unixSocket(tb testing.TB) string {
	tb.Helper()
	// macOS caps unix socket paths near 104 bytes; testing's TempDir is
	// longer.
	dir, err := os.MkdirTemp("", "vh")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

// startUnixGRPC serves fake over gRPC on a unix socket, as vmmd's local data
// socket does.
func startUnixGRPC(tb testing.TB, fake *fakeVmmd) string {
	tb.Helper()
	path := unixSocket(tb)
	lis, err := net.Listen("unix", path)
	if err != nil {
		tb.Fatal(err)
	}
	gsrv := grpc.NewServer(grpc.ChainStreamInterceptor(vmmdgrpc.CallerPolicyStreamInterceptor()))
	vmmdpb.RegisterVmmdServer(gsrv, fake)
	go func() { _ = gsrv.Serve(lis) }()
	tb.Cleanup(gsrv.Stop)
	return path
}

// startUnixHop serves fake over the hop on a unix socket: a plain
// connection whose peer carries no TLS state, as gRPC's does there.
func startUnixHop(tb testing.TB, fake *fakeVmmd) string {
	tb.Helper()
	pki := newTestPKI(tb)
	srv, err := NewServer(context.Background(), ServerConfig{
		TLS: pki.serverTLS(tb), Service: fake, Interceptor: vmmdgrpc.CallerPolicyStreamInterceptor(),
		Streams: StreamHandlers(&vmmdpb.Vmmd_ServiceDesc, "ForwardHTTPStream"),
	})
	if err != nil {
		tb.Fatal(err)
	}
	path := unixSocket(tb)
	lis, err := net.Listen("unix", path)
	if err != nil {
		tb.Fatal(err)
	}
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				if !srv.track() {
					return
				}
				defer srv.wg.Done()
				srv.serveExchanges(context.Background(), conn, &peer.Peer{Addr: conn.RemoteAddr(), LocalAddr: conn.LocalAddr()})
			}()
		}
	}()
	tb.Cleanup(func() { _ = lis.Close(); srv.Close() })
	return path
}

func unixHopClient(path string) *Client {
	d := &net.Dialer{Timeout: dialTimeout}
	return &Client{address: path, network: "unix", dial: d.DialContext, now: timeNow}
}

// benchForward runs one forward: init with the headers a browser request
// carries, body bytes of request body (echoed back), then the response.
func benchForward(b *testing.B, s grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], body []byte) {
	init := &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1", Method: "GET", RequestUri: "/", Port: 8080, Stream: true, AppProtocol: "http1", Headers: []*vmmdpb.Header{
		{Name: "Host", Value: "app.gregale.dev"}, {Name: "User-Agent", Value: "k6/1.0"}, {Name: "Accept", Value: "*/*"},
		{Name: "Traceparent", Value: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"},
	}}
	if err := s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: init}}); err != nil {
		b.Fatal(err)
	}
	if len(body) > 0 {
		if err := s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: body}}); err != nil {
			b.Fatal(err)
		}
	}
	_ = s.CloseSend()
	got := 0
	for {
		m, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			b.Fatal(err)
		}
		got += len(m.GetBodyChunk())
	}
	if got != len(body) {
		b.Fatalf("echoed %d bytes, want %d", got, len(body))
	}
}

var unixBenchBodies = []struct {
	name string
	size int
}{{"hello", 0}, {"16KiB", 16 << 10}}

func BenchmarkUnixForwardGRPC(b *testing.B) {
	for _, bb := range unixBenchBodies {
		b.Run(bb.name, func(b *testing.B) {
			path := startUnixGRPC(b, &fakeVmmd{})
			conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()
			cli := vmmdpb.NewVmmdClient(conn)
			body := make([]byte, bb.size)
			b.ReportAllocs()
			for b.Loop() {
				s, err := cli.ForwardHTTPStream(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				benchForward(b, s, body)
			}
		})
	}
}

func BenchmarkUnixForwardHop(b *testing.B) {
	for _, bb := range unixBenchBodies {
		b.Run(bb.name, func(b *testing.B) {
			path := startUnixHop(b, &fakeVmmd{})
			c := unixHopClient(path)
			defer c.Close()
			body := make([]byte, bb.size)
			b.ReportAllocs()
			for b.Loop() {
				s, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](context.Background(), c, forwardMethod)
				if err != nil {
					b.Fatal(err)
				}
				benchForward(b, s, body)
			}
		})
	}
}

// TestUnixHopServesThroughCallerPolicy checks the benchmark is like for
// like: both transports reach the handler through the caller policy on a
// unix peer, and the hop reuses its connection.
func TestUnixHopServesThroughCallerPolicy(t *testing.T) {
	fake := &fakeVmmd{}
	c := unixHopClient(startUnixHop(t, fake))
	defer c.Close()
	var dials int
	dial := c.dial
	c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		return dial(ctx, network, address)
	}
	for range 3 {
		if _, body, err := forward(t, context.Background(), c, "x"); err != nil || body != "x" {
			t.Fatalf("hop over unix: body %q, err %v", body, err)
		}
	}
	if dials != 1 || fake.calls.Load() != 3 {
		t.Fatalf("dials %d calls %d, want 1 and 3", dials, fake.calls.Load())
	}
	conn, err := grpc.NewClient("unix://"+startUnixGRPC(t, fake), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s, err := vmmdpb.NewVmmdClient(conn).ForwardHTTPStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1"}}})
	_ = s.CloseSend()
	for {
		if _, err := s.Recv(); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("gRPC over unix: %v", err)
			}
			break
		}
	}
}
