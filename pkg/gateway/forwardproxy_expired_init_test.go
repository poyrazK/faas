// adr: 570
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"google.golang.org/grpc"
)

type queuedInitStream[Request, Response any] struct {
	grpc.ClientStream
	frames []*Response
}

func (s *queuedInitStream[Request, Response]) Send(*Request) error { return nil }
func (s *queuedInitStream[Request, Response]) CloseSend() error    { return nil }
func (s *queuedInitStream[Request, Response]) Recv() (*Response, error) {
	if len(s.frames) == 0 {
		return nil, io.EOF
	}
	frame := s.frames[0]
	s.frames = s.frames[1:]
	return frame, nil
}

type queuedInitClient struct {
	vmmdpb.VmmdClient
	httpStream *queuedInitStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]
	rawStream  *queuedInitStream[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]
}

func (c *queuedInitClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return c.httpStream, nil
}

func (c *queuedInitClient) ForwardRawStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return c.rawStream, nil
}

// A queued init can arrive after the wall-clock budget expires, before its
// context timer publishes cancellation. The pending response still belongs to
// the gateway: committing guest headers would arm an expired socket deadline
// and turn the platform's timeout into an EOF.
func TestForwarderExpiredInitBeforeTimerCancellation(t *testing.T) {
	for _, raw := range []bool{false, true} {
		for _, code := range []int{http.StatusOK, http.StatusGatewayTimeout} {
			t.Run("raw="+strconv.FormatBool(raw)+"/status="+strconv.Itoa(code), func(t *testing.T) {
				client := &queuedInitClient{
					httpStream: &queuedInitStream[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]{frames: []*vmmdpb.ForwardHTTPStreamResponse{
						{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: int32(code)}}},
						{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("late guest response")}},
					}},
					rawStream: &queuedInitStream[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse]{frames: []*vmmdpb.ForwardRawResponse{
						{Frame: &vmmdpb.ForwardRawResponse_Init{Init: &vmmdpb.ForwardRawResponseInit{Status: int32(code)}}},
						{Frame: &vmmdpb.ForwardRawResponse_BodyChunk{BodyChunk: []byte("late guest response")}},
					}},
				}
				log := slog.New(slog.NewTextHandler(io.Discard, nil))
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:contextcheck // The synthetic parent retains r.Context but deliberately delays publishing its expired deadline.
					ctx := reqbudget.NewContext(expiredResponseDeadlineContext{Context: r.Context()}, reqbudget.Budget{
						Started: time.Now().Add(-2 * time.Second), Total: time.Second, Ceiling: time.Second,
					})
					if ctx.Err() != nil {
						t.Error("cancellation was already published before the queued init")
						return
					}
					r = r.WithContext(ctx) //nolint:contextcheck // Install the inherited synthetic deadline used to exercise the timer publication race.
					writer := &statusRecorder{ResponseWriter: w, trafficResponseContext: func() context.Context { return ctx }}
					defer writer.stopTrafficResponse()
					if raw {
						rawStreamOnceWithEvents(writer, r, client, log, Target{NodeID: "node"}, nil, nil)
					} else {
						fwdStreamOnceWithEvents(writer, r, client, log, Target{NodeID: "node"}, nil)
					}
				}))
				t.Cleanup(server.Close)
				httpClient := server.Client()
				httpClient.Timeout = time.Second
				response, err := httpClient.Get(server.URL)
				if err != nil {
					t.Fatalf("expired init lost its timeout response: %v", err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != http.StatusGatewayTimeout ||
					response.Header.Get(api.ErrorCodeHeader) != api.CodeRequestBudgetExceeded ||
					!strings.Contains(string(body), api.CodeRequestBudgetExceeded) || strings.Contains(string(body), "late guest response") {
					t.Fatalf("expired init status=%d body=%q error=%v", response.StatusCode, body, err)
				}
			})
		}
	}
}
