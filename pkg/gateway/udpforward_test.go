package gateway

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type udpEchoClient struct {
	vmmdpb.VmmdClient
	reply    func([]byte) *vmmdpb.ForwardUDPResponse
	terminal error
}

func (c udpEchoClient) ForwardUDPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse], error) {
	return &udpEchoStream{ctx: ctx, replies: make(chan *vmmdpb.ForwardUDPResponse, 8), reply: c.reply, terminal: c.terminal}, nil
}

type udpEchoStream struct {
	grpc.ClientStream
	ctx      context.Context
	replies  chan *vmmdpb.ForwardUDPResponse
	reply    func([]byte) *vmmdpb.ForwardUDPResponse
	terminal error
	receives int
}

func (s *udpEchoStream) Send(frame *vmmdpb.ForwardUDPRequest) error {
	var response *vmmdpb.ForwardUDPResponse
	if frame.GetInit() != nil {
		response = &vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Init{Init: &vmmdpb.ForwardUDPResponseInit{}}}
	} else {
		response = &vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Datagram{Datagram: append([]byte(nil), frame.GetDatagram()...)}}
		if s.reply != nil {
			response = s.reply(frame.GetDatagram())
		}
	}
	select {
	case s.replies <- response:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *udpEchoStream) Recv() (*vmmdpb.ForwardUDPResponse, error) {
	s.receives++
	if s.receives > 1 && s.terminal != nil {
		return nil, s.terminal
	}
	select {
	case frame := <-s.replies:
		return frame, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}
func (s *udpEchoStream) CloseSend() error { return nil }

type udpChannelPeer struct {
	requests chan []byte
	replies  chan []byte
}

func (p udpChannelPeer) Receive(ctx context.Context) ([]byte, error) {
	select {
	case payload, ok := <-p.requests:
		if !ok {
			return nil, io.EOF
		}
		return payload, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p udpChannelPeer) Send(ctx context.Context, payload []byte) error {
	select {
	case p.replies <- payload:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestUDPForwarderEmptyBinaryAndCancellation(t *testing.T) {
	peer := udpChannelPeer{requests: make(chan []byte, 2), replies: make(chan []byte, 2)}
	forwarder := UDPForwarder{Nodes: udpNodeLookup{}, IdleTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- forwarder.ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353})
	}()
	for _, payload := range [][]byte{nil, {0, 255, 0}} {
		peer.requests <- payload
		select {
		case got := <-peer.replies:
			if !bytes.Equal(got, payload) {
				t.Fatalf("datagram changed: %v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("forwarding stalled")
		}
	}
	cancel()
	select {
	case err := <-done:
		if status.Code(err) != codes.Canceled {
			t.Fatalf("cancellation status: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forwarder did not cancel")
	}
}
func TestUDPForwarderIdleExpiry(t *testing.T) {
	peer := udpChannelPeer{requests: make(chan []byte), replies: make(chan []byte)}
	forwarder := UDPForwarder{Nodes: udpNodeLookup{}, IdleTimeout: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if code := status.Code(forwarder.ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353})); code != codes.DeadlineExceeded {
		t.Fatalf("idle expiry status: %s", code)
	}
}

type udpNodeLookup struct{ client vmmdpb.VmmdClient }

func (n udpNodeLookup) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	if n.client != nil {
		return n.client, nil, true
	}
	return udpEchoClient{}, nil, true
}

func TestUDPForwarderRejectsDirectionalQuotaAndMalformedDatagrams(t *testing.T) {
	response := func(payload []byte) *vmmdpb.ForwardUDPResponse {
		return &vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Datagram{Datagram: payload}}
	}
	cases := []struct {
		name     string
		input    []byte
		maxBytes int64
		reply    func([]byte) *vmmdpb.ForwardUDPResponse
		want     codes.Code
	}{
		{name: "inbound byte cap", input: []byte("four"), maxBytes: 3, want: codes.ResourceExhausted},
		{name: "inbound oversize", input: make([]byte, api.UDPDatagramMaxBytes+1), want: codes.InvalidArgument},
		{name: "outbound byte cap", input: []byte("x"), maxBytes: 3, reply: func([]byte) *vmmdpb.ForwardUDPResponse { return response([]byte("four")) }, want: codes.ResourceExhausted},
		{name: "outbound oversize", input: []byte("x"), reply: func([]byte) *vmmdpb.ForwardUDPResponse { return response(make([]byte, api.UDPDatagramMaxBytes+1)) }, want: codes.InvalidArgument},
		{name: "repeated readiness", input: []byte("x"), reply: func([]byte) *vmmdpb.ForwardUDPResponse {
			return &vmmdpb.ForwardUDPResponse{Frame: &vmmdpb.ForwardUDPResponse_Init{Init: &vmmdpb.ForwardUDPResponseInit{}}}
		}, want: codes.Unavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := udpChannelPeer{requests: make(chan []byte, 1), replies: make(chan []byte, 1)}
			peer.requests <- tc.input
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			forwarder := UDPForwarder{Nodes: udpNodeLookup{client: udpEchoClient{reply: tc.reply}}, MaxBytes: tc.maxBytes}
			err := forwarder.ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353})
			if status.Code(err) != tc.want {
				t.Fatalf("status=%v want %v", err, tc.want)
			}
			select {
			case payload := <-peer.replies:
				t.Fatalf("invalid response reached peer (%d bytes)", len(payload))
			default:
			}
		})
	}
}
func TestUDPForwarderEmptyDatagramsExhaustMessageQuota(t *testing.T) {
	peer := udpChannelPeer{requests: make(chan []byte, 1), replies: make(chan []byte, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (UDPForwarder{Nodes: udpNodeLookup{}, MaxDatagrams: 1}).ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353})
	}()
	peer.requests <- nil
	select {
	case payload := <-peer.replies:
		if len(payload) != 0 {
			t.Fatal("empty datagram changed")
		}
	case <-ctx.Done():
		t.Fatal("first datagram stalled")
	}
	peer.requests <- nil
	select {
	case err := <-done:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("empty-datagram quota status=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("quota did not terminate peer")
	}
	select {
	case <-peer.replies:
		t.Fatal("over-quota empty datagram was forwarded")
	default:
	}
}
func TestUDPForwarderPreservesUnexpectedRemoteCancellation(t *testing.T) {
	peer := udpChannelPeer{requests: make(chan []byte), replies: make(chan []byte)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	forwarder := UDPForwarder{Nodes: udpNodeLookup{client: udpEchoClient{terminal: status.Error(codes.Canceled, "remote stream canceled")}}}
	if err := forwarder.ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353}); status.Code(err) != codes.Canceled {
		t.Fatalf("remote cancellation was reported as success: %v", err)
	}
}

