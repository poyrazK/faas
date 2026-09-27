// spec: §4.1

package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// failAfterFramesStream replays frames, then fails every later Recv with err:
// the guest bridge dying after the response is already committed.
type failAfterFramesStream struct {
	frames []*vmmdpb.ForwardHTTPStreamResponse
	err    error
}

func (s *failAfterFramesStream) Send(*vmmdpb.ForwardHTTPStreamRequest) error { return nil }
func (s *failAfterFramesStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	if len(s.frames) > 0 {
		f := s.frames[0]
		s.frames = s.frames[1:]
		return f, nil
	}
	return nil, s.err
}
func (s *failAfterFramesStream) CloseSend() error             { return nil }
func (s *failAfterFramesStream) Context() context.Context     { return context.Background() }
func (s *failAfterFramesStream) Header() (metadata.MD, error) { return nil, nil }
func (s *failAfterFramesStream) Trailer() metadata.MD         { return nil }
func (s *failAfterFramesStream) SendMsg(any) error            { return nil }
func (s *failAfterFramesStream) RecvMsg(any) error            { return nil }

type failAfterFramesClient struct {
	vmmdpb.VmmdClient
	stream *failAfterFramesStream
}

type singleClientLookup struct{ cli vmmdpb.VmmdClient }

func (l singleClientLookup) ClientFor(context.Context, string) (vmmdpb.VmmdClient, io.Closer, bool) {
	return l.cli, io.NopCloser(nil), true
}

func (c *failAfterFramesClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return c.stream, nil
}

// TestForwarder_MidStreamFailureAbortsCommittedResponse — once the init frame
// has put a status on the wire, a bridge failure must abort the response. The
// forwarder used to append a problem document to the customer's body under
// the committed 200, producing `{"items":[1,2{"type":...}` that terminated
// cleanly and looked complete.
func TestForwarder_MidStreamFailureAbortsCommittedResponse(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.NotFound, codes.Internal} {
		t.Run(code.String(), func(t *testing.T) {
			stream := &failAfterFramesStream{
				frames: []*vmmdpb.ForwardHTTPStreamResponse{
					{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
						Status:  http.StatusOK,
						Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
					}}},
					{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte(`{"items":[1,2`)}},
				},
				err: status.Error(code, "guest connection reset"),
			}
			nodes := singleClientLookup{cli: &failAfterFramesClient{stream: stream}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fwdOnceWithEvents(w, r, nodes, log, Target{NodeID: "n1"}, nil)
			}))
			t.Cleanup(srv.Close)
			// Silence net/http's "http: panic serving" line for the abort.
			srv.Config.ErrorLog = nil

			resp, err := srv.Client().Get(srv.URL + "/list")
			if err != nil {
				// Aborted before the buffered status line was flushed:
				// the client sees a transport error, which is the truth.
				return
			}
			defer func() { _ = resp.Body.Close() }()
			body, readErr := io.ReadAll(resp.Body)
			if strings.Contains(string(body), `"type"`) {
				t.Fatalf("problem document appended to committed %d body: %q", resp.StatusCode, body)
			}
			if readErr == nil {
				t.Fatalf("truncated body %q read back cleanly under %d; want a transport error", body, resp.StatusCode)
			}
		})
	}
}
