package vmmdpb

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestUDPFramesRetainEmptyDatagramPresence(t *testing.T) {
	for _, payload := range [][]byte{nil, {}, {0, 255, 0}, []byte("datagram")} {
		request := &ForwardUDPRequest{Frame: &ForwardUDPRequest_Datagram{Datagram: payload}}
		wire, err := proto.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ForwardUDPRequest
		if err := proto.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		frame, ok := decoded.GetFrame().(*ForwardUDPRequest_Datagram)
		if !ok || !bytes.Equal(frame.Datagram, payload) {
			t.Fatalf("request datagram lost presence or content: %#v", decoded.GetFrame())
		}
		response := &ForwardUDPResponse{Frame: &ForwardUDPResponse_Datagram{Datagram: payload}}
		wire, err = proto.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var decodedResponse ForwardUDPResponse
		if err := proto.Unmarshal(wire, &decodedResponse); err != nil {
			t.Fatal(err)
		}
		reply, ok := decodedResponse.GetFrame().(*ForwardUDPResponse_Datagram)
		if !ok || !bytes.Equal(reply.Datagram, payload) {
			t.Fatalf("response datagram lost presence or content: %#v", decodedResponse.GetFrame())
		}
	}
	var missing ForwardUDPRequest
	if missing.GetFrame() != nil {
		t.Fatal("missing frame confused with empty datagram")
	}
}
