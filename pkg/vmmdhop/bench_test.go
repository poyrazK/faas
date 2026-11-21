package vmmdhop

// adr: 20261121202814 — the hop's reason to exist is CPU per exchange. Run
// with -cpu 1 so ns/op is close to the CPU both ends spend:
//
//	go test -run '^$' -bench Forward -benchmem -cpu 1 ./pkg/vmmdhop/

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// benchExchange is a hello-world forward: init with a few headers, no body;
// response init and a 13-byte body.
func benchExchange(b *testing.B, s grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) {
	init := &vmmdpb.ForwardHTTPRequestInit{Instance: "i-1", Method: "GET", RequestUri: "/", Port: 8080, Stream: true, AppProtocol: "http1", Headers: []*vmmdpb.Header{
		{Name: "Host", Value: "app.gregale.dev"}, {Name: "User-Agent", Value: "k6/1.0"}, {Name: "Accept", Value: "*/*"},
		{Name: "Traceparent", Value: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"},
	}}
	if err := s.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: init}}); err != nil {
		b.Fatal(err)
	}
	_ = s.CloseSend()
	for {
		_, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkForwardGRPC(b *testing.B) {
	v := startVmmd(b, &fakeVmmd{}, false)
	conn, err := grpc.NewClient(v.addr, grpc.WithTransportCredentials(credentials.NewTLS(v.pki.clientTLS(b, "gatewayd-internal.faas"))))
	if err != nil {
		b.Fatal(err)
	}
	defer conn.Close()
	cli := vmmdpb.NewVmmdClient(conn)
	b.ReportAllocs()
	for b.Loop() {
		s, err := cli.ForwardHTTPStream(context.Background())
		if err != nil {
			b.Fatal(err)
		}
		benchExchange(b, s)
	}
}

func BenchmarkForwardHop(b *testing.B) {
	v := startVmmd(b, &fakeVmmd{}, true)
	c := v.client(b, "gatewayd-internal.faas")
	b.ReportAllocs()
	for b.Loop() {
		s, err := NewStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse](context.Background(), c, forwardMethod)
		if err != nil {
			b.Fatal(err)
		}
		benchExchange(b, s)
	}
}
