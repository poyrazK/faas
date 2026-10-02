package vmmdgrpc

import (
	"bytes"
	"io"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/udpwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type udpTestStream struct {
	grpc.ServerStream
	requests  []*vmmdpb.ForwardUDPRequest
	responses []*vmmdpb.ForwardUDPResponse
	index     int
}

func (s *udpTestStream) Recv() (*vmmdpb.ForwardUDPRequest, error) {
	if s.index == len(s.requests) {
		return nil, io.EOF
	}
	frame := s.requests[s.index]
	s.index++
	return frame, nil
}
func (s *udpTestStream) Send(frame *vmmdpb.ForwardUDPResponse) error {
	s.responses = append(s.responses, frame)
	return nil
}

func TestUDPStreamPreservesFrameBoundaries(t *testing.T) {
	messages := [][]byte{nil, []byte("one"), {0, 255, 0}}
	stream := &udpTestStream{}
	for _, payload := range messages {
		stream.requests = append(stream.requests, &vmmdpb.ForwardUDPRequest{Frame: &vmmdpb.ForwardUDPRequest_Datagram{Datagram: payload}})
	}
	var pipe bytes.Buffer
	if err := udpRequestFrames(stream, &pipe, newUDPBudget(&vmmdpb.ForwardUDPRequestInit{})); err != nil {
		t.Fatal(err)
	}
	if err := udpResponseFrames(stream, &pipe, newUDPBudget(&vmmdpb.ForwardUDPRequestInit{})); err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) != len(messages) {
		t.Fatalf("response datagrams=%d", len(stream.responses))
	}
	for i, want := range messages {
		frame, ok := stream.responses[i].GetFrame().(*vmmdpb.ForwardUDPResponse_Datagram)
		if !ok || !bytes.Equal(frame.Datagram, want) {
			t.Fatalf("datagram %d changed", i)
		}
	}
}

func TestUDPStreamBudgets(t *testing.T) {
	b := newUDPBudget(&vmmdpb.ForwardUDPRequestInit{MaxBytes: 2, MaxDatagrams: 2})
	if err := b.consume(nil); err != nil {
		t.Fatal(err)
	}
	if err := b.consume([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if code := status.Code(b.consume(nil)); code != codes.ResourceExhausted {
		t.Fatalf("empty messages bypassed budget: %s", code)
	}
	b = newUDPBudget(&vmmdpb.ForwardUDPRequestInit{MaxBytes: 1})
	if code := status.Code(b.consume([]byte("ab"))); code != codes.ResourceExhausted {
		t.Fatalf("byte cap bypassed: %s", code)
	}
	b = newUDPBudget(&vmmdpb.ForwardUDPRequestInit{MaxBytes: api.UDPStreamMaxBytes + 1, MaxDatagrams: api.UDPStreamMaxDatagrams + 1})
	if b.bytes != api.UDPStreamMaxBytes || b.datagrams != api.UDPStreamMaxDatagrams {
		t.Fatal("caller raised receiver caps")
	}
	if code := status.Code(b.consume(make([]byte, api.UDPDatagramMaxBytes+1))); code != codes.InvalidArgument {
		t.Fatalf("oversized payload accepted: %s", code)
	}
}

func TestUDPStreamRejectsRepeatedInitAndOversizedResponse(t *testing.T) {
	stream := &udpTestStream{requests: []*vmmdpb.ForwardUDPRequest{{Frame: &vmmdpb.ForwardUDPRequest_Init{Init: &vmmdpb.ForwardUDPRequestInit{}}}}}
	var pipe bytes.Buffer
	if code := status.Code(udpRequestFrames(stream, &pipe, newUDPBudget(&vmmdpb.ForwardUDPRequestInit{}))); code != codes.InvalidArgument {
		t.Fatalf("init accepted as data: %s", code)
	}
	if pipe.Len() != 0 {
		t.Fatal("invalid frame reached helper")
	}
	if err := udpwire.Write(&pipe, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	if code := status.Code(udpResponseFrames(stream, &pipe, newUDPBudget(&vmmdpb.ForwardUDPRequestInit{MaxBytes: 1}))); code != codes.ResourceExhausted {
		t.Fatalf("response byte cap: %s", code)
	}
	if len(stream.responses) != 0 {
		t.Fatal("over-budget response forwarded")
	}
}
