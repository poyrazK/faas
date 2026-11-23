package vmmdgrpc

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

type fwdStream = grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]

// perCallAdmittedStream is the earlier admittedHTTPStream shape, a goroutine
// per Recv and Send, kept here as the benchmark's baseline.
type perCallAdmittedStream struct {
	fwdStream
	ctx context.Context
}

func (s *perCallAdmittedStream) Recv() (*vmmdpb.ForwardHTTPStreamRequest, error) {
	type result struct {
		msg *vmmdpb.ForwardHTTPStreamRequest
		err error
	}
	done := make(chan result, 1)
	go func() { msg, err := s.fwdStream.Recv(); done <- result{msg, err} }()
	select {
	case <-s.ctx.Done():
		return nil, status.FromContextError(s.ctx.Err()).Err()
	case r := <-done:
		return r.msg, r.err
	}
}

func (s *perCallAdmittedStream) Send(msg *vmmdpb.ForwardHTTPStreamResponse) error {
	done := make(chan error, 1)
	go func() { done <- s.fwdStream.Send(msg) }()
	select {
	case <-s.ctx.Done():
		return status.FromContextError(s.ctx.Err()).Err()
	case err := <-done:
		return err
	}
}

// admittedBenchServer answers ForwardHTTPStream the way forwardHTTPStreamV2
// talks to its stream on a small request: Recv until EOF, then a response
// init, one body chunk and the trailers.
type admittedBenchServer struct {
	vmmdpb.UnimplementedVmmdServer
	wrap func(fwdStream) fwdStream
}

func (b *admittedBenchServer) ForwardHTTPStream(stream fwdStream) error {
	st := b.wrap(stream)
	for {
		if _, err := st.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	body := []byte("hello, world\n")
	for _, f := range []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 200,
			Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}}}}},
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: body}},
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{}}},
	} {
		if err := st.Send(f); err != nil {
			return err
		}
	}
	return nil
}

// BenchmarkAdmittedHTTPStream measures one small forward RPC through the
// admitted-stream wrapper over a real in-process gRPC connection.
func BenchmarkAdmittedHTTPStream(b *testing.B) {
	for _, tc := range []struct {
		name string
		wrap func(fwdStream) fwdStream
	}{
		{"per_call", func(s fwdStream) fwdStream { return &perCallAdmittedStream{fwdStream: s, ctx: s.Context()} }},
		{"pumps", func(s fwdStream) fwdStream { return newAdmittedHTTPStream(s, s.Context()) }},
	} {
		b.Run(tc.name, func(b *testing.B) {
			srv := grpc.NewServer()
			vmmdpb.RegisterVmmdServer(srv, &admittedBenchServer{wrap: tc.wrap})
			lis := bufconn.Listen(1 << 20)
			go func() { _ = srv.Serve(lis) }()
			defer srv.Stop()
			conn, err := grpc.NewClient("passthrough://bufnet",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }))
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			cli := vmmdpb.NewVmmdClient(conn)
			init := &vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{
				Init: &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1", Method: "GET", RequestUri: "/"}}}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				st, err := cli.ForwardHTTPStream(ctx)
				if err != nil {
					b.Fatal(err)
				}
				if err := st.Send(init); err != nil {
					b.Fatal(err)
				}
				if err := st.CloseSend(); err != nil {
					b.Fatal(err)
				}
				for {
					if _, err := st.Recv(); err != nil {
						if errors.Is(err, io.EOF) {
							break
						}
						b.Fatal(err)
					}
				}
			}
		})
	}
}
