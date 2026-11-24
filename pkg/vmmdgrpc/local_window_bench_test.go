package vmmdgrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"

	vmmdv1 "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/wire"
)

// echoForwardServer answers a ForwardHTTPStream the way a small production
// forward does on the wire: it reads the request until the client half-closes,
// then sends a response head and a short body as two messages and returns.
type echoForwardServer struct {
	vmmdv1.UnimplementedVmmdServer
	head, body []byte
}

func (s echoForwardServer) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdv1.ForwardHTTPStreamRequest, vmmdv1.ForwardHTTPStreamResponse]) error {
	for {
		if _, err := stream.Recv(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
	}
	if err := stream.Send(&vmmdv1.ForwardHTTPStreamResponse{Frame: &vmmdv1.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: s.head}}); err != nil {
		return err
	}
	return stream.Send(&vmmdv1.ForwardHTTPStreamResponse{Frame: &vmmdv1.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: s.body}})
}

// BenchmarkLocalDataForward measures one forward-shaped exchange over a real
// unix socket with gRPC's dynamic flow control ("dynamic", the default) and
// with wire.LocalStaticWindow* ("static"). Run it with -cpu 4 or more: the
// probe's cost is wakeups between goroutines and processes, which a serial
// run hides.
func BenchmarkLocalDataForward(b *testing.B) {
	request := make([]byte, 1287) // a production request head, in size
	head, body := make([]byte, 125), make([]byte, 74)
	for _, mode := range []struct {
		name   string
		dial   []grpc.DialOption
		server []grpc.ServerOption
	}{
		{"dynamic", nil, nil},
		{"static", wire.LocalStaticWindowDialOptions(), wire.LocalStaticWindowServerOptions()},
	} {
		b.Run(mode.name, func(b *testing.B) {
			dir, err := os.MkdirTemp("", "lw")
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(dir) }()
			sock := filepath.Join(dir, "d.sock")
			lis, err := net.Listen("unix", sock)
			if err != nil {
				b.Fatal(err)
			}
			srv := grpc.NewServer(mode.server...)
			vmmdv1.RegisterVmmdServer(srv, echoForwardServer{head: head, body: body})
			go func() { _ = srv.Serve(lis) }()
			defer srv.Stop()
			conn, err := wire.DialContext(context.Background(), "unix://"+sock, nil, mode.dial...)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			client := vmmdv1.NewVmmdClient(conn)
			exchange := func() {
				stream, err := client.ForwardHTTPStream(context.Background())
				if err != nil {
					b.Fatal(err)
				}
				if err := stream.Send(&vmmdv1.ForwardHTTPStreamRequest{Frame: &vmmdv1.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: request}}); err != nil {
					b.Fatal(err)
				}
				if err := stream.CloseSend(); err != nil {
					b.Fatal(err)
				}
				for {
					if _, err := stream.Recv(); errors.Is(err, io.EOF) {
						return
					} else if err != nil {
						b.Fatal(err)
					}
				}
			}
			for range 200 {
				exchange()
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					exchange()
				}
			})
		})
	}
}
