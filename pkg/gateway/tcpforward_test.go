// adr: 576
package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
)

// tcpStubStream is a vmmd ForwardTCPStream that answers the init with
// initErr, then echoes every body chunk until the client half-closes.
type tcpStubStream struct {
	grpc.ClientStream
	ctx     context.Context
	initErr string

	mu       sync.Mutex
	sentInit bool
	sends    [][]byte
	replies  chan *vmmdpb.ForwardTCPResponse
	closed   bool
}

func newTCPStubStream(ctx context.Context, initErr string) *tcpStubStream {
	s := &tcpStubStream{ctx: ctx, initErr: initErr, replies: make(chan *vmmdpb.ForwardTCPResponse, 16)}
	return s
}

func (s *tcpStubStream) Context() context.Context { return s.ctx }

func (s *tcpStubStream) Send(req *vmmdpb.ForwardTCPRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.GetInit() != nil {
		s.sentInit = true
		s.replies <- &vmmdpb.ForwardTCPResponse{Frame: &vmmdpb.ForwardTCPResponse_Init{Init: &vmmdpb.ForwardTCPResponseInit{Error: s.initErr}}}
		return nil
	}
	chunk := append([]byte(nil), req.GetBodyChunk()...)
	s.sends = append(s.sends, chunk)
	s.replies <- &vmmdpb.ForwardTCPResponse{Frame: &vmmdpb.ForwardTCPResponse_BodyChunk{BodyChunk: chunk}}
	return nil
}

func (s *tcpStubStream) CloseSend() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.replies)
	}
	return nil
}

func (s *tcpStubStream) Recv() (*vmmdpb.ForwardTCPResponse, error) {
	select {
	case reply, ok := <-s.replies:
		if !ok {
			return nil, io.EOF
		}
		return reply, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *tcpStubStream) bodyBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, chunk := range s.sends {
		n += len(chunk)
	}
	return n
}

type tcpStubVmmd struct {
	vmmdpb.VmmdClient
	initErr string
	opened  chan *tcpStubStream
}

func (v *tcpStubVmmd) ForwardTCPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardTCPRequest, vmmdpb.ForwardTCPResponse], error) {
	stream := newTCPStubStream(ctx, v.initErr)
	v.opened <- stream
	return stream, nil
}

// tcpStubNodes resolves any non-empty node to the stub vmmd.
type tcpStubNodes struct{ cli vmmdpb.VmmdClient }

func (n tcpStubNodes) ClientFor(_ context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if nodeID == "" {
		return nil, nil, false
	}
	return n.cli, io.NopCloser(nil), true
}

func tcpForwarderWithStub(initErr string) (TCPForwarder, *tcpStubVmmd) {
	vmmd := &tcpStubVmmd{initErr: initErr, opened: make(chan *tcpStubStream, 4)}
	return TCPForwarder{Nodes: tcpStubNodes{cli: vmmd}, IdleTimeout: time.Minute}, vmmd
}

// A failed guest dial hands back an open, unread connection so the caller
// can try another replica.
func TestServeConnAwaitingDialHandsBackUnreachableGuest(t *testing.T) {
	forwarder, vmmd := tcpForwarderWithStub("connection refused")
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	written := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte("startup-packet"))
		written <- err
	}()
	err := forwarder.ServeConnAwaitingDial(context.Background(), server, Target{AppID: "app", NodeID: "node", InstanceID: "inst", Port: 5432})
	if !errors.Is(err, ErrTCPGuestUnreachable) {
		t.Fatalf("ServeConnAwaitingDial err = %v, want ErrTCPGuestUnreachable", err)
	}
	stream := <-vmmd.opened
	if stream.bodyBytes() != 0 {
		t.Fatalf("forwarded %d client bytes to an unreachable guest", stream.bodyBytes())
	}
	// The connection is still open and unread: the pending client bytes are
	// delivered to whoever serves it next.
	buf := make([]byte, len("startup-packet"))
	if _, err := io.ReadFull(server, buf); err != nil || string(buf) != "startup-packet" {
		t.Fatalf("handed-back connection read = %q, %v", buf, err)
	}
	if err := <-written; err != nil {
		t.Fatalf("client write: %v", err)
	}
	_ = server.Close()

	// Unreachable nodes are retryable too.
	client2, server2 := net.Pipe()
	defer func() { _ = client2.Close(); _ = server2.Close() }()
	if err := forwarder.ServeConnAwaitingDial(context.Background(), server2, Target{AppID: "app", Port: 5432}); !errors.Is(err, ErrTCPGuestUnreachable) {
		t.Fatalf("unknown node: err = %v, want ErrTCPGuestUnreachable", err)
	}
}

// After a confirmed dial the session forwards both directions and closes
// the connection, exactly like ServeConn.
func TestServeConnAwaitingDialForwardsAfterDial(t *testing.T) {
	forwarder, _ := tcpForwarderWithStub("")
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan error, 1)
	go func() {
		done <- forwarder.ServeConnAwaitingDial(context.Background(), server, Target{AppID: "app", NodeID: "node", InstanceID: "inst", Port: 6379})
	}()
	if _, err := client.Write([]byte("PING")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(client, reply); err != nil || string(reply) != "PING" {
		t.Fatalf("echo = %q, %v", reply, err)
	}
	_ = client.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ServeConnAwaitingDial = %v, want a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not end after the client closed")
	}
}

// ServeConn keeps its contract: a failed dial is a gRPC Unavailable and the
// connection is closed.
func TestServeConnClosesOnUnreachableGuest(t *testing.T) {
	forwarder, _ := tcpForwarderWithStub("connection refused")
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	err := forwarder.ServeConn(context.Background(), server, Target{AppID: "app", NodeID: "", Port: 5432})
	if err == nil || errors.Is(err, ErrTCPGuestUnreachable) {
		t.Fatalf("ServeConn err = %v, want a non-retryable transport error", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("ServeConn left the connection open: read err = %v", err)
	}
}