func TestUDPForwarderCleanPeerEOF(t *testing.T) {
	peer := udpChannelPeer{requests: make(chan []byte), replies: make(chan []byte)}
	close(peer.requests)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (UDPForwarder{Nodes: udpNodeLookup{}}).ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353}); err != nil {
		t.Fatalf("clean peer EOF failed: %v", err)
	}
}

// A guest may emit multiple replies for one request. Outbound message credit
// must be independent of the request count and must charge empty replies.
type udpDoubleReplyClient struct{ vmmdpb.VmmdClient }

func (udpDoubleReplyClient) ForwardUDPStream(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardUDPRequest, vmmdpb.ForwardUDPResponse], error) {
	return &udpDoubleReplyStream{udpEchoStream: &udpEchoStream{ctx: ctx, replies: make(chan *vmmdpb.ForwardUDPResponse, 8)}}, nil
}

type udpDoubleReplyStream struct{ *udpEchoStream }

func (s *udpDoubleReplyStream) Send(frame *vmmdpb.ForwardUDPRequest) error {
	if err := s.udpEchoStream.Send(frame); err != nil {
		return err
	}
	if frame.GetInit() != nil {
		return nil
	}
	return s.udpEchoStream.Send(frame)
}
func TestUDPForwarderOutboundEmptyMessageQuotaIsIndependent(t *testing.T) {
	peer := udpChannelPeer{requests: make(chan []byte, 1), replies: make(chan []byte, 2)}
	peer.requests <- nil
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	forwarder := UDPForwarder{Nodes: udpNodeLookup{client: udpDoubleReplyClient{}}, MaxDatagrams: 1}
	if err := forwarder.ServePeer(ctx, peer, Target{NodeID: "node", InstanceID: "instance", Port: 5353}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("outbound quota status=%v", err)
	}
	if len(peer.replies) != 1 {
		t.Fatalf("forwarded replies=%d want one", len(peer.replies))
	}
	if payload := <-peer.replies; len(payload) != 0 {
		t.Fatal("empty reply changed")
	}
}
