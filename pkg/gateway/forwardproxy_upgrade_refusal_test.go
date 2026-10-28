package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// keepAliveRawStream is a guest that answers and then keeps its connection
// open: after the scripted frames, Recv blocks until the test ends.
type keepAliveRawStream struct {
	*fakeRawBidiStream
	frames []*vmmdpb.ForwardRawResponse
	next   int
	hold   chan struct{}
}

func (s *keepAliveRawStream) Recv() (*vmmdpb.ForwardRawResponse, error) {
	if s.next < len(s.frames) {
		frame := s.frames[s.next]
		s.next++
		return frame, nil
	}
	<-s.hold
	return nil, io.EOF
}

// TestRawStreamUpgradeRefusalEndsWithItsBody is issue #2681: a keep-alive
// guest that refuses a WebSocket upgrade does not close its socket, so the
// gateway must end the exchange once the refusal's declared body is relayed
// instead of holding the request until its budget expires.
func TestRawStreamUpgradeRefusalEndsWithItsBody(t *testing.T) {
	for name, tc := range map[string]struct {
		status        int32
		contentLength string
		chunks        []string
		wantBody      string
	}{
		"401 split across chunks": {status: 401, contentLength: "6", chunks: []string{"den", "ied"}, wantBody: "denied"},
		"426 with no body":        {status: 426, contentLength: "0", wantBody: ""},
		"trailing bytes dropped":  {status: 403, contentLength: "6", chunks: []string{"deniedEXTRA"}, wantBody: "denied"},
	} {
		t.Run(name, func(t *testing.T) {
			frames := []*vmmdpb.ForwardRawResponse{{Frame: &vmmdpb.ForwardRawResponse_Init{
				Init: &vmmdpb.ForwardRawResponseInit{Status: tc.status, Headers: []*vmmdpb.Header{
					{Name: "Content-Length", Value: tc.contentLength},
					{Name: "Content-Type", Value: "text/plain"},
				}},
			}}}
			for _, chunk := range tc.chunks {
				frames = append(frames, &vmmdpb.ForwardRawResponse{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{BodyChunk: []byte(chunk)}})
			}
			stream := &keepAliveRawStream{fakeRawBidiStream: &fakeRawBidiStream{}, frames: frames, hold: make(chan struct{})}
			t.Cleanup(func() { close(stream.hold) })
			proxy := gateway.ForwardingRawReverseProxy(&fakeNodeLookup{cli: &fakeVmmdClient{RawStream: stream}}, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "https://app.example.com/socket", nil)
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("x-faas-instance", "i-test")
			rec := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test", Port: 8080}).ServeHTTP(rec, req)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("the gateway is still waiting on the guest after relaying the refusal")
			}
			if rec.Code != int(tc.status) || rec.Body.String() != tc.wantBody {
				t.Fatalf("response = %d %q, want %d %q", rec.Code, rec.Body.String(), tc.status, tc.wantBody)
			}
		})
	}
}
